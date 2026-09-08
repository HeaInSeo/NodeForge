package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	godigest "github.com/opencontainers/go-digest"

	"github.com/HeaInSeo/sori"

	"github.com/HeaInSeo/NodeVault/pkg/registryconfig"
)

// mediaTypeToolSpec is the semantic kind of the spec referrer pkg/oras attaches
// after a successful build + registration. It is taken from sori rather than
// re-spelled here so the producer and this consumer cannot drift apart on the
// string — string drift is the exact failure mode this check exists to prevent.
const mediaTypeToolSpec = sori.MediaTypeToolSpec

// maxReferrerInspections bounds the extra manifest fetches a single
// SpecReferrerWitness call may perform while resolving candidate descriptors.
// Exceeding the budget is reported as indeterminate rather than absent, so a
// subject with a pathological number of referrers cannot silently downgrade a
// Healthy entry to Partial.
const maxReferrerInspections = 32

// maxReferrerPages bounds how many pages of a paginated referrers listing a
// single SpecReferrerWitness call will traverse. Outrunning it is reported as
// indeterminate, never as a confirmed absence.
const maxReferrerPages = 16

// maxEvidenceBytes bounds any single response the witness reads. A referrers
// page, a referrer manifest, and a ToolSpec payload are all small; refusing to
// accumulate more protects the check from a response that never ends.
const maxEvidenceBytes = 4 << 20

// witnessTimeout bounds one whole SpecReferrerWitness call — every page and
// every fetch it makes. The shared registry client has no timeout of its own, so
// without this a single response left open would stall the sequential reconcile
// pass and starve every entry behind it. Overridden in tests.
var witnessTimeout = 60 * time.Second

// decodeExactly decodes a single JSON value from r and requires the body to end
// there. json.Decoder.Decode stops at the first value, so trailing bytes would
// otherwise be ignored and a malformed response accepted as evidence.
//
// The read is bounded: an oversized body is reported rather than accumulated,
// and the caller's context deadline covers a body that simply never ends.
func decodeExactly(r io.Reader, v any) error {
	limited := &io.LimitedReader{R: r, N: maxEvidenceBytes + 1}
	dec := json.NewDecoder(limited)
	if err := dec.Decode(v); err != nil {
		if limited.N <= 0 {
			return fmt.Errorf("response exceeds the %d byte evidence limit", maxEvidenceBytes)
		}
		return err
	}
	var extra json.RawMessage
	err := dec.Decode(&extra)
	if limited.N <= 0 {
		return fmt.Errorf("response exceeds the %d byte evidence limit", maxEvidenceBytes)
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing data after the first JSON value")
	}
	return nil
}

// usableDigest reports whether a descriptor digest can identify a manifest.
// Validation is delegated to the canonical parser so the encoding and length are
// checked against the named algorithm — a shape-only check would still accept
// e.g. "sha256:" plus 64 non-hex characters. An unusable digest is never
// fetched: the request would 404 and masquerade as a clean nonmatch.
func usableDigest(d string) bool {
	return godigest.Digest(d).Validate() == nil
}

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

// witnessFor identifies the index entry whose ToolSpec referrer we are looking
// for. A ToolSpec referrer attached to the same image but belonging to a
// different entry is not evidence for this one: one image digest may carry
// several entries, each with its own ToolSpec.
type witnessFor struct {
	// referrerDigest is Entry.SpecReferrerDigest. When set it is the only
	// acceptable witness, matched exactly.
	referrerDigest string
	// casHash is Entry.CasHash, used when referrerDigest is unset — the referrer
	// payload names the entry it was pushed for, so the payload's cas_hash is what
	// ties an artifact back to this entry.
	casHash string
}

// SpecReferrerWitness reports whether the subject image carries a ToolSpec
// referrer belonging to this specific index entry. Uses the OCI referrers API:
// GET /v2/{name}/referrers/{digest}.
//
// IntegrityHealth=Healthy asserts a per-entry witness, not merely that the image
// has some ToolSpec referrer. Because uniqueness in the index is by CasHash, one
// image digest can carry several entries; entry A's referrer must never make
// entry B look Healthy.
//
// Evidence is established by exact semantic referrer kind, never by the mere
// presence of some referrer — an unrelated artifact, or the ToolProfile referrer
// that pkg/oras attaches to this very same subject digest, cannot witness it.
// Two descriptor shapes are recognized: typed descriptors carry the kind in
// artifactType, while sori's current push path stamps a generic image-manifest
// artifactType and records the kind only in the referrer manifest's
// config.mediaType, so those are resolved by a bounded manifest fetch rather
// than reported absent.
//
// Outcome contract matches the rest of this type: a confirmed absence is
// (false, nil); anything indeterminate — 401/403/5xx/timeout/decode failure, or
// evidence that outruns the inspection or page budgets — is (false, err), so the
// caller leaves integrity_health untouched instead of recording a false Partial
// or Missing.
func (c *HarborChecker) SpecReferrerWitness(
	ctx context.Context, imageRef, subjectDigest, expectedReferrerDigest, casHash string,
) (bool, error) {
	if imageRef == "" || subjectDigest == "" {
		return false, nil
	}
	host, name, err := parseRef(imageRef)
	if err != nil {
		return false, fmt.Errorf("spec referrer witness: %w", err)
	}
	// Bound the whole walk, so one unresponsive registry cannot stall reconcile.
	ctx, cancel := context.WithTimeout(ctx, witnessTimeout)
	defer cancel()

	if expectedReferrerDigest != "" && !usableDigest(expectedReferrerDigest) {
		// The entry records a digest that cannot identify a manifest, so no artifact
		// can validly match it. That is indeterminate, not a confirmed absence.
		return false, fmt.Errorf(
			"spec referrer witness: indeterminate: entry records an unusable spec referrer digest (%q)",
			expectedReferrerDigest)
	}
	want := witnessFor{referrerDigest: expectedReferrerDigest, casHash: casHash}
	pageURL := fmt.Sprintf("%s://%s/v2/%s/referrers/%s", c.scheme, host, name, subjectDigest)

	// deferred holds the first indeterminate outcome met while resolving
	// candidates. It is not returned immediately: a later descriptor may still be
	// this entry's readable witness, and positive evidence outranks an artifact we
	// could not read. It is returned only if the walk ends without a witness, so
	// an unreadable neighbor can never become a confirmed absence.
	var deferred error

	budget := maxReferrerInspections
	for page := 0; ; page++ {
		if page == maxReferrerPages {
			return false, fmt.Errorf(
				"spec referrer witness %s: indeterminate: listing exceeds the %d page budget",
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
				"spec referrer witness %s: indeterminate: continuation page disappeared", pageURL)
		}

		matched, pageDeferred := c.scanPage(ctx, host, name, pageURL, listing.descriptors, &budget, want)
		if matched {
			return true, nil
		}
		if deferred == nil {
			deferred = pageDeferred
		}

		// This page did not witness it, so an unusable continuation now matters.
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

	// The whole listing was walked without a witness. That is a confirmed absence
	// only if nothing was left unread.
	if deferred != nil {
		return false, deferred
	}
	return false, nil
}

// candidate is a descriptor that still needs a fetch before it can be accepted
// or dismissed as this entry's witness.
type candidate struct {
	digest    string
	kindKnown bool // true when the listing already proved it is a ToolSpec
}

// scanPage looks for this entry's witness among one page's descriptors. Typed
// descriptors are decided from the listing wherever possible; the rest are
// resolved with fetches drawn from the shared budget.
//
// A non-nil second return is an indeterminate outcome met on this page. It is
// advisory: the caller keeps walking and only falls back to it if the whole
// listing yields no witness.
func (c *HarborChecker) scanPage(
	ctx context.Context, host, name, pageURL string,
	descriptors []referrerDescriptor, budget *int, want witnessFor,
) (bool, error) {
	matched, candidates, deferred := triage(descriptors, pageURL, want)
	if matched {
		return true, nil
	}
	found, resolveDeferred := c.resolveCandidates(ctx, host, name, pageURL, candidates, budget, want)
	if found {
		return true, nil
	}
	if deferred == nil {
		deferred = resolveDeferred
	}
	return false, deferred
}

// triage sorts one page's descriptors into an immediate match, candidates that
// need a fetch, and an advisory indeterminate outcome.
func triage(descriptors []referrerDescriptor, pageURL string, want witnessFor) (bool, []candidate, error) {
	var (
		candidates []candidate
		deferred   error
	)
	defer1 := func(format string, args ...any) {
		if deferred == nil {
			deferred = fmt.Errorf(format, args...)
		}
	}
	for i := range descriptors {
		kind, decided := descriptors[i].kind()

		// With an expected referrer digest, only that exact artifact can witness
		// this entry, so every other descriptor is irrelevant — including ones we
		// could not read, which therefore raise no indeterminacy at all.
		if want.referrerDigest != "" {
			if descriptors[i].Digest != want.referrerDigest {
				continue
			}
			if !usableDigest(descriptors[i].Digest) {
				// A registry echoing the entry's own malformed digest proves nothing.
				defer1("spec referrer witness %s: indeterminate: matched descriptor digest is unusable (%q)",
					pageURL, descriptors[i].Digest)
				continue
			}
			switch {
			case decided && kind == mediaTypeToolSpec:
				return true, nil, nil
			case decided:
				continue // the expected digest is some other kind: not a witness
			default:
				candidates = append(candidates, candidate{digest: descriptors[i].Digest})
			}
			continue
		}

		// Without one, any ToolSpec referrer on this subject is a candidate, and
		// its payload has to name this entry.
		switch {
		case decided && kind == mediaTypeToolSpec && usableDigest(descriptors[i].Digest):
			candidates = append(candidates, candidate{digest: descriptors[i].Digest, kindKnown: true})
		case decided && kind == mediaTypeToolSpec:
			defer1("spec referrer witness %s: indeterminate: ToolSpec descriptor carries no usable digest (%q)",
				pageURL, descriptors[i].Digest)
		case decided:
			continue // some other kind (ToolProfile, or foreign): settled, not a witness
		case usableDigest(descriptors[i].Digest):
			candidates = append(candidates, candidate{digest: descriptors[i].Digest})
		default:
			// Untyped, with no usable digest to fetch: its kind can never be
			// established, so it must not be counted as a nonmatch.
			defer1("spec referrer witness %s: indeterminate: untyped referrer descriptor carries no usable digest (%q)",
				pageURL, descriptors[i].Digest)
		}
	}
	return false, candidates, deferred
}

// resolveCandidates fetches what triage could not decide, spending from the
// shared inspection budget.
func (c *HarborChecker) resolveCandidates(
	ctx context.Context, host, name, pageURL string,
	candidates []candidate, budget *int, want witnessFor,
) (bool, error) {
	var deferred error
	defer1 := func(format string, args ...any) {
		if deferred == nil {
			deferred = fmt.Errorf(format, args...)
		}
	}
	for _, cand := range candidates {
		if *budget == 0 {
			// Out of fetches, but later pages may still carry a decidable witness.
			defer1("spec referrer witness %s: indeterminate: candidates exceed the inspection budget of %d",
				pageURL, maxReferrerInspections)
			break
		}
		*budget--

		facts, factsErr := c.inspectReferrer(ctx, host, name, cand.digest)
		if factsErr != nil {
			defer1("spec referrer witness %s: %w", pageURL, factsErr)
			continue
		}
		if !facts.found {
			// Listed, but gone by the time we fetched it. It cannot witness this
			// entry, and its disappearance is confirmed rather than indeterminate —
			// so this must not be dragged into the payload lookup below.
			continue
		}
		if !cand.kindKnown && facts.kind != mediaTypeToolSpec {
			continue // resolved to something else: settled, not a witness
		}
		if want.referrerDigest != "" {
			// Reached only for the expected digest, whose kind is now confirmed.
			return true, nil
		}

		casHash, casErr := c.referrerCasHash(ctx, host, name, facts.configDigest)
		if casErr != nil {
			defer1("spec referrer witness %s: %w", pageURL, casErr)
			continue
		}
		if casHash == want.casHash {
			return true, nil
		}
		// A ToolSpec referrer naming a different entry: real evidence, just not
		// for this one.
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
		// A pointer distinguishes an explicitly empty listing, which is real absence
		// evidence, from a response that omits the field or sends null — those carry
		// no evidence at all and must not be read as "no referrers".
		Manifests *[]referrerDescriptor `json:"manifests"`
	}
	if decErr := decodeExactly(resp.Body, &idx); decErr != nil {
		return referrerListing{}, fmt.Errorf("referrer exists GET %s: decode response: %w", pageURL, decErr)
	}
	if idx.Manifests == nil {
		return referrerListing{}, fmt.Errorf(
			"referrer exists GET %s: indeterminate: response omits the manifests array", pageURL)
	}

	listing := referrerListing{descriptors: *idx.Manifests, found: true}
	// Resolve against the URL the response actually came from: a redirect would
	// otherwise make a relative continuation resolve against the wrong directory.
	base := pageURL
	if resp.Request != nil && resp.Request.URL != nil {
		base = resp.Request.URL.String()
	}
	next, nextErr := nextPageURL(base, resp.Header.Values("Link"))
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
	// A field that could not be parsed to the end may have hidden the
	// continuation. Positive evidence still wins — if a usable next link is found,
	// it is returned — but otherwise this is indeterminate, never "no next page".
	var malformed string
	for _, link := range links {
		entries, complete := splitLinkEntries(link)
		if !complete {
			malformed = link
		}
		for _, entry := range entries {
			// RFC 8288: a link-value begins with its <URI-Reference>. Anything before
			// it means the entry is malformed, and accepting the first bracket pair
			// found anywhere would follow a junk target instead of the real one.
			trimmed := strings.TrimSpace(entry)
			lo := strings.Index(trimmed, "<")
			hi := strings.Index(trimmed, ">")
			if lo != 0 || hi < lo {
				// Structurally malformed. If it nonetheless declares itself the
				// continuation, silently skipping it would strand an unvisited page.
				if hasNextRelation(trimmed) {
					return "", fmt.Errorf("advertised next page link is malformed: %q", entry)
				}
				continue
			}
			if !hasNextRelation(trimmed[hi+1:]) {
				continue
			}
			base, err := neturl.Parse(currentURL)
			if err != nil {
				return "", fmt.Errorf("resolve next page against %q: %w", currentURL, err)
			}
			ref, err := neturl.Parse(strings.TrimSpace(trimmed[lo+1 : hi]))
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
	if malformed != "" {
		return "", fmt.Errorf(
			"link field is structurally incomplete and may hide a continuation: %q", malformed)
	}
	return "", nil
}

// splitLinkEntries splits a Link header field into its comma-separated entries
// without breaking on a comma inside a <...> target or a quoted parameter value.
//
// The second return reports whether the field ended in a structurally complete
// state. A field that runs out mid-quote, mid-target, or mid-escape may have
// swallowed a delimiter — and with it a continuation — so the caller must not
// read it as "no next page".
func splitLinkEntries(link string) (entries []string, complete bool) {
	return splitUnquoted(link, ',', true)
}

// splitUnquoted splits s on sep, ignoring any separator that appears inside a
// quoted string or, when angleAware, inside a <...> target. A backslash-escaped
// character inside a quoted string is passed through without changing quote
// state, so an escaped quote cannot desynchronize the scan.
//
// complete is false when the input ends while still inside a quoted string, an
// unterminated <...> target, or a dangling escape. Everything after such a point
// was absorbed rather than parsed, so the split cannot be trusted to have seen
// every separator.
func splitUnquoted(s string, sep rune, angleAware bool) (parts []string, complete bool) {
	var (
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
	return parts, !inQuote && !inAngle && !escaped
}

// hasNextRelation reports whether a Link entry's parameter section declares the
// "next" relation. Per RFC 8288 the value may be quoted or bare, relation names
// are case-insensitive, and a single rel may list several space-separated types.
// Parameters are separated on unquoted semicolons only, so a quoted value that
// itself contains a semicolon is not mistaken for further parameters.
func hasNextRelation(params string) bool {
	// Completeness is judged for the whole Link field by the caller, so the
	// parameter-level split only needs the pieces it could parse.
	params2, _ := splitUnquoted(params, ';', false)
	for _, param := range params2 {
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

// referrerFacts is what one referrer manifest tells us about itself. found is
// false when the manifest was confirmed absent — listed, but already gone by the
// time it was fetched — which is a clean nonmatch rather than a failure.
type referrerFacts struct {
	kind         string
	configDigest string
	found        bool
}

// inspectReferrer fetches a single referrer manifest and reports its semantic
// kind and the digest of its config blob (which holds the artifact's payload).
//
// A confirmed 404 returns ("", nil): the descriptor was listed but the manifest
// is already gone, so it is not the spec referrer we are looking for. Every other
// non-200 is indeterminate and returns an error, as does a manifest that declares
// neither a recognized artifactType nor a config.mediaType — that identifies
// nothing, and must not be mistaken for a nonmatch.
func (c *HarborChecker) inspectReferrer(
	ctx context.Context, host, name, digest string,
) (referrerFacts, error) {
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", c.scheme, host, name, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return referrerFacts{}, fmt.Errorf("referrer inspect: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")

	resp, err := c.doWithAuthRetry(ctx, req)
	if err != nil {
		return referrerFacts{}, fmt.Errorf("referrer inspect GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return referrerFacts{found: false}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return referrerFacts{}, fmt.Errorf("referrer inspect GET %s: indeterminate status %d", url, resp.StatusCode)
	}

	var m struct {
		ArtifactType string `json:"artifactType"`
		Config       struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
		} `json:"config"`
	}
	if decErr := decodeExactly(resp.Body, &m); decErr != nil {
		return referrerFacts{}, fmt.Errorf("referrer inspect GET %s: decode manifest: %w", url, decErr)
	}

	facts := referrerFacts{configDigest: m.Config.Digest, found: true}
	// artifactType is authoritative wherever it says anything meaningful; the
	// config media type is a fallback only for the legacy generic value, matching
	// how the vendored ORAS client resolves a manifest's artifact type.
	switch {
	case m.ArtifactType != "" && m.ArtifactType != legacyGenericArtifactType:
		facts.kind = m.ArtifactType
	case m.Config.MediaType != "":
		facts.kind = m.Config.MediaType
	default:
		// Neither field identifies the artifact, so nothing was learned. Reporting
		// this as a nonmatch would let an unidentified referrer look like absence.
		return referrerFacts{}, fmt.Errorf(
			"referrer inspect GET %s: indeterminate: manifest declares no artifactType and no config.mediaType", url)
	}
	return facts, nil
}

// referrerCasHash reads the cas_hash recorded in a ToolSpec referrer's payload,
// which sori stores as the referrer manifest's config blob. It is what ties an
// artifact to the index entry it was pushed for.
//
// A payload that records no cas_hash identifies no entry, so it is indeterminate
// rather than a nonmatch.
func (c *HarborChecker) referrerCasHash(ctx context.Context, host, name, configDigest string) (string, error) {
	if !usableDigest(configDigest) {
		return "", fmt.Errorf(
			"referrer payload: indeterminate: manifest config carries no usable digest (%q)", configDigest)
	}
	url := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", c.scheme, host, name, configDigest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("referrer payload: build request: %w", err)
	}

	resp, err := c.doWithAuthRetry(ctx, req)
	if err != nil {
		return "", fmt.Errorf("referrer payload GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("referrer payload GET %s: indeterminate status %d", url, resp.StatusCode)
	}

	var payload struct {
		CasHash string `json:"cas_hash"`
	}
	if decErr := decodeExactly(resp.Body, &payload); decErr != nil {
		return "", fmt.Errorf("referrer payload GET %s: decode: %w", url, decErr)
	}
	if payload.CasHash == "" {
		return "", fmt.Errorf(
			"referrer payload GET %s: indeterminate: ToolSpec payload records no cas_hash", url)
	}
	return payload.CasHash, nil
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
	//nolint:gosec // G704: req.URL is built in ImageExists/SpecReferrerWitness/PullReachable from
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
