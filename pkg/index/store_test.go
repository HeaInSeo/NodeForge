package index_test

import (
	"errors"
	"testing"

	"github.com/HeaInSeo/NodeForge/pkg/index"
)

func newStore(t *testing.T) *index.Store {
	t.Helper()
	s, err := index.NewAt(t.TempDir())
	if err != nil {
		t.Fatalf("NewAt: %v", err)
	}
	return s
}

func toolEntry(casHash, stableRef string) index.Entry {
	return index.Entry{
		CasHash:         casHash,
		ArtifactKind:    index.KindTool,
		StableRef:       stableRef,
		ToolName:        "bwa-mem2",
		Version:         "2.2.1",
		ImageDigest:     "sha256:aaaa",
		LifecyclePhase:  index.PhaseActive,
		IntegrityHealth: index.HealthHealthy,
	}
}

// ── Append ────────────────────────────────────────────────────────────────────

func TestAppend_Success(t *testing.T) {
	s := newStore(t)
	e := toolEntry("hash-001", "bwa-mem2@2.2.1")
	if err := s.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	all, _ := s.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(all))
	}
}

func TestAppend_EmptyCasHash_Rejected(t *testing.T) {
	s := newStore(t)
	err := s.Append(index.Entry{StableRef: "bwa@1"})
	if err == nil {
		t.Fatal("expected error for empty CasHash")
	}
}

func TestAppend_DuplicateCasHash_Rejected(t *testing.T) {
	s := newStore(t)
	e := toolEntry("hash-dup", "bwa@1")
	if err := s.Append(e); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if err := s.Append(e); err == nil {
		t.Fatal("expected error for duplicate CasHash")
	}
}

// ── GetByCasHash ──────────────────────────────────────────────────────────────

func TestGetByCasHash_Found(t *testing.T) {
	s := newStore(t)
	e := toolEntry("hash-get", "bwa@1")
	if err := s.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := s.GetByCasHash("hash-get")
	if err != nil {
		t.Fatalf("GetByCasHash: %v", err)
	}
	if got.CasHash != "hash-get" {
		t.Errorf("CasHash: got %q want hash-get", got.CasHash)
	}
	if got.StableRef != "bwa@1" {
		t.Errorf("StableRef: got %q want bwa@1", got.StableRef)
	}
}

func TestGetByCasHash_NotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.GetByCasHash("nonexistent")
	if !errors.Is(err, index.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ── ListByStableRef ───────────────────────────────────────────────────────────

func TestListByStableRef_MultipleRevisions(t *testing.T) {
	s := newStore(t)
	// Two different casHashes share the same stableRef (1:N cardinality).
	for _, h := range []string{"hash-r1", "hash-r2"} {
		if err := s.Append(toolEntry(h, "bwa-mem2@2.2.1")); err != nil {
			t.Fatalf("Append %s: %v", h, err)
		}
	}
	// A third entry with a different stableRef.
	if err := s.Append(toolEntry("hash-other", "bowtie2@2.5.0")); err != nil {
		t.Fatalf("Append other: %v", err)
	}

	got, err := s.ListByStableRef("bwa-mem2@2.2.1")
	if err != nil {
		t.Fatalf("ListByStableRef: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 entries for bwa-mem2@2.2.1, got %d", len(got))
	}
}

func TestListByStableRef_NoMatch_EmptySlice(t *testing.T) {
	s := newStore(t)
	got, err := s.ListByStableRef("nonexistent@1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d entries", len(got))
	}
}

// ── ListActive ────────────────────────────────────────────────────────────────

func TestListActive_LifecyclePhaseOnly(t *testing.T) {
	s := newStore(t)

	// Active + Healthy — should appear
	e1 := toolEntry("hash-a1", "bwa@1")
	e1.LifecyclePhase = index.PhaseActive
	e1.IntegrityHealth = index.HealthHealthy
	_ = s.Append(e1)

	// Active + Partial — should still appear (integrity_health irrelevant for Catalog)
	e2 := toolEntry("hash-a2", "bwa@2")
	e2.LifecyclePhase = index.PhaseActive
	e2.IntegrityHealth = index.HealthPartial
	_ = s.Append(e2)

	// Active + Missing — should still appear
	e3 := toolEntry("hash-a3", "bwa@3")
	e3.LifecyclePhase = index.PhaseActive
	e3.IntegrityHealth = index.HealthMissing
	_ = s.Append(e3)

	// Retracted — must NOT appear
	e4 := toolEntry("hash-r1", "bwa@4")
	e4.LifecyclePhase = index.PhaseRetracted
	_ = s.Append(e4)

	// Deleted — must NOT appear
	e5 := toolEntry("hash-d1", "bwa@5")
	e5.LifecyclePhase = index.PhaseDeleted
	_ = s.Append(e5)

	active, err := s.ListActive()
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 3 {
		t.Errorf("expected 3 Active entries, got %d", len(active))
	}
	for _, e := range active {
		if e.LifecyclePhase != index.PhaseActive {
			t.Errorf("non-Active entry in ListActive result: %q phase=%q", e.CasHash, e.LifecyclePhase)
		}
	}
}

// ── SetLifecyclePhase ─────────────────────────────────────────────────────────

func TestSetLifecyclePhase_Transition(t *testing.T) {
	s := newStore(t)
	e := toolEntry("hash-lc", "bwa@1")
	e.LifecyclePhase = index.PhasePending
	if err := s.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if err := s.SetLifecyclePhase("hash-lc", index.PhaseActive); err != nil {
		t.Fatalf("SetLifecyclePhase: %v", err)
	}
	got, _ := s.GetByCasHash("hash-lc")
	if got.LifecyclePhase != index.PhaseActive {
		t.Errorf("LifecyclePhase: got %q want Active", got.LifecyclePhase)
	}
	if got.LifecycleUpdatedAt.IsZero() {
		t.Error("LifecycleUpdatedAt should be set")
	}
	// IntegrityHealth must be untouched by lifecycle transition
	if got.IntegrityHealth != index.HealthHealthy {
		t.Errorf("IntegrityHealth should be unchanged: got %q", got.IntegrityHealth)
	}
}

func TestSetLifecyclePhase_NotFound(t *testing.T) {
	s := newStore(t)
	err := s.SetLifecyclePhase("nonexistent", index.PhaseActive)
	if !errors.Is(err, index.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ── SetIntegrityHealth ────────────────────────────────────────────────────────

func TestSetIntegrityHealth_Transition(t *testing.T) {
	s := newStore(t)
	e := toolEntry("hash-ih", "bwa@1")
	e.IntegrityHealth = index.HealthHealthy
	if err := s.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if err := s.SetIntegrityHealth("hash-ih", index.HealthPartial); err != nil {
		t.Fatalf("SetIntegrityHealth: %v", err)
	}
	got, _ := s.GetByCasHash("hash-ih")
	if got.IntegrityHealth != index.HealthPartial {
		t.Errorf("IntegrityHealth: got %q want Partial", got.IntegrityHealth)
	}
	if got.HealthCheckedAt.IsZero() {
		t.Error("HealthCheckedAt should be set")
	}
	// LifecyclePhase must be untouched by health transition
	if got.LifecyclePhase != index.PhaseActive {
		t.Errorf("LifecyclePhase should be unchanged: got %q", got.LifecyclePhase)
	}
}

func TestSetIntegrityHealth_NotFound(t *testing.T) {
	s := newStore(t)
	err := s.SetIntegrityHealth("nonexistent", index.HealthMissing)
	if !errors.Is(err, index.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ── Two-axis independence ─────────────────────────────────────────────────────

// TestTwoAxesAreIndependent verifies that SetLifecyclePhase and
// SetIntegrityHealth each change only their own axis, never the other.
func TestTwoAxesAreIndependent(t *testing.T) {
	s := newStore(t)
	e := toolEntry("hash-2ax", "bwa@1")
	e.LifecyclePhase = index.PhasePending
	e.IntegrityHealth = index.HealthHealthy
	if err := s.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Transition lifecycle: Pending → Active; health must stay Healthy
	if err := s.SetLifecyclePhase("hash-2ax", index.PhaseActive); err != nil {
		t.Fatalf("SetLifecyclePhase: %v", err)
	}
	got, _ := s.GetByCasHash("hash-2ax")
	if got.IntegrityHealth != index.HealthHealthy {
		t.Errorf("after lifecycle transition, IntegrityHealth changed: %q", got.IntegrityHealth)
	}

	// Transition health: Healthy → Missing; lifecycle must stay Active
	if err := s.SetIntegrityHealth("hash-2ax", index.HealthMissing); err != nil {
		t.Fatalf("SetIntegrityHealth: %v", err)
	}
	got, _ = s.GetByCasHash("hash-2ax")
	if got.LifecyclePhase != index.PhaseActive {
		t.Errorf("after health transition, LifecyclePhase changed: %q", got.LifecyclePhase)
	}
}

// ── Persistence ───────────────────────────────────────────────────────────────

func TestPersistence_ReloadFromDisk(t *testing.T) {
	dir := t.TempDir()

	s1, _ := index.NewAt(dir)
	e := toolEntry("hash-persist", "bwa@1")
	if err := s1.Append(e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Open a second Store pointing at the same dir — simulates restart.
	s2, err := index.NewAt(dir)
	if err != nil {
		t.Fatalf("NewAt (reload): %v", err)
	}
	got, err := s2.GetByCasHash("hash-persist")
	if err != nil {
		t.Fatalf("GetByCasHash after reload: %v", err)
	}
	if got.StableRef != "bwa@1" {
		t.Errorf("StableRef after reload: got %q want bwa@1", got.StableRef)
	}
}

// ── Data artifact support (P3 reservation) ───────────────────────────────────

func TestAppend_DataKind_AcceptedBySchema(t *testing.T) {
	s := newStore(t)
	e := index.Entry{
		CasHash:         "hash-data",
		ArtifactKind:    index.KindData,
		StableRef:       "grch38-genome@2024",
		LifecyclePhase:  index.PhaseActive,
		IntegrityHealth: index.HealthHealthy,
	}
	if err := s.Append(e); err != nil {
		t.Fatalf("Append data artifact: %v", err)
	}
	got, err := s.GetByCasHash("hash-data")
	if err != nil {
		t.Fatalf("GetByCasHash: %v", err)
	}
	if got.ArtifactKind != index.KindData {
		t.Errorf("ArtifactKind: got %q want data", got.ArtifactKind)
	}
}
