// Package registry handles internal container registry interactions.
// Responsible for verifying push success and acquiring image digests
// via the OCI Distribution Spec registry API.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// acceptHeaders lists the manifest media types to request, in preference order.
var acceptHeaders = []string{
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.oci.image.index.v1+json",
}

// Client queries the internal container registry.
type Client struct {
	http *http.Client
}

// NewClient creates a registry Client using the default HTTP client.
func NewClient() *Client {
	return &Client{http: http.DefaultClient}
}

// newClientWithHTTP creates a registry Client with a custom HTTP client (for testing).
func newClientWithHTTP(h *http.Client) *Client {
	return &Client{http: h}
}

// GetDigest queries the registry manifest endpoint for the image digest.
// destination must be in the form "host:port/name:tag".
// It first checks the Docker-Content-Digest response header, then falls
// back to parsing config.digest from the manifest JSON body.
func (c *Client) GetDigest(ctx context.Context, destination string) (string, error) {
	host, name, tag, err := parseDestination(destination)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("http://%s/v2/%s/manifests/%s", host, name, tag)

	for _, accept := range acceptHeaders {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			return "", fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Accept", accept)

		resp, err := c.http.Do(req)
		if err != nil {
			return "", fmt.Errorf("registry GET %s: %w", url, err)
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		if d := resp.Header.Get("Docker-Content-Digest"); d != "" {
			return d, nil
		}

		var m struct {
			Config struct {
				Digest string `json:"digest"`
			} `json:"config"`
		}
		if jerr := json.NewDecoder(resp.Body).Decode(&m); jerr == nil && m.Config.Digest != "" {
			return m.Config.Digest, nil
		}
	}

	return "", fmt.Errorf("digest not found in registry response for %s", destination)
}

// parseDestination splits "host/name:tag" into its three components.
func parseDestination(destination string) (host, name, tag string, err error) {
	slash := strings.SplitN(destination, "/", 2)
	if len(slash) != 2 {
		return "", "", "", fmt.Errorf("invalid destination (no '/'): %s", destination)
	}
	host = slash[0]
	nameTag := strings.SplitN(slash[1], ":", 2)
	if len(nameTag) != 2 {
		return "", "", "", fmt.Errorf("invalid destination (no ':'): %s", destination)
	}
	return host, nameTag[0], nameTag[1], nil
}
