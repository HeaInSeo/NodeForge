package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// nodeVaultReferrerKinds is the set of semantic kinds NodeVault's producers emit.
// A descriptor carrying one of them is decidable from the referrers index alone;
// anything else — notably the generic image-manifest artifactType sori's current
// push path stamps on every referrer — must be resolved by fetching the manifest.
var nodeVaultReferrerKinds = map[string]bool{
	sori.MediaTypeToolSpec:     true,
	sori.MediaTypeToolProfile:  true,
	sori.MediaTypeDataSpec:     true,
	sori.MediaTypeSecurityScan: true,
}

// referrerDescriptor is one entry of an OCI referrers index response. An index
// descriptor carries no config, so artifactType is the only kind evidence the
// listing itself can offer.
type referrerDescriptor struct {
	Digest       string `json:"digest"`
	ArtifactType string `json:"artifactType"`
}

// kind returns the NodeVault semantic referrer kind this descriptor proves, or ""
// when the listing alone cannot decide it.
func (d *referrerDescriptor) kind() string {
	if nodeVaultReferrerKinds[d.ArtifactType] {
		return d.ArtifactType
	}
	return ""
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
	url := fmt.Sprintf("%s://%s/v2/%s/referrers/%s", c.scheme, host, name, subjectDigest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("referrer exists: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json")

	resp, err := c.doWithAuthRetry(ctx, req)
	if err != nil {
		return false, fmt.Errorf("referrer exists GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("referrer exists GET %s: indeterminate status %d", url, resp.StatusCode)
	}

	var idx struct {
		Manifests []referrerDescriptor `json:"manifests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return false, fmt.Errorf("referrer exists GET %s: decode response: %w", url, err)
	}

	// First pass: decide everything that needs no extra round trip, and collect
	// the descriptors whose kind the index does not reveal.
	var undecidable []string
	for i := range idx.Manifests {
		switch kind := idx.Manifests[i].kind(); {
		case kind == mediaTypeToolSpec:
			return true, nil
		case kind != "":
			continue // a different NodeVault kind (e.g. ToolProfile): decided, not a match
		case idx.Manifests[i].Digest != "":
			undecidable = append(undecidable, idx.Manifests[i].Digest)
		}
	}
	// Second pass: resolve legacy descriptors by reading each referrer manifest's
	// config.mediaType. Any indeterminate fetch aborts with an error rather than
	// letting a legacy-but-valid spec referrer look absent.
	for i, d := range undecidable {
		if i == maxReferrerInspections {
			return false, fmt.Errorf(
				"referrer exists GET %s: indeterminate: %d untyped referrers exceed the inspection budget of %d",
				url, len(undecidable), maxReferrerInspections)
		}
		kind, err := c.referrerKind(ctx, host, name, d)
		if err != nil {
			return false, fmt.Errorf("referrer exists GET %s: %w", url, err)
		}
		if kind == mediaTypeToolSpec {
			return true, nil
		}
	}

	// Nothing matched on this page. The referrers API may paginate, and the spec
	// referrer could be on a page we did not read, so an unfollowed continuation
	// is indeterminate rather than a confirmed absence.
	if resp.Header.Get("Link") != "" {
		return false, fmt.Errorf(
			"referrer exists GET %s: indeterminate: no spec referrer on the first page and the listing is paginated",
			url)
	}
	return false, nil
}

// referrerKind fetches a single referrer manifest and returns the semantic kind
// recorded in its config.mediaType.
//
// A confirmed 404 returns ("", nil): the descriptor was listed but the manifest
// is already gone, so it is not the spec referrer we are looking for. Every other
// non-200 is indeterminate and returns an error.
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
	if nodeVaultReferrerKinds[m.ArtifactType] {
		return m.ArtifactType, nil
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
