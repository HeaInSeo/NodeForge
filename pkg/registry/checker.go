package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// HarborChecker implements reconcile.RegistryChecker using the OCI Distribution Spec API.
//
// imageRef must be in the form "host/project/repo:tag" (stored in index.Entry.ImageRef).
// digest must be the manifest digest "sha256:...".
//
// All checks use plain HTTP (http://) matching the existing registry client convention.
type HarborChecker struct {
	http *http.Client
}

// NewHarborChecker creates a HarborChecker using the default HTTP client.
func NewHarborChecker() *HarborChecker {
	return &HarborChecker{http: http.DefaultClient}
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
	url := fmt.Sprintf("http://%s/v2/%s/manifests/%s", host, name, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("image exists: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("image exists HEAD %s: %w", url, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// ReferrerExists checks whether any spec referrer is attached to the subject image.
// Uses the OCI referrers API: GET /v2/{name}/referrers/{digest}.
// Returns true if the response contains at least one referrer manifest.
func (c *HarborChecker) ReferrerExists(ctx context.Context, imageRef, subjectDigest string) (bool, error) {
	if imageRef == "" || subjectDigest == "" {
		return false, nil
	}
	host, name, err := parseRef(imageRef)
	if err != nil {
		return false, fmt.Errorf("referrer exists: %w", err)
	}
	url := fmt.Sprintf("http://%s/v2/%s/referrers/%s", host, name, subjectDigest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("referrer exists: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("referrer exists GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	// Parse the referrer index to check if any manifests are listed.
	var idx struct {
		Manifests []json.RawMessage `json:"manifests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return false, nil
	}
	return len(idx.Manifests) > 0, nil
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
	url := fmt.Sprintf("http://%s/v2/%s/manifests/%s", host, name, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("pull reachable: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("pull reachable GET %s: %w", url, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
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
