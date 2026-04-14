package oras_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	orasoras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"

	"github.com/HeaInSeo/NodeForge/pkg/oras"
)

// newLocalStore creates a temporary OCI layout store for tests.
func newTestStore(t *testing.T) orasoras.Target {
	t.Helper()
	s, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}
	return s
}

// pushFakeSubject pushes a minimal OCI image manifest into target and returns its digest.
func pushFakeSubject(t *testing.T, ctx context.Context, target orasoras.Target) string {
	t.Helper()

	configData := []byte(`{}`)
	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    godigest.FromBytes(configData),
		Size:      int64(len(configData)),
	}
	if err := target.Push(ctx, configDesc, strings.NewReader(string(configData))); err != nil {
		t.Fatalf("push config: %v", err)
	}

	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    []ocispec.Descriptor{},
	}
	manifestData, _ := json.Marshal(manifest)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    godigest.FromBytes(manifestData),
		Size:      int64(len(manifestData)),
	}
	if err := target.Push(ctx, manifestDesc, strings.NewReader(string(manifestData))); err != nil {
		t.Fatalf("push manifest: %v", err)
	}
	return manifestDesc.Digest.String()
}

// ── PushToolSpecReferrer ──────────────────────────────────────────────────────

func TestPushToolSpecReferrer_Success(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	subjectDigest := pushFakeSubject(t, ctx, store)

	specJSON, err := oras.MarshalSpec(map[string]string{"tool": "bwa-mem2", "version": "2.2.1"})
	if err != nil {
		t.Fatalf("MarshalSpec: %v", err)
	}

	result, err := oras.PushToolSpecReferrer(ctx, store, subjectDigest, specJSON)
	if err != nil {
		t.Fatalf("PushToolSpecReferrer: %v", err)
	}

	if result.SubjectDigest != subjectDigest {
		t.Errorf("SubjectDigest: got %q want %q", result.SubjectDigest, subjectDigest)
	}
	if result.MediaType != oras.MediaTypeToolSpec {
		t.Errorf("MediaType: got %q want %q", result.MediaType, oras.MediaTypeToolSpec)
	}
	if result.ReferrerDigest == "" {
		t.Error("ReferrerDigest must not be empty")
	}
	if result.ReferrerDigest == subjectDigest {
		t.Error("ReferrerDigest should not equal SubjectDigest")
	}
}

func TestPushDataSpecReferrer_Success(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	subjectDigest := pushFakeSubject(t, ctx, store)

	specJSON, _ := oras.MarshalSpec(map[string]string{"dataset": "grch38", "version": "2024"})

	result, err := oras.PushDataSpecReferrer(ctx, store, subjectDigest, specJSON)
	if err != nil {
		t.Fatalf("PushDataSpecReferrer: %v", err)
	}
	if result.MediaType != oras.MediaTypeDataSpec {
		t.Errorf("MediaType: got %q want %q", result.MediaType, oras.MediaTypeDataSpec)
	}
}

func TestPushToolSpecReferrer_EmptySubjectDigest(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	specJSON, _ := oras.MarshalSpec(map[string]string{"k": "v"})
	_, err := oras.PushToolSpecReferrer(ctx, store, "", specJSON)
	if err == nil {
		t.Fatal("expected error for empty subjectDigest")
	}
}

func TestPushToolSpecReferrer_EmptySpecJSON(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	_, err := oras.PushToolSpecReferrer(ctx, store, "sha256:aaaa", nil)
	if err == nil {
		t.Fatal("expected error for empty specJSON")
	}
}

// ── ToolSpec != DataSpec mediaType ────────────────────────────────────────────

func TestPushToolVsDataReferrer_DifferentMediaType(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	subjectDigest := pushFakeSubject(t, ctx, store)

	specJSON, _ := oras.MarshalSpec(map[string]string{"k": "v"})

	toolResult, err := oras.PushToolSpecReferrer(ctx, store, subjectDigest, specJSON)
	if err != nil {
		t.Fatalf("PushToolSpecReferrer: %v", err)
	}

	store2 := newTestStore(t)
	subjectDigest2 := pushFakeSubject(t, ctx, store2)
	dataResult, err := oras.PushDataSpecReferrer(ctx, store2, subjectDigest2, specJSON)
	if err != nil {
		t.Fatalf("PushDataSpecReferrer: %v", err)
	}

	if toolResult.MediaType == dataResult.MediaType {
		t.Errorf("tool and data referrer must have different MediaType, both got %q", toolResult.MediaType)
	}
}

// ── MarshalSpec ───────────────────────────────────────────────────────────────

func TestMarshalSpec_RoundTrip(t *testing.T) {
	type Spec struct {
		Tool    string `json:"tool"`
		Version string `json:"version"`
	}
	orig := Spec{Tool: "bwa-mem2", Version: "2.2.1"}
	data, err := oras.MarshalSpec(orig)
	if err != nil {
		t.Fatalf("MarshalSpec: %v", err)
	}
	var got Spec
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != orig {
		t.Errorf("round-trip: got %+v want %+v", got, orig)
	}
}

// ── MediaType constants ───────────────────────────────────────────────────────

func TestMediaTypeConstants(t *testing.T) {
	if oras.MediaTypeToolSpec == "" {
		t.Error("MediaTypeToolSpec must not be empty")
	}
	if oras.MediaTypeDataSpec == "" {
		t.Error("MediaTypeDataSpec must not be empty")
	}
	if oras.MediaTypeToolSpec == oras.MediaTypeDataSpec {
		t.Error("tool and data spec media types must differ")
	}
}

// ── NewLocalStore ─────────────────────────────────────────────────────────────

func TestNewLocalStore_CreatesStore(t *testing.T) {
	store, err := oras.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestNewLocalStore_PushReferrer(t *testing.T) {
	ctx := context.Background()
	store, err := oras.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	subjectDigest := pushFakeSubject(t, ctx, store)
	specJSON, _ := oras.MarshalSpec(map[string]string{"tool": "bwa"})

	result, err := oras.PushToolSpecReferrer(ctx, store, subjectDigest, specJSON)
	if err != nil {
		t.Fatalf("PushToolSpecReferrer via local store: %v", err)
	}
	if result.ReferrerDigest == "" {
		t.Error("ReferrerDigest must not be empty")
	}
}
