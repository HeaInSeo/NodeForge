// Package ping implements the PingService gRPC handler.
// Used in Phase 0 to verify NodeKit ↔ NodeForge gRPC connectivity.
package ping

import (
	"context"
	"os"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"
)

// Handler implements nfv1.PingServiceServer.
type Handler struct {
	nfv1.UnimplementedPingServiceServer
}

// NewHandler creates a PingService handler.
func NewHandler() *Handler {
	return &Handler{}
}

// Ping responds with pong and the server hostname.
func (_ *Handler) Ping(_ context.Context, req *nfv1.PingRequest) (*nfv1.PingResponse, error) {
	host, _ := os.Hostname()
	return &nfv1.PingResponse{
		Message:  "pong: " + req.GetMessage(),
		ServerId: "NodeForge/" + host,
	}, nil
}
