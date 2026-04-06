// Package registry handles internal container registry interactions.
// Responsible for verifying push success and acquiring image digests.
package registry

// Client is a stub — full implementation in Phase 2.
type Client struct{}

// NewClient creates a new registry client stub.
func NewClient() *Client {
	return &Client{}
}
