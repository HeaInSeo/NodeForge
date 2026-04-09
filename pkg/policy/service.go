// Package policy manages DockGuard policy bundles.
// PolicyService serves the pre-built dockguard.wasm bundle to NodeKit clients.
// The bundle path is configured via DOCKGUARD_WASM_PATH (default: assets/policy/dockguard.wasm).
package policy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"
)

const defaultWasmPath = "assets/policy/dockguard.wasm"

// Service implements PolicyServiceServer.
type Service struct {
	nfv1.UnimplementedPolicyServiceServer
	wasmPath string
}

// NewService creates a PolicyService.
// DOCKGUARD_WASM_PATH env overrides the default bundle path.
func NewService() *Service {
	path := os.Getenv("DOCKGUARD_WASM_PATH")
	if path == "" {
		path = defaultWasmPath
	}
	return &Service{wasmPath: path}
}

// GetPolicyBundle reads the .wasm bundle from disk and returns it.
func (s *Service) GetPolicyBundle(_ context.Context, _ *nfv1.GetPolicyBundleRequest) (*nfv1.PolicyBundle, error) {
	data, err := os.ReadFile(s.wasmPath)
	if err != nil {
		return nil, fmt.Errorf("read wasm bundle %q: %w", s.wasmPath, err)
	}

	info, err := os.Stat(s.wasmPath)
	if err != nil {
		return nil, fmt.Errorf("stat wasm bundle: %w", err)
	}

	version := filepath.Base(filepath.Dir(s.wasmPath))
	if version == "." || version == "/" {
		version = "local"
	}

	return &nfv1.PolicyBundle{
		WasmBytes: data,
		Version:   version,
		BuiltAt:   info.ModTime().Unix(),
	}, nil
}

// ListPolicies returns metadata about the loaded policy bundle.
func (s *Service) ListPolicies(_ context.Context, _ *nfv1.ListPoliciesRequest) (*nfv1.ListPoliciesResponse, error) {
	info, err := os.Stat(s.wasmPath)
	if err != nil {
		return nil, fmt.Errorf("wasm bundle not found at %q: %w", s.wasmPath, err)
	}

	version := info.ModTime().Format(time.RFC3339)

	policies := []*nfv1.PolicyInfo{
		// ── dockerfile/multistage (DFM) ──────────────────────────────────────
		{
			RuleId:      "DFM001",
			Name:        "Exactly one FROM required",
			Version:     version,
			Description: "User Dockerfile must contain exactly one FROM instruction.",
		},
		{
			RuleId:      "DFM002",
			Name:        "AS builder alias required",
			Version:     version,
			Description: "FROM must include AS builder alias.",
		},
		{
			RuleId:      "DFM003",
			Name:        "AS final reserved",
			Version:     version,
			Description: "The alias 'final' is reserved; do not define it in user Dockerfiles.",
		},
		{
			RuleId:      "DFM004",
			Name:        "COPY --from=builder prohibited",
			Version:     version,
			Description: "COPY --from=builder must not appear in user-submitted Dockerfiles.",
		},
		// ── dockerfile/security (DSF) ─────────────────────────────────────────
		{
			RuleId:      "DSF001",
			Name:        "Non-root USER required",
			Version:     version,
			Description: "Dockerfile must specify a non-root USER instruction (not root or UID 0).",
		},
		{
			RuleId:      "DSF002",
			Name:        "No secrets in ENV",
			Version:     version,
			Description: "ENV instructions must not contain variables named PASSWORD, SECRET, API_KEY, TOKEN, or PASSWD.",
		},
		{
			RuleId:      "DSF003",
			Name:        "No remote ADD URLs",
			Version:     version,
			Description: "ADD instruction must not reference remote http:// or https:// URLs; use RUN curl/wget instead.",
		},
		// ── dockerfile/genomics (DGF) ─────────────────────────────────────────
		{
			RuleId:      "DGF001",
			Name:        "conda/mamba install version pinning required",
			Version:     version,
			Description: "RUN conda/mamba/micromamba install must specify exact versions (pkg=version) or use -f/--file.",
		},
		{
			RuleId:      "DGF002",
			Name:        "pip install version pinning required",
			Version:     version,
			Description: "RUN pip install must specify exact versions (pkg==version) or use -r/--requirement.",
		},
	}

	return &nfv1.ListPoliciesResponse{
		BundleVersion: version,
		Policies:      policies,
	}, nil
}
