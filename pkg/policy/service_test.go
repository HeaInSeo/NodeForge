package policy

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"
)

func TestGetPolicyBundle_ReturnsBytes(t *testing.T) {
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "dockguard.wasm")
	payload := []byte("fake-wasm-bytes")
	if err := os.WriteFile(wasmPath, payload, 0o600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}

	svc := &Service{wasmPath: wasmPath}
	resp, err := svc.GetPolicyBundle(context.Background(), &nfv1.GetPolicyBundleRequest{})
	if err != nil {
		t.Fatalf("GetPolicyBundle: %v", err)
	}
	if !bytes.Equal(resp.WasmBytes, payload) {
		t.Errorf("WasmBytes mismatch: got %q want %q", resp.WasmBytes, payload)
	}
	if resp.BuiltAt == 0 {
		t.Error("BuiltAt should be non-zero")
	}
}

func TestGetPolicyBundle_FileMissing(t *testing.T) {
	svc := &Service{wasmPath: "/nonexistent/dockguard.wasm"}
	_, err := svc.GetPolicyBundle(context.Background(), &nfv1.GetPolicyBundleRequest{})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestListPolicies_ReturnsFourRules(t *testing.T) {
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "dockguard.wasm")
	if err := os.WriteFile(wasmPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}

	svc := &Service{wasmPath: wasmPath}
	resp, err := svc.ListPolicies(context.Background(), &nfv1.ListPoliciesRequest{})
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(resp.Policies) != 4 {
		t.Errorf("expected 4 policies, got %d", len(resp.Policies))
	}
	ids := map[string]bool{}
	for _, p := range resp.Policies {
		ids[p.RuleId] = true
	}
	for _, want := range []string{"DFM001", "DFM002", "DFM003", "DFM004"} {
		if !ids[want] {
			t.Errorf("missing policy rule %s", want)
		}
	}
}

func TestListPolicies_FileMissing(t *testing.T) {
	svc := &Service{wasmPath: "/nonexistent/dockguard.wasm"}
	_, err := svc.ListPolicies(context.Background(), &nfv1.ListPoliciesRequest{})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestNewService_DefaultPath(t *testing.T) {
	os.Unsetenv("DOCKGUARD_WASM_PATH")
	svc := NewService()
	if svc.wasmPath != defaultWasmPath {
		t.Errorf("default path: got %q want %q", svc.wasmPath, defaultWasmPath)
	}
}

func TestNewService_EnvOverride(t *testing.T) {
	t.Setenv("DOCKGUARD_WASM_PATH", "/custom/path.wasm")
	svc := NewService()
	if svc.wasmPath != "/custom/path.wasm" {
		t.Errorf("env override: got %q want %q", svc.wasmPath, "/custom/path.wasm")
	}
}
