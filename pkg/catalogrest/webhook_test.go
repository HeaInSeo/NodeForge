package catalogrest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HeaInSeo/NodeForge/pkg/catalog"
	"github.com/HeaInSeo/NodeForge/pkg/catalogrest"
	"github.com/HeaInSeo/NodeForge/pkg/index"
)

// fakeReconciler records which casHashes were triggered.
type fakeReconciler struct {
	triggered []string
}

func (f *fakeReconciler) ReconcileOne(_ context.Context, casHash string) error {
	f.triggered = append(f.triggered, casHash)
	return nil
}

func newWebhookServer(t *testing.T) (*httptest.Server, *index.Store, *fakeReconciler) {
	t.Helper()
	store, err := index.NewAt(t.TempDir())
	if err != nil {
		t.Fatalf("index.NewAt: %v", err)
	}
	cat := catalog.NewCatalogAt(t.TempDir())
	dataCat := catalog.NewDataCatalogAt(t.TempDir())
	rec := &fakeReconciler{}

	mux := catalogrest.NewMux(store, cat, dataCat)
	catalogrest.RegisterWebhook(mux, store, rec)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, store, rec
}

func appendTestEntry(t *testing.T, store *index.Store, casHash, imageDigest string) {
	t.Helper()
	err := store.Append(index.Entry{
		CasHash:         casHash,
		ArtifactKind:    index.KindTool,
		StableRef:       casHash + "@1.0",
		ImageDigest:     imageDigest,
		LifecyclePhase:  index.PhaseActive,
		IntegrityHealth: index.HealthHealthy,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func postHarborEvent(t *testing.T, ts *httptest.Server, digest string) *http.Response {
	t.Helper()
	payload := map[string]any{
		"type": "PUSH_ARTIFACT",
		"event_data": map[string]any{
			"resources": []map[string]any{
				{"digest": digest},
			},
		},
	}
	body, _ := json.Marshal(payload)
	resp, err := ts.Client().Post(ts.URL+"/v1/webhooks/harbor", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST webhook: %v", err)
	}
	return resp
}

func TestWebhook_MatchingDigest_TriggersReconcile(t *testing.T) {
	ts, store, rec := newWebhookServer(t)
	appendTestEntry(t, store, "casA", "sha256:aaa")

	resp := postHarborEvent(t, ts, "sha256:aaa")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	if len(rec.triggered) != 1 || rec.triggered[0] != "casA" {
		t.Errorf("triggered: %v, want [casA]", rec.triggered)
	}
}

func TestWebhook_NoMatchingDigest_ZeroTriggers(t *testing.T) {
	ts, store, rec := newWebhookServer(t)
	appendTestEntry(t, store, "casA", "sha256:aaa")

	resp := postHarborEvent(t, ts, "sha256:zzz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	if len(rec.triggered) != 0 {
		t.Errorf("expected no triggers, got %v", rec.triggered)
	}
}

func TestWebhook_MultipleArtifactsSameDigest_AllTriggered(t *testing.T) {
	ts, store, rec := newWebhookServer(t)
	// Two artifacts with the same image digest (different CAS hashes).
	appendTestEntry(t, store, "casA", "sha256:aaa")
	appendTestEntry(t, store, "casB", "sha256:aaa")

	resp := postHarborEvent(t, ts, "sha256:aaa")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	if len(rec.triggered) != 2 {
		t.Errorf("expected 2 triggers, got %d: %v", len(rec.triggered), rec.triggered)
	}
}

func TestWebhook_InvalidJSON_BadRequest(t *testing.T) {
	ts, _, _ := newWebhookServer(t)

	resp, err := ts.Client().Post(ts.URL+"/v1/webhooks/harbor", "application/json",
		bytes.NewReader([]byte("not-json")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", resp.StatusCode)
	}
}

func TestWebhook_EmptyResources_NoContent(t *testing.T) {
	ts, _, rec := newWebhookServer(t)

	payload := map[string]any{
		"type":       "PUSH_ARTIFACT",
		"event_data": map[string]any{"resources": []any{}},
	}
	body, _ := json.Marshal(payload)
	resp, err := ts.Client().Post(ts.URL+"/v1/webhooks/harbor", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status: got %d want 204", resp.StatusCode)
	}
	if len(rec.triggered) != 0 {
		t.Errorf("expected no triggers, got %v", rec.triggered)
	}
}
