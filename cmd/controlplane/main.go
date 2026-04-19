// Package main is the NodeVault control plane entrypoint.
// Starts the gRPC server (PolicyService, BuildService, ValidateService, ToolRegistryService)
// and the read-only Catalog REST HTTP server.
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"google.golang.org/grpc"

	nfv1 "github.com/HeaInSeo/NodeVault/protos/nodeforge/v1"

	"github.com/HeaInSeo/podbridge5"

	"github.com/HeaInSeo/NodeVault/pkg/build"
	"github.com/HeaInSeo/NodeVault/pkg/catalog"
	"github.com/HeaInSeo/NodeVault/pkg/catalogrest"
	"github.com/HeaInSeo/NodeVault/pkg/index"
	"github.com/HeaInSeo/NodeVault/pkg/ping"
	"github.com/HeaInSeo/NodeVault/pkg/policy"
	"github.com/HeaInSeo/NodeVault/pkg/validate"
)

const (
	defaultGRPCAddr    = ":50051"
	defaultCatalogAddr = ":8080"
)

func main() {
	// Required before storage/build initialization in podbridge5 rootless mode.
	if podbridge5.ReexecIfNeeded() {
		os.Exit(0)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	grpcAddr := os.Getenv("NODEVAULT_ADDR")
	if grpcAddr == "" {
		grpcAddr = defaultGRPCAddr
	}
	grpcAddr = sanitizeLogValue(grpcAddr)

	catalogAddr := os.Getenv("CATALOG_HTTP_ADDR")
	if catalogAddr == "" {
		catalogAddr = defaultCatalogAddr
	}
	catalogAddr = sanitizeLogValue(catalogAddr)

	// ── Shared storage ──────────────────────────────────────────────────────

	cat := catalog.NewCatalog()
	dataCat := catalog.NewDataCatalog()
	indexStore, indexErr := index.New()
	if indexErr != nil {
		slog.Error("failed to open index store", "err", indexErr)
		os.Exit(1)
	}

	// ── Catalog REST HTTP server ─────────────────────────────────────────────

	catalogMux := catalogrest.NewMux(indexStore, cat, dataCat)
	go func() {
		slog.Info("Catalog REST server starting", "addr", catalogAddr)
		//nolint:gosec // catalogAddr is operator-configured and sanitized.
		if err := http.ListenAndServe(catalogAddr, catalogMux); err != nil {
			slog.Error("Catalog REST server exited", "err", err)
		}
	}()

	// ── gRPC server ──────────────────────────────────────────────────────────

	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", grpcAddr)
	if err != nil {
		//nolint:gosec // grpcAddr is normalized to a single-line value before logging.
		slog.Error("failed to listen", "addr", grpcAddr, "err", err)
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

	// ToolRegistryService — CAS storage + index dual-write (gRPC write path).
	registrySvc := catalog.NewToolRegistryService(cat, indexStore)
	nfv1.RegisterToolRegistryServiceServer(srv, registrySvc)

	// DataRegistryService — data artifact registration (gRPC write path).
	dataRegistrySvc := catalog.NewDataRegistryService(dataCat, indexStore)
	nfv1.RegisterDataRegistryServiceServer(srv, dataRegistrySvc)

	// BuildService — image build+push → L3 → L4 → registration.
	buildSvc, err := build.NewService(validateSvc, registrySvc)
	if err != nil {
		slog.Warn("BuildService unavailable (kubeconfig missing?)", "err", err)
	} else {
		nfv1.RegisterBuildServiceServer(srv, buildSvc)
	}

	//nolint:gosec // listener address is normalized before being attached to logs.
	slog.Info("NodeVault gRPC server starting", "addr", sanitizeLogValue(lis.Addr().String()))

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
