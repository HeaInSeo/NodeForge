package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"

	"github.com/HeaInSeo/sori"

	"github.com/HeaInSeo/NodeVault/pkg/registryconfig"
)

// mediaTypeToolSpec is the semantic kind of the spec referrer pkg/oras attaches
// after a successful build + registration. It is taken from sori rather than
// re-spelled here so the producer and this consumer cannot drift apart on the
// string — string drift is the exact failure mode this check exists to prevent.
const mediaTypeToolSpec = sori.MediaTypeToolSpec

// maxReferrerInspections bounds the extra manifest fetches a single
// ReferrerExists call may perform while resolving legacy untyped descriptors.
// Exceeding the budget is reported as indeterminate rather than absent, so a
// subject with a pathological number of referrers cannot silently downgrade a
// Healthy entry to Partial.
const maxReferrerInspections = 32

// maxReferrerPages bounds how many pages of a paginated referrers listing a
// single ReferrerExists call will traverse. Outrunning it is reported as
// indeterminate, never as a confirmed absence.
const maxReferrerPages = 16

// legacyGenericArtifactType is the artifactType sori's current push path stamps
// on every referrer regardless of kind. It identifies nothing, so it is the only
// value (besides an absent one) for which the semantic kind must be read from the
// referrer manifest's config.mediaType instead.
const legacyGenericArtifactType = "application/vnd.oci.image.manifest.v1+json"

// referrerDescriptor is one entry of an OCI referrers index response. An index
// descriptor carries no config, so artifactType is the only kind evidence the
// listing itself can offer.
type referrerDescriptor struct {
	Digest       string `json:"digest"`
	ArtifactType string `json:"artifactType"`
}

// kind returns the semantic referrer kind this descriptor states and whether the
// listing alone settles it. artifactType is authoritative when it says anything
// meaningful — including a foreign kind, which settles the descriptor as "not
// ours" without a fetch. Only an absent or legacy-generic artifactType leaves the
// kind undecided.
func (d *referrerDescriptor) kind() (string, bool) {
	if d.ArtifactType == "" || d.ArtifactType == legacyGenericArtifactType {
		return "", false
	}
	return d.ArtifactType, true
}

// HarborChecker implements reconcile.RegistryChecker using the OCI Distribution Spec API.
//
// imageRef must be in the form "host/project/repo:tag" (stored in index.Entry.ImageRef).
// digest must be the manifest digest "sha256:...".
//
// Scheme and TLS trust come from the registryconfig.Config passed to
// NewHarborChecker — the same settings pkg/oras uses for referrer push, so
// reconcile no longer trusts a different CA or scheme than the rest of
// NodeVault.
//
// Outcome contract: a 200 response means (true, nil). A confirmed 404 means
// (false, nil) — not found. Anything else (401/403/5xx/timeout/TLS failure)
// is indeterminate and returns (false, err); callers must not treat that
// error as "not found," since doing so would misclassify an auth challenge
// or transient failure as a missing artifact.
type HarborChecker struct {
	client *Client
	scheme string
}

// NewHarborChecker creates a HarborChecker using cfg's scheme and CA trust.
func NewHarborChecker(cfg registryconfig.Config) (*HarborChecker, error) {
	httpClient, err := cfg.HTTPClient()
	if err != nil {
		return nil, fmt.Errorf("registry: build HTTP client: %w", err)
	}
	return &HarborChecker{client: newClientWithHTTP(httpClient), scheme: cfg.Scheme}, nil
}

// ImageExists checks whether the manifest identified by digest exists in the registry.
// Uses HEAD /v2/{name}/manifests/{digest}.
func (c *HarborChecker) ImageExists(ctx context.Context, imageRef, digest string) (bool, error) {
	if imageRef == "" || digest == "" {
		return false, nil
	}
	host, name, err := parseRef(imageRef)
	if err != nil {
		return false, fmt.Errorf("image exists: %w", err)
	}
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", c.scheme, host, name, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("image exists: build request: %w", err)
	}
	resp, err := c.doWithAuthRetry(ctx, req)
	if err != nil {
		return false, fmt.Errorf("image exists HEAD %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return classifyExistence(url, resp.StatusCode)
}

// ReferrerExists reports whether the expected ToolSpec spec referrer is attached
// to the subject image. Uses the OCI referrers API: GET /v2/{name}/referrers/{digest}.
//
// The answer is established by exact semantic referrer kind, never by the mere
// presence of some referrer. index.HealthPartial is defined as "image OK, spec
// referrer missing", so an unrelated artifact — or a ToolProfile referrer, which
// pkg/oras.PushToolProfileReferrer attaches to this very same subject digest —
// must not be able to report the spec referrer as present.
//
// Two descriptor shapes are recognized:
//
//   - Typed: the referrers-index descriptor already carries the semantic kind in
//     artifactType.
//   - Legacy: sori's current push path packs every referrer with a generic
//     image-manifest artifactType and records the semantic kind only in the
//     referrer manifest's config.mediaType. Those artifacts are valid, so each
//     undecidable descriptor is resolved with a bounded manifest fetch rather
//     than being reported absent.
//
// Outcome contract matches the rest of this type: a confirmed absence is
// (false, nil); anything indeterminate — 401/403/5xx/timeout/decode failure, or
// more undecidable descriptors than the inspection budget allows — is
// (false, err), so the caller leaves integrity_health untouched instead of
// recording a false Partial or Missing.
func (c *HarborChecker) ReferrerExists(ctx context.Context, imageRef, subjectDigest string) (bool, error) {
	if imageRef == "" || subjectDigest == "" {
		return false, nil
	}
	host, name, err := parseRef(imageRef)
	if err != nil {
		return false, fmt.Errorf("referrer exists: %w", err)
	}
	pageURL := fmt.Sprintf("%s://%s/v2/%s/referrers/%s", c.scheme, host, name, subjectDigest)

	// deferred holds the first indeterminate outcome met while resolving untyped
	// referrers. It is not returned immediately: a later descriptor may still be a
	// readable ToolSpec, and positive evidence outranks an unrelated artifact we
	// could not read. It is returned only if the walk ends without a match, so an
	// unreadable neighbor can never become a confirmed absence.
	var deferred error

	budget := maxReferrerInspections
	for page := 0; ; page++ {
		if page == maxReferrerPages {
			return false, fmt.Errorf(
				"referrer exists %s: indeterminate: listing exceeds the %d page budget",
				pageURL, maxReferrerPages)
		}

		listing, pageErr := c.referrerPage(ctx, pageURL)
		if pageErr != nil {
			return false, pageErr
		}
		if !listing.found {
			if page == 0 {
				return false, nil // confirmed: the subject has no referrers
			}
			return false, fmt.Errorf(
				"referrer exists %s: indeterminate: continuation page disappeared", pageURL)
		}

		matched, pageDeferred := c.scanPage(ctx, host, name, pageURL, listing.descriptors, &budget)
		if matched {
			return true, nil
		}
		if deferred == nil {
			deferred = pageDeferred
		}

		// This page did not prove it, so an unusable continuation now matters.
		if listing.nextErr != nil {
			if deferred == nil {
				deferred = listing.nextErr
			}
			break
		}
		if listing.next == "" {
			break
		}
		pageURL = listing.next
	}

	// The whole listing was walked without finding a spec referrer. That is a
	// confirmed absence only if nothing was left unread.
	if deferred != nil {
		return false, deferred
	}
	return false, nil
}

// scanPage looks for the spec referrer among one page's descriptors. Typed
// descriptors are decided from the listing; the rest are resolved with manifest
// fetches drawn from the shared budget.
//
// A non-nil second return is an indeterminate outcome met on this page — an
// unreadable descriptor, or a spent budget. It is advisory: the caller keeps
// walking, because positive evidence elsewhere outranks a descriptor that could
// not be read, and only falls back to it if the whole listing yields no match.
func (c *HarborChecker) scanPage(
	ctx context.Context, host, name, pageURL string, descriptors []referrerDescriptor, budget *int,
) (bool, error) {
	var (
		undecidable []string
		deferred    error
	)
	for i := range descriptors {
		kind, decided := descriptors[i].kind()
		switch {
		case decided && kind == mediaTypeToolSpec && descriptors[i].Digest != "":
			return true, nil
		case decided && kind == mediaTypeToolSpec:
			// Claims to be the spec referrer but names no manifest: malformed, and
			// too weak to prove Healthy.
			if deferred == nil {
				deferred = fmt.Errorf(
					"referrer exists %s: indeterminate: ToolSpec descriptor carries no digest", pageURL)
			}
		case decided:
			continue // some other kind (ToolProfile, or foreign): settled, not a match
		case descriptors[i].Digest != "":
			undecidable = append(undecidable, descriptors[i].Digest)
		default:
			// Untyped and with no digest to fetch: its kind can never be established,
			// so it must not be counted as a nonmatch.
			if deferred == nil {
				deferred = fmt.Errorf(
					"referrer exists %s: indeterminate: untyped referrer descriptor carries no digest to inspect",
					pageURL)
			}
		}
	}
	for _, d := range undecidable {
		if *budget == 0 {
			if deferred == nil {
				deferred = fmt.Errorf(
					"referrer exists %s: indeterminate: untyped referrers exceed the inspection budget of %d",
					pageURL, maxReferrerInspections)
			}
			break
		}
		*budget--
		kind, kindErr := c.referrerKind(ctx, host, name, d)
		if kindErr != nil {
			if deferred == nil {
				deferred = fmt.Errorf("referrer exists %s: %w", pageURL, kindErr)
			}
			continue
		}
		if kind == mediaTypeToolSpec {
			return true, nil
		}
	}
	return false, deferred
}

// referrerListing is one decoded page of the referrers listing.
type referrerListing struct {
	descriptors []referrerDescriptor
	// next is the resolved rel="next" URL, or "" when this is the last page.
	next string
	// nextErr reports a continuation that was advertised but cannot be followed.
	// It is carried alongside the descriptors rather than returned as a hard error
	// so the caller can still scan this page: if the spec referrer is here, no
	// continuation needs following at all.
	nextErr error
	// found is false for a confirmed 404 — this subject has no referrers listing.
	found bool
}

// referrerPage fetches and decodes one page of the referrers listing.
func (c *HarborChecker) referrerPage(
	ctx context.Context, pageURL string,
) (referrerListing, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, http.NoBody)
	if err != nil {
		return referrerListing{}, fmt.Errorf("referrer exists: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json")

	resp, err := c.doWithAuthRetry(ctx, req)
	if err != nil {
		return referrerListing{}, fmt.Errorf("referrer exists GET %s: %w", pageURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return referrerListing{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return referrerListing{}, fmt.Errorf(
			"referrer exists GET %s: indeterminate status %d", pageURL, resp.StatusCode)
	}

	var idx struct {
		Manifests []referrerDescriptor `json:"manifests"`
	}
	if decErr := json.NewDecoder(resp.Body).Decode(&idx); decErr != nil {
		return referrerListing{}, fmt.Errorf("referrer exists GET %s: decode response: %w", pageURL, decErr)
	}

	listing := referrerListing{descriptors: idx.Manifests, found: true}
	next, nextErr := nextPageURL(pageURL, resp.Header.Values("Link"))
	if nextErr != nil {
		listing.nextErr = fmt.Errorf("referrer exists GET %s: indeterminate: %w", pageURL, nextErr)
	}
	listing.next = next
	return listing, nil
}

// nextPageURL extracts the rel="next" target from the response's Link header
// fields and resolves it against the current page URL. Every field is examined,
// not just the first: a registry may send several Link fields and put the
// continuation in a later one.
//
// ("", nil) means no continuation was advertised, so the caller may treat the
// current page as the last one. A non-nil error means a continuation WAS
// advertised but cannot be followed — off-origin, or unparseable. That is
// indeterminate, not the end of the listing: reporting it as the last page would
// let an unread continuation become a false confirmed absence.
func nextPageURL(currentURL string, links []string) (string, error) {
	for _, link := range links {
		for _, entry := range splitLinkEntries(link) {
			lo := strings.Index(entry, "<")
			hi := strings.Index(entry, ">")
			if lo < 0 || hi < lo {
				continue
			}
			if !hasNextRelation(entry[hi+1:]) {
				continue
			}
			base, err := neturl.Parse(currentURL)
			if err != nil {
				return "", fmt.Errorf("resolve next page against %q: %w", currentURL, err)
			}
			ref, err := neturl.Parse(strings.TrimSpace(entry[lo+1 : hi]))
			if err != nil {
				return "", fmt.Errorf("parse advertised next page link: %w", err)
			}
			resolved := base.ResolveReference(ref)
			// Never follow a continuation off the registry we were asked about, but do
			// not silently treat that refusal as the end of the listing either.
			if !sameOrigin(base, resolved) {
				return "", fmt.Errorf(
					"advertised next page %q is not on the same origin as %q", resolved.Redacted(), base.Redacted())
			}
			return resolved.String(), nil
		}
	}
	return "", nil
}

// splitLinkEntries splits a Link header field into its comma-separated entries
// without breaking on a comma inside a <...> target or a quoted parameter value.
func splitLinkEntries(link string) []string {
	return splitUnquoted(link, ',', true)
}

// splitUnquoted splits s on sep, ignoring any separator that appears inside a
// quoted string or, when angleAware, inside a <...> target. A backslash-escaped
// character inside a quoted string is passed through without changing quote
// state, so an escaped quote cannot desynchronize the scan.
func splitUnquoted(s string, sep rune, angleAware bool) []string {
	var (
		parts   []string
		buf     strings.Builder
		inAngle bool
		inQuote bool
		escaped bool
	)
	for _, r := range s {
		if escaped {
			escaped = false
			buf.WriteRune(r)
			continue
		}
		switch {
		case inQuote && r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case inQuote:
		case angleAware && r == '<':
			inAngle = true
		case angleAware && r == '>':
			inAngle = false
		case r == sep && !inAngle:
			parts = append(parts, buf.String())
			buf.Reset()
			continue
		}
		buf.WriteRune(r)
	}
	if strings.TrimSpace(buf.String()) != "" {
		parts = append(parts, buf.String())
	}
	return parts
}

// hasNextRelation reports whether a Link entry's parameter section declares the
// "next" relation. Per RFC 8288 the value may be quoted or bare, relation names
// are case-insensitive, and a single rel may list several space-separated types.
// Parameters are separated on unquoted semicolons only, so a quoted value that
// itself contains a semicolon is not mistaken for further parameters.
func hasNextRelation(params string) bool {
	for _, param := range splitUnquoted(params, ';', false) {
		key, value, ok := strings.Cut(param, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "rel") {
			continue
		}
		for _, rel := range strings.Fields(strings.Trim(strings.TrimSpace(value), `"`)) {
			if strings.EqualFold(rel, "next") {
				return true
			}
		}
	}
	return false
}

// sameOrigin compares scheme/host/port, treating host case and an omitted
// default port as equivalent so an equivalent origin spelled differently is not
// mistaken for a cross-origin redirect.
func sameOrigin(a, b *neturl.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		defaultedPort(a) == defaultedPort(b)
}

// defaultedPort returns u's explicit port, or the default port for its scheme.
func defaultedPort(u *neturl.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// referrerKind fetches a single referrer manifest and returns the semantic kind
// recorded in its config.mediaType.
//
// A confirmed 404 returns ("", nil): the descriptor was listed but the manifest
// is already gone, so it is not the spec referrer we are looking for. Every other
// non-200 is indeterminate and returns an error, as does a manifest that declares
// neither a recognized artifactType nor a config.mediaType — that identifies
// nothing, and must not be mistaken for a nonmatch.
func (c *HarborChecker) referrerKind(ctx context.Context, host, name, digest string) (string, error) {
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", c.scheme, host, name, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("referrer kind: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")

	resp, err := c.doWithAuthRetry(ctx, req)
	if err != nil {
		return "", fmt.Errorf("referrer kind GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("referrer kind GET %s: indeterminate status %d", url, resp.StatusCode)
	}

	var m struct {
		ArtifactType string `json:"artifactType"`
		Config       struct {
			MediaType string `json:"mediaType"`
		} `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return "", fmt.Errorf("referrer kind GET %s: decode manifest: %w", url, err)
	}
	// artifactType is authoritative wherever it says anything meaningful; the
	// config media type is a fallback only for the legacy generic value, matching
	// how the vendored ORAS client resolves a manifest's artifact type.
	if m.ArtifactType != "" && m.ArtifactType != legacyGenericArtifactType {
		return m.ArtifactType, nil
	}
	if m.Config.MediaType == "" {
		// Neither field identifies the artifact, so nothing was learned. Reporting
		// this as a nonmatch would let an unidentified referrer look like absence.
		return "", fmt.Errorf(
			"referrer kind GET %s: indeterminate: manifest declares no artifactType and no config.mediaType", url)
	}
	return m.Config.MediaType, nil
}

// PullReachable verifies the image manifest can be fetched (GET, not just HEAD).
// Returns true if the manifest is successfully retrieved.
func (c *HarborChecker) PullReachable(ctx context.Context, imageRef, digest string) (bool, error) {
	if imageRef == "" || digest == "" {
		return false, nil
	}
	host, name, err := parseRef(imageRef)
	if err != nil {
		return false, fmt.Errorf("pull reachable: %w", err)
	}
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", c.scheme, host, name, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("pull reachable: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")

	resp, err := c.doWithAuthRetry(ctx, req)
	if err != nil {
		return false, fmt.Errorf("pull reachable GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return classifyExistence(url, resp.StatusCode)
}

// classifyExistence turns an HTTP status into the reconcile outcome
// contract: 200 → (true, nil); 404 → (false, nil) confirmed not-found;
// anything else is indeterminate → (false, err).
func classifyExistence(url string, status int) (bool, error) {
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("%s: indeterminate status %d", url, status)
	}
}

// doWithAuthRetry performs req and, if the response is 401 with a usable
// Bearer challenge, transparently retries once with an anonymous token —
// the same flow Client.ResolveTagDigest uses for public registries. A 401
// without a usable challenge, or one that survives the retry, is returned
// as-is so the caller classifies it as indeterminate rather than "not found."
func (c *HarborChecker) doWithAuthRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	//nolint:gosec // G704: req.URL is built in ImageExists/ReferrerExists/PullReachable from
	// the operator-configured registry host and an index.Entry.ImageRef this process itself
	// wrote — not from an untrusted external request, so this is not an SSRF vector.
	resp, err := c.client.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	challenge, ok := parseBearerChallenge(resp.Header.Get("WWW-Authenticate"))
	if !ok {
		return resp, nil // no usable challenge; caller sees the 401 as-is
	}
	_ = resp.Body.Close()
	token, tokErr := c.client.anonymousToken(ctx, challenge)
	if tokErr != nil {
		return nil, fmt.Errorf("anonymous token: %w", tokErr)
	}
	retryReq := req.Clone(ctx)
	retryReq.Header.Set("Authorization", "Bearer "+token)
	//nolint:gosec // G704: same trust boundary as the request above (retry of the same URL).
	return c.client.http.Do(retryReq)
}

// parseRef splits "host/project/repo:tag" or "host/project/repo" into (host, name).
// name is everything after the host (project/repo without tag).
func parseRef(imageRef string) (host, name string, err error) {
	slash := strings.SplitN(imageRef, "/", 2)
	if len(slash) != 2 {
		return "", "", fmt.Errorf("invalid image ref (no '/'): %q", imageRef)
	}
	host = slash[0]
	// Strip tag if present.
	name = strings.SplitN(slash[1], ":", 2)[0]
	if name == "" {
		return "", "", fmt.Errorf("invalid image ref (empty name): %q", imageRef)
	}
	return host, name, nil
}
