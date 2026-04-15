// Package catalogrest provides the read-only Catalog HTTP REST service.
//
// Endpoints:
//
//	GET /v1/catalog/tools                     — list active tools (query: stable_ref, artifact_kind)
//	GET /v1/catalog/tools/{cas_hash}          — get single tool by CAS hash
//
// Catalog 노출 규칙: lifecycle_phase = Active 기준만.
// integrity_health는 이 서비스가 노출 결정에 사용하지 않는다.
package catalogrest

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/HeaInSeo/NodeForge/pkg/catalog"
	"github.com/HeaInSeo/NodeForge/pkg/index"
	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"
)

// ToolItem is the JSON wire format for a single registered tool.
type ToolItem struct {
	CasHash         string `json:"cas_hash"`
	ToolName        string `json:"tool_name"`
	Version         string `json:"version"`
	StableRef       string `json:"stable_ref"`
	ImageUri        string `json:"image_uri"`
	Digest          string `json:"digest"`
	LifecyclePhase  string `json:"lifecycle_phase"`
	IntegrityHealth string `json:"integrity_health"`
	RegisteredAt    int64  `json:"registered_at"`
	DisplayLabel    string `json:"display_label,omitempty"`
	DisplayCategory string `json:"display_category,omitempty"`
	Command         string `json:"command,omitempty"`
}

// ListToolsResponse is the JSON body for GET /v1/catalog/tools.
type ListToolsResponse struct {
	Tools []ToolItem `json:"tools"`
}

// Server serves the read-only Catalog REST API.
type Server struct {
	store   *index.Store
	catalog *catalog.Catalog
}

// NewMux creates an http.ServeMux pre-wired with Catalog REST endpoints.
// The caller is responsible for binding it to an *http.Server.
func NewMux(store *index.Store, cat *catalog.Catalog) *http.ServeMux {
	s := &Server{store: store, catalog: cat}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/catalog/tools", s.handleListTools)
	mux.HandleFunc("GET /v1/catalog/tools/{cas_hash}", s.handleGetTool)
	return mux
}

// ── handlers ──────────────────────────────────────────────────────────────────

// handleListTools serves GET /v1/catalog/tools.
// Query parameters:
//   - stable_ref: filter by stable_ref (UI search key)
//   - artifact_kind: "tool" | "data" — empty returns all kinds
func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	stableRef := r.URL.Query().Get("stable_ref")
	kind := r.URL.Query().Get("artifact_kind")

	var entries []index.Entry
	var err error
	if stableRef != "" {
		entries, err = s.store.ListByStableRef(stableRef)
	} else {
		entries, err = s.store.ListActive()
	}
	if err != nil {
		http.Error(w, "index error", http.StatusInternalServerError)
		return
	}

	items := make([]ToolItem, 0, len(entries))
	for _, e := range entries {
		if kind != "" && string(e.ArtifactKind) != kind {
			continue
		}
		tool, loadErr := s.catalog.Load(e.CasHash)
		if loadErr != nil {
			// CAS file missing — skip; reconcile loop will update integrity_health.
			continue
		}
		items = append(items, toToolItem(tool, e.IntegrityHealth))
	}

	writeJSON(w, http.StatusOK, ListToolsResponse{Tools: items})
}

// handleGetTool serves GET /v1/catalog/tools/{cas_hash}.
func (s *Server) handleGetTool(w http.ResponseWriter, r *http.Request) {
	casHash := r.PathValue("cas_hash")
	if casHash == "" {
		http.Error(w, "cas_hash required", http.StatusBadRequest)
		return
	}

	entry, err := s.store.GetByCasHash(casHash)
	if err != nil {
		if errors.Is(err, index.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "index error", http.StatusInternalServerError)
		return
	}

	tool, err := s.catalog.Load(casHash)
	if err != nil {
		http.Error(w, "catalog load error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, toToolItem(tool, entry.IntegrityHealth))
}

// ── helpers ───────────────────────────────────────────────────────────────────

func toToolItem(t *nfv1.RegisteredToolDefinition, health index.IntegrityHealth) ToolItem {
	item := ToolItem{
		CasHash:         t.CasHash,
		ToolName:        t.ToolName,
		Version:         t.Version,
		StableRef:       t.StableRef,
		ImageUri:        t.ImageUri,
		Digest:          t.Digest,
		LifecyclePhase:  t.LifecyclePhase,
		IntegrityHealth: string(health),
		RegisteredAt:    t.RegisteredAt,
		Command:         t.Command,
	}
	if t.Display != nil {
		item.DisplayLabel = t.Display.Label
		item.DisplayCategory = t.Display.Category
	}
	return item
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
