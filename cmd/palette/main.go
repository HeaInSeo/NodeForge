// Package main is the NodePalette entrypoint.
// NodePalette is the read-only artifact palette service — it serves
// lifecycle_phase=Active tools and data artifacts to NodeKit and DagEdit.
//
// NodePalette runs as a separate host binary alongside NodeVault.
// It shares the same vault-index.json and CAS storage (assets/ directory).
// The index is re-read from disk on every request so that registrations
// by NodeVault (separate process) are immediately visible.
//
// Environment variables:
//
//	NODEPALETTE_ADDR   — HTTP listen address (default :8080)
//	INDEX_DIR          — vault-index.json directory (default assets/index)
//	CATALOG_DIR        — CAS tool definitions directory (default assets/catalog)
//	DATA_CATALOG_DIR   — CAS data definitions directory (default assets/datacatalog)
package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/HeaInSeo/NodeVault/pkg/catalog"
	"github.com/HeaInSeo/NodeVault/pkg/catalogrest"
	"github.com/HeaInSeo/NodeVault/pkg/index"
)

const defaultPaletteAddr = ":8080"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	addr := os.Getenv("NODEPALETTE_ADDR")
	if addr == "" {
		addr = defaultPaletteAddr
	}
	addr = sanitize(addr)

	// ── Storage (read-only access to shared assets/) ────────────────────────

	indexStore, err := index.New()
	if err != nil {
		slog.Error("failed to open index store", "err", err)
		os.Exit(1)
	}

	cat := catalog.NewCatalog()
	dataCat := catalog.NewDataCatalog()

	// ── HTTP server ──────────────────────────────────────────────────────────

	mux := catalogrest.NewMux(indexStore, cat, dataCat)

	// Reload middleware: re-reads vault-index.json from disk before each
	// request so NodePalette always reflects the latest NodeVault writes.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reloadErr := indexStore.Reload(); reloadErr != nil {
			slog.Warn("index reload failed", "err", reloadErr)
		}
		mux.ServeHTTP(w, r)
	})

	slog.Info("NodePalette starting", "addr", addr) //nolint:gosec // G706: addr is sanitized above.
	//nolint:gosec // addr is operator-configured and sanitized.
	if err := http.ListenAndServe(addr, handler); err != nil {
		slog.Error("NodePalette exited", "err", err)
		os.Exit(1)
	}
}

func sanitize(v string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, v)
}
