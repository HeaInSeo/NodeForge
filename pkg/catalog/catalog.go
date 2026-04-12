// Package catalog manages RegisteredToolDefinition CAS storage and ToolRegistryService.
// Files are stored as {sha256-hash}.tooldefinition for content-addressable lookup.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"
)

const defaultCatalogDir = "assets/catalog"

// Catalog stores RegisteredToolDefinition objects as content-addressed files.
type Catalog struct {
	dir string
}

// NewCatalog creates a Catalog. CATALOG_DIR env overrides the default directory.
func NewCatalog() *Catalog {
	dir := os.Getenv("CATALOG_DIR")
	if dir == "" {
		dir = defaultCatalogDir
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "catalog: mkdir %s: %v\n", dir, err)
	}
	return &Catalog{dir: dir}
}

// Save marshals tool to JSON, computes SHA256, and writes {hash}.tooldefinition.
// Returns the hex-encoded hash used as the CAS key.
func (c *Catalog) Save(tool *nfv1.RegisteredToolDefinition) (string, error) {
	data, err := json.Marshal(tool)
	if err != nil {
		return "", fmt.Errorf("marshal tool: %w", err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	path := filepath.Join(c.dir, hash+".tooldefinition")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return hash, nil
}

// List reads all *.tooldefinition files and returns the parsed tools.
func (c *Catalog) List() ([]*nfv1.RegisteredToolDefinition, error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return nil, fmt.Errorf("read catalog dir: %w", err)
	}
	tools := make([]*nfv1.RegisteredToolDefinition, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tooldefinition") {
			continue
		}
		path := filepath.Join(c.dir, e.Name())
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			continue
		}
		var t nfv1.RegisteredToolDefinition
		if jerr := json.Unmarshal(data, &t); jerr != nil {
			continue
		}
		tools = append(tools, &t)
	}
	return tools, nil
}

// ListActive returns only tools with phase == "Active".
// UI 팔레트가 사용자에게 표시할 툴 목록 조회에 사용된다.
func (c *Catalog) ListActive() ([]*nfv1.RegisteredToolDefinition, error) {
	all, err := c.List()
	if err != nil {
		return nil, err
	}
	out := make([]*nfv1.RegisteredToolDefinition, 0, len(all))
	for _, t := range all {
		if t.Phase == "Active" {
			out = append(out, t)
		}
	}
	return out, nil
}

// ListByStableRef returns all tools matching the given stable_ref.
// UI 검색·카탈로그 탐색 전용. 파이프라인 pin에는 casHash를 사용한다.
func (c *Catalog) ListByStableRef(stableRef string) ([]*nfv1.RegisteredToolDefinition, error) {
	all, err := c.List()
	if err != nil {
		return nil, err
	}
	out := make([]*nfv1.RegisteredToolDefinition, 0)
	for _, t := range all {
		if t.StableRef == stableRef {
			out = append(out, t)
		}
	}
	return out, nil
}

// ToolRegistryService implements ToolRegistryServiceServer.
type ToolRegistryService struct {
	nfv1.UnimplementedToolRegistryServiceServer
	catalog *Catalog
}

// NewToolRegistryService creates a ToolRegistryService backed by the given Catalog.
func NewToolRegistryService(cat *Catalog) *ToolRegistryService {
	return &ToolRegistryService{catalog: cat}
}

// RegisterTool creates a RegisteredToolDefinition and saves it to the catalog.
func (s *ToolRegistryService) RegisterTool(
	_ context.Context, req *nfv1.RegisterToolRequest,
) (*nfv1.RegisterToolResponse, error) {
	stableRef := req.StableRef
	if stableRef == "" && req.ToolName != "" {
		// NodeForge가 tool_name@version 형태로 조립한다.
		if req.Version != "" {
			stableRef = req.ToolName + "@" + req.Version
		} else {
			stableRef = req.ToolName
		}
	}

	tool := &nfv1.RegisteredToolDefinition{
		ToolDefinitionId: req.ToolDefinitionId,
		ToolName:         req.ToolName,
		ImageUri:         req.ImageUri,
		Digest:           req.Digest,
		EnvironmentSpec:  req.EnvironmentSpec,
		RegisteredAt:     time.Now().Unix(),
		Version:          req.Version,
		StableRef:        stableRef,
		Inputs:           req.Inputs,
		Outputs:          req.Outputs,
		Display:          req.Display,
		Command:          req.Command,
		Phase:            "Active",
		Validation: &nfv1.ValidationStatus{
			Phase:           "Passed",
			LastValidatedAt: time.Now().Unix(),
		},
	}
	hash, err := s.catalog.Save(tool)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "catalog save: %v", err)
	}
	tool.CasHash = hash
	// Re-save with CasHash populated.
	if _, err = s.catalog.Save(tool); err != nil {
		return nil, status.Errorf(codes.Internal, "catalog re-save with cas_hash: %v", err)
	}
	return &nfv1.RegisterToolResponse{CasHash: hash, Tool: tool}, nil
}

// ListTools returns all registered tools from the catalog.
func (s *ToolRegistryService) ListTools(
	_ context.Context, _ *nfv1.ListToolsRequest,
) (*nfv1.ListToolsResponse, error) {
	tools, err := s.catalog.List()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "catalog list: %v", err)
	}
	return &nfv1.ListToolsResponse{Tools: tools}, nil
}

// GetTool retrieves a single RegisteredToolDefinition by its CAS hash.
func (s *ToolRegistryService) GetTool(
	_ context.Context, req *nfv1.GetToolRequest,
) (*nfv1.RegisteredToolDefinition, error) {
	tools, err := s.catalog.List()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "catalog list: %v", err)
	}
	for _, t := range tools {
		if t.CasHash == req.CasHash {
			return t, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "tool %s not found", req.CasHash)
}
