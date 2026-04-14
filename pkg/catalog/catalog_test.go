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
		Version:          "0.7.17",
		EnvironmentSpec:  "name: bwa\ndependencies:\n  - bwa=0.7.17=h5bf99c6_8\n",
		Inputs: []*nfv1.PortSpec{
			{Name: "reads.fq", Role: "sample-fastq", Format: "fastq", Shape: "pair", Required: true},
		},
		Outputs: []*nfv1.PortSpec{
			{Name: "aligned.bam", Role: "aligned-bam", Format: "bam", Shape: "single", Class: "primary"},
		},
		Display: &nfv1.DisplaySpec{
			Label:    "BWA 0.7.17",
			Category: "Alignment",
		},
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
	if resp.Tool.EnvironmentSpec != req.EnvironmentSpec {
		t.Errorf("Tool.EnvironmentSpec %q != request EnvironmentSpec %q", resp.Tool.EnvironmentSpec, req.EnvironmentSpec)
	}
}

// TestListTools_AfterRegister verifies ListTools returns previously registered tools.
// Each RegisterTool now writes exactly one file (SaveWithCasHash), so exactly N tools expected.
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
	if len(resp.Tools) != 2 {
		t.Errorf("ListTools returned %d tools, want exactly 2", len(resp.Tools))
	}
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

// TestRegisterTool_V02RoundTrip verifies that all v0.2 fields survive the
// RegisterTool → GetTool round-trip through CAS storage.
func TestRegisterTool_V02RoundTrip(t *testing.T) {
	cat := newTestCatalog(t)
	svc := catalog.NewToolRegistryService(cat)

	req := &nfv1.RegisterToolRequest{
		RequestId:        "req-v02",
		ToolDefinitionId: "def-v02",
		ToolName:         "bwa-mem2",
		Version:          "2.2.1",
		ImageUri:         "registry.example.com/bwa-mem2:2.2.1@sha256:deadbeef",
		Digest:           "sha256:deadbeef",
		EnvironmentSpec:  "name: bwa\ndependencies:\n  - bwa-mem2=2.2.1\n",
		Command:          "/usr/bin/bwa-mem2",
		Inputs: []*nfv1.PortSpec{
			{Name: "reads", Role: "sample-fastq", Format: "fastq", Shape: "pair", Required: true},
		},
		Outputs: []*nfv1.PortSpec{
			{Name: "aligned", Role: "aligned-bam", Format: "bam", Shape: "single", Class: "primary",
				Constraints: map[string]string{"sorted": "coordinate"}},
		},
		Display: &nfv1.DisplaySpec{
			Label:       "BWA-MEM2 2.2.1",
			Description: "Fast aligner",
			Category:    "Alignment",
			Tags:        []string{"wgs", "alignment"},
		},
	}

	regResp, err := svc.RegisterTool(t.Context(), req)
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	if regResp.CasHash == "" {
		t.Fatal("empty CasHash")
	}

	got, err := svc.GetTool(t.Context(), &nfv1.GetToolRequest{CasHash: regResp.CasHash})
	if err != nil {
		t.Fatalf("GetTool: %v", err)
	}

	// ── v0.2 field round-trip assertions ────────────────────────────────────
	if got.CasHash != regResp.CasHash {
		t.Errorf("CasHash: got %q want %q", got.CasHash, regResp.CasHash)
	}
	if got.ToolDefinitionId != req.ToolDefinitionId {
		t.Errorf("ToolDefinitionId: got %q want %q", got.ToolDefinitionId, req.ToolDefinitionId)
	}
	if got.ToolName != req.ToolName {
		t.Errorf("ToolName: got %q want %q", got.ToolName, req.ToolName)
	}
	if got.Version != req.Version {
		t.Errorf("Version: got %q want %q", got.Version, req.Version)
	}
	wantStableRef := req.ToolName + "@" + req.Version
	if got.StableRef != wantStableRef {
		t.Errorf("StableRef: got %q want %q", got.StableRef, wantStableRef)
	}
	if got.ImageUri != req.ImageUri {
		t.Errorf("ImageUri: got %q want %q", got.ImageUri, req.ImageUri)
	}
	if got.Digest != req.Digest {
		t.Errorf("Digest: got %q want %q", got.Digest, req.Digest)
	}
	if got.EnvironmentSpec != req.EnvironmentSpec {
		t.Errorf("EnvironmentSpec mismatch")
	}
	if got.Command != req.Command {
		t.Errorf("Command: got %q want %q", got.Command, req.Command)
	}
	if got.LifecyclePhase != "Active" {
		t.Errorf("LifecyclePhase: got %q want Active", got.LifecyclePhase)
	}
	if got.IntegrityHealth != "Healthy" {
		t.Errorf("IntegrityHealth: got %q want Healthy", got.IntegrityHealth)
	}
	if got.RegisteredAt == 0 {
		t.Error("RegisteredAt should be non-zero")
	}
	if got.Validation == nil || got.Validation.Phase != "Passed" {
		t.Errorf("Validation.Phase: got %v want Passed", got.Validation)
	}
	if len(got.Inputs) != 1 || got.Inputs[0].Name != "reads" {
		t.Errorf("Inputs mismatch")
	}
	if len(got.Outputs) != 1 || got.Outputs[0].Name != "aligned" {
		t.Errorf("Outputs mismatch")
	}
	if got.Outputs[0].Constraints["sorted"] != "coordinate" {
		t.Errorf("Outputs[0].Constraints[sorted] mismatch")
	}
	if got.Display == nil || got.Display.Label != "BWA-MEM2 2.2.1" {
		t.Errorf("Display.Label mismatch")
	}
	if len(got.Display.Tags) != 2 {
		t.Errorf("Display.Tags: got %d want 2", len(got.Display.Tags))
	}
}

// TestRegisterTool_SingleFilePerRegistration verifies SaveWithCasHash writes
// exactly one .tooldefinition file (no ghost files from double-save).
func TestRegisterTool_SingleFilePerRegistration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CATALOG_DIR", dir)
	cat := catalog.NewCatalog()
	svc := catalog.NewToolRegistryService(cat)

	_, err := svc.RegisterTool(t.Context(), &nfv1.RegisterToolRequest{
		ToolName: "bowtie2",
		Digest:   "sha256:abc",
		Version:  "2.5.0",
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	if len(files) != 1 {
		t.Errorf("expected exactly 1 .tooldefinition file, got %d: %v", len(files), files)
	}
}

// TestListTools_StableRefFilter verifies that ListTools(stable_ref=X) returns
// only tools matching X and ignores others.
func TestListTools_StableRefFilter(t *testing.T) {
	cat := newTestCatalog(t)
	svc := catalog.NewToolRegistryService(cat)

	// Register two tools: bwa@1.0 and bowtie2@2.0
	for _, tc := range []struct{ name, version string }{
		{"bwa", "1.0"},
		{"bowtie2", "2.0"},
	} {
		if _, err := svc.RegisterTool(t.Context(), &nfv1.RegisterToolRequest{
			ToolName: tc.name,
			Version:  tc.version,
			Digest:   "sha256:000",
		}); err != nil {
			t.Fatalf("RegisterTool %s: %v", tc.name, err)
		}
	}

	resp, err := svc.ListTools(t.Context(), &nfv1.ListToolsRequest{StableRef: "bwa@1.0"})
	if err != nil {
		t.Fatalf("ListTools with filter: %v", err)
	}
	if len(resp.Tools) != 1 {
		t.Fatalf("expected 1 tool for stable_ref=bwa@1.0, got %d", len(resp.Tools))
	}
	if resp.Tools[0].ToolName != "bwa" {
		t.Errorf("expected bwa, got %q", resp.Tools[0].ToolName)
	}

	// Empty filter returns all
	allResp, err := svc.ListTools(t.Context(), &nfv1.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools all: %v", err)
	}
	if len(allResp.Tools) != 2 {
		t.Errorf("expected 2 tools total, got %d", len(allResp.Tools))
	}
}
