package catalog_test

import (
	"os"
	"path/filepath"
	"testing"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"

	"github.com/HeaInSeo/NodeForge/pkg/catalog"
)

func newTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CATALOG_DIR", dir)
	return catalog.NewCatalog()
}

// TestSave_SameContent_SameHash verifies that identical content produces the same CAS key.
func TestSave_SameContent_SameHash(t *testing.T) {
	cat := newTestCatalog(t)

	tool := &nfv1.RegisteredToolDefinition{
		ToolName: "bwa-mem2",
		ImageUri: "registry.example.com/bwa-mem2:2.2.1",
		Digest:   "sha256:abc123",
	}

	hash1, err := cat.Save(tool)
	if err != nil {
		t.Fatalf("Save #1: %v", err)
	}

	hash2, err := cat.Save(tool)
	if err != nil {
		t.Fatalf("Save #2: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("same content produced different hashes: %s vs %s", hash1, hash2)
	}
}

// TestSave_DifferentContent_DifferentHash verifies distinct content produces distinct hashes.
func TestSave_DifferentContent_DifferentHash(t *testing.T) {
	cat := newTestCatalog(t)

	tool1 := &nfv1.RegisteredToolDefinition{ToolName: "tool-a", Digest: "sha256:aaa"}
	tool2 := &nfv1.RegisteredToolDefinition{ToolName: "tool-b", Digest: "sha256:bbb"}

	hash1, err := cat.Save(tool1)
	if err != nil {
		t.Fatalf("Save tool1: %v", err)
	}
	hash2, err := cat.Save(tool2)
	if err != nil {
		t.Fatalf("Save tool2: %v", err)
	}

	if hash1 == hash2 {
		t.Error("different content produced the same hash")
	}
}

// TestSave_FileExists verifies that a .tooldefinition file is written to disk.
func TestSave_FileExists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CATALOG_DIR", dir)
	cat := catalog.NewCatalog()

	tool := &nfv1.RegisteredToolDefinition{ToolName: "samtools", Digest: "sha256:def456"}
	hash, err := cat.Save(tool)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, hash+".tooldefinition")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("expected file %s to exist", path)
	}
}

// TestList_ReturnsAllSaved verifies that List returns all saved tools.
func TestList_ReturnsAllSaved(t *testing.T) {
	cat := newTestCatalog(t)

	tools := []*nfv1.RegisteredToolDefinition{
		{ToolName: "alpha", Digest: "sha256:001"},
		{ToolName: "beta", Digest: "sha256:002"},
		{ToolName: "gamma", Digest: "sha256:003"},
	}
	for _, tool := range tools {
		if _, err := cat.Save(tool); err != nil {
			t.Fatalf("Save %s: %v", tool.ToolName, err)
		}
	}

	listed, err := cat.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != len(tools) {
		t.Errorf("List returned %d tools, want %d", len(listed), len(tools))
	}
}

// TestRegisterTool_CasHashPopulated verifies RegisterTool sets CasHash on the returned tool.
func TestRegisterTool_CasHashPopulated(t *testing.T) {
	cat := newTestCatalog(t)
	svc := catalog.NewToolRegistryService(cat)

	req := &nfv1.RegisterToolRequest{
		RequestId:        "req-001",
		ToolDefinitionId: "def-001",
		ToolName:         "bwa",
		ImageUri:         "registry.example.com/bwa:1.0",
		Digest:           "sha256:abc",
		InputNames:       []string{"reads.fq"},
		OutputNames:      []string{"aligned.bam"},
	}

	resp, err := svc.RegisterTool(t.Context(), req)
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	if resp.CasHash == "" {
		t.Error("RegisterTool returned empty CasHash")
	}
	if resp.Tool == nil {
		t.Fatal("RegisterTool returned nil Tool")
	}
	if resp.Tool.CasHash != resp.CasHash {
		t.Errorf("Tool.CasHash %q != response CasHash %q", resp.Tool.CasHash, resp.CasHash)
	}
}

// TestListTools_AfterRegister verifies ListTools returns previously registered tools.
func TestListTools_AfterRegister(t *testing.T) {
	cat := newTestCatalog(t)
	svc := catalog.NewToolRegistryService(cat)

	for i, name := range []string{"star", "salmon"} {
		_, err := svc.RegisterTool(t.Context(), &nfv1.RegisterToolRequest{
			RequestId: "req-" + string(rune('0'+i)),
			ToolName:  name,
			Digest:    "sha256:000",
		})
		if err != nil {
			t.Fatalf("RegisterTool %s: %v", name, err)
		}
	}

	resp, err := svc.ListTools(t.Context(), &nfv1.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	// Each RegisterTool saves twice (initial + with CasHash), so dedup via unique names.
	names := make(map[string]struct{})
	for _, tool := range resp.Tools {
		names[tool.ToolName] = struct{}{}
	}
	for _, want := range []string{"star", "salmon"} {
		if _, ok := names[want]; !ok {
			t.Errorf("ListTools missing tool %q", want)
		}
	}
}
