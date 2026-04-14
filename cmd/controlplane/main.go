// Package main is the NodeForge control plane entrypoint.
// Starts the gRPC server: PolicyService, BuildService, ValidateService, ToolRegistryService.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"

	"github.com/HeaInSeo/podbridge5"

	"github.com/HeaInSeo/NodeForge/pkg/build"
	"github.com/HeaInSeo/NodeForge/pkg/catalog"
	"github.com/HeaInSeo/NodeForge/pkg/index"
	"github.com/HeaInSeo/NodeForge/pkg/ping"
	"github.com/HeaInSeo/NodeForge/pkg/policy"
	"github.com/HeaInSeo/NodeForge/pkg/validate"
)

const defaultAddr = ":50051"

func main() {
	// Required before storage/build initialization in podbridge5 rootless mode.
	if podbridge5.ReexecIfNeeded() {
		os.Exit(0)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	addr := os.Getenv("NODEFORGE_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	addr = sanitizeLogValue(addr)

	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		//nolint:gosec // addr is normalized to a single-line value before logging.
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

	// Catalog + ToolRegistryService — RegisteredToolDefinition CAS storage + index.
	cat := catalog.NewCatalog()
	indexStore, indexErr := index.New()
	if indexErr != nil {
		slog.Error("failed to open index store", "err", indexErr)
		os.Exit(1)
	}
	registrySvc := catalog.NewToolRegistryService(cat, indexStore)
	nfv1.RegisterToolRegistryServiceServer(srv, registrySvc)

	// BuildService — image build+push → L3 → L4 → registration.
	buildSvc, err := build.NewService(validateSvc, registrySvc)
	if err != nil {
		slog.Warn("BuildService unavailable (kubeconfig missing?)", "err", err)
	} else {
		nfv1.RegisterBuildServiceServer(srv, buildSvc)
	}

	//nolint:gosec // listener address is normalized before being attached to logs.
	slog.Info("NodeForge gRPC server starting", "addr", sanitizeLogValue(lis.Addr().String()))

	if serveErr := srv.Serve(lis); serveErr != nil {
		slog.Error("server exited", "err", serveErr)
		os.Exit(1)
	}
}

func sanitizeLogValue(v string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, v)
}
