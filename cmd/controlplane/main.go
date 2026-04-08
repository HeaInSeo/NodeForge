// Package main is the NodeForge control plane entrypoint.
// Starts the gRPC server: PolicyService, BuildService, ValidateService, ToolRegistryService.
package main

import (
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"

	"github.com/HeaInSeo/NodeForge/pkg/build"
	"github.com/HeaInSeo/NodeForge/pkg/catalog"
	"github.com/HeaInSeo/NodeForge/pkg/ping"
	"github.com/HeaInSeo/NodeForge/pkg/policy"
	"github.com/HeaInSeo/NodeForge/pkg/validate"
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

	// PingService — Phase 0 connectivity check.
	nfv1.RegisterPingServiceServer(srv, ping.NewHandler())

	// PolicyService — serves dockguard.wasm bundle to NodeKit.
	nfv1.RegisterPolicyServiceServer(srv, policy.NewService())

	// ValidateService — L3 dry-run + L4 smoke run.
	validateSvc, err := validate.NewService()
	if err != nil {
		slog.Warn("ValidateService unavailable (kubeconfig missing?)", "err", err)
	} else {
		nfv1.RegisterValidateServiceServer(srv, validateSvc)
	}

	// Catalog + ToolRegistryService — RegisteredToolDefinition CAS storage.
	cat := catalog.NewCatalog()
	registrySvc := catalog.NewToolRegistryService(cat)
	nfv1.RegisterToolRegistryServiceServer(srv, registrySvc)

	// BuildService — kaniko Job orchestration → L3 → L4 → registration.
	buildSvc, err := build.NewService(validateSvc, registrySvc)
	if err != nil {
		slog.Warn("BuildService unavailable (kubeconfig missing?)", "err", err)
	} else {
		nfv1.RegisterBuildServiceServer(srv, buildSvc)
	}

	slog.Info("NodeForge gRPC server starting", "addr", addr)

	if serveErr := srv.Serve(lis); serveErr != nil {
		slog.Error("server exited", "err", serveErr)
		os.Exit(1)
	}
}
