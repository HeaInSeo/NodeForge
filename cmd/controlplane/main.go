// Package main is the NodeForge control plane entrypoint.
// Starts the gRPC server that serves PolicyService, BuildService, ValidateService, and ToolRegistryService.
package main

import (
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"

	"github.com/HeaInSeo/NodeForge/pkg/build"
	"github.com/HeaInSeo/NodeForge/pkg/catalog"
	"github.com/HeaInSeo/NodeForge/pkg/ping"
	"github.com/HeaInSeo/NodeForge/pkg/policy"
	"github.com/HeaInSeo/NodeForge/pkg/validate"
	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"
)

const defaultAddr = ":50051"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	addr := os.Getenv("NODEFORGE_ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("failed to listen", "addr", addr, "err", err)
		os.Exit(1)
	}

	srv := grpc.NewServer()

	// PingService — Phase 0 connectivity check
	nfv1.RegisterPingServiceServer(srv, ping.NewHandler())

	// Service stubs — proto-generated handlers registered in Phase 2
	_ = policy.NewService()
	_ = build.NewService()
	_ = validate.NewService()
	_ = catalog.NewCatalog()

	slog.Info("NodeForge gRPC server starting", "addr", addr)

	if err := srv.Serve(lis); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
