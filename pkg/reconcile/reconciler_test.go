package reconcile_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HeaInSeo/NodeVault/pkg/index"
	"github.com/HeaInSeo/NodeVault/pkg/reconcile"
	"github.com/HeaInSeo/NodeVault/pkg/registry"
	"github.com/HeaInSeo/NodeVault/pkg/registryconfig"
)

// fakeChecker implements RegistryChecker for tests.
type fakeChecker struct {
	imageExists    bool
	referrerExists bool
	pullReachable  bool
	imageErr       error
	referrerErr    error
	pullErr        error
}

func (f *fakeChecker) ImageExists(_ context.Context, _, _ string) (bool, error) {
	return f.imageExists, f.imageErr
}
func (f *fakeChecker) SpecReferrerWitness(_ context.Context, _, _, _, _ string) (bool, error) {
	return f.referrerExists, f.referrerErr
}
func (f *fakeChecker) PullReachable(_ context.Context, _, _ string) (bool, error) {
	return f.pullReachable, f.pullErr
}

func newTestStore(t *testing.T) *index.Store {
	t.Helper()
	store, err := index.NewAt(t.TempDir())
	if err != nil {
		t.Fatalf("index.NewAt: %v", err)
	}
	return store
}

func appendEntry(t *testing.T, store *index.Store, casHash, stableRef, imageDigest string) {
	t.Helper()
	err := store.Append(index.Entry{
		CasHash:         casHash,
		ArtifactKind:    index.KindTool,
		StableRef:       stableRef,
		ImageDigest:     imageDigest,
		LifecyclePhase:  index.PhaseActive,
		IntegrityHealth: index.HealthHealthy,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// ── judgeHealth via FastRun ───────────────────────────────────────────────────

func TestFastRun_ImageAndReferrer_Healthy(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")

	r := reconcile.New(store, &fakeChecker{imageExists: true, referrerExists: true})
	if err := r.FastRun(t.Context()); err != nil {
		t.Fatalf("FastRun: %v", err)
	}

	e, _ := store.GetByCasHash("h1")
	if e.IntegrityHealth != index.HealthHealthy {
		t.Errorf("want Healthy, got %q", e.IntegrityHealth)
	}
}

func TestFastRun_ImageOK_ReferrerMissing_Partial(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")

	r := reconcile.New(store, &fakeChecker{imageExists: true, referrerExists: false})
	if err := r.FastRun(t.Context()); err != nil {
		t.Fatalf("FastRun: %v", err)
	}

	e, _ := store.GetByCasHash("h1")
	if e.IntegrityHealth != index.HealthPartial {
		t.Errorf("want Partial, got %q", e.IntegrityHealth)
	}
}

func TestFastRun_BothMissing_Missing(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")

	r := reconcile.New(store, &fakeChecker{imageExists: false, referrerExists: false})
	if err := r.FastRun(t.Context()); err != nil {
		t.Fatalf("FastRun: %v", err)
	}

	e, _ := store.GetByCasHash("h1")
	if e.IntegrityHealth != index.HealthMissing {
		t.Errorf("want Missing, got %q", e.IntegrityHealth)
	}
}

func TestFastRun_ImageMissing_ReferrerPresent_Orphaned(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")

	r := reconcile.New(store, &fakeChecker{imageExists: false, referrerExists: true})
	if err := r.FastRun(t.Context()); err != nil {
		t.Fatalf("FastRun: %v", err)
	}

	e, _ := store.GetByCasHash("h1")
	if e.IntegrityHealth != index.HealthOrphaned {
		t.Errorf("want Orphaned, got %q", e.IntegrityHealth)
	}
}

// ── SlowRun ───────────────────────────────────────────────────────────────────

func TestSlowRun_PullFail_Unreachable(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")
	// Entry starts Healthy (appendEntry sets it).

	r := reconcile.New(store, &fakeChecker{
		imageExists: true, referrerExists: true, pullReachable: false,
	})
	if err := r.SlowRun(t.Context()); err != nil {
		t.Fatalf("SlowRun: %v", err)
	}

	e, _ := store.GetByCasHash("h1")
	if e.IntegrityHealth != index.HealthUnreachable {
		t.Errorf("want Unreachable, got %q", e.IntegrityHealth)
	}
}

func TestSlowRun_PullOK_HealthUnchanged(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")

	r := reconcile.New(store, &fakeChecker{
		imageExists: true, referrerExists: true, pullReachable: true,
	})
	if err := r.SlowRun(t.Context()); err != nil {
		t.Fatalf("SlowRun: %v", err)
	}

	e, _ := store.GetByCasHash("h1")
	if e.IntegrityHealth != index.HealthHealthy {
		t.Errorf("want Healthy, got %q", e.IntegrityHealth)
	}
}

// raceChecker simulates a FastRun pass landing a fresher integrity_health write
// while SlowRun's slow PullReachable check for the same entry is still in flight —
// reproducing the stale-snapshot race deterministically (no timing dependency).
type raceChecker struct {
	store            *index.Store
	casHash          string
	concurrentHealth index.IntegrityHealth // health FastRun "lands" mid-pull
	pullReachable    bool
}

func (*raceChecker) ImageExists(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}
func (*raceChecker) SpecReferrerWitness(_ context.Context, _, _, _, _ string) (bool, error) {
	return true, nil
}
func (c *raceChecker) PullReachable(_ context.Context, _, _ string) (bool, error) {
	// Simulate a concurrent FastRun tick completing while this slow pull check is
	// still in flight, transitioning the entry away from the Healthy snapshot
	// SlowRun read before starting this call.
	if err := c.store.SetIntegrityHealth(c.casHash, c.concurrentHealth); err != nil {
		return false, err
	}
	return c.pullReachable, nil
}

func TestSlowRun_ConcurrentFastRunChange_NotClobbered(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")
	// Entry starts Healthy (appendEntry sets it) — SlowRun will snapshot this value.

	r := reconcile.New(store, &raceChecker{
		store:            store,
		casHash:          "h1",
		concurrentHealth: index.HealthPartial, // fresher verdict landed by "FastRun"
		pullReachable:    false,               // slow pull check fails
	})
	if err := r.SlowRun(t.Context()); err != nil {
		t.Fatalf("SlowRun: %v", err)
	}

	// The fresher Partial verdict (landed mid-flight) must win — SlowRun's stale
	// Unreachable write, based on the pre-interleaving Healthy snapshot, must be
	// discarded rather than clobbering it.
	e, _ := store.GetByCasHash("h1")
	if e.IntegrityHealth != index.HealthPartial {
		t.Errorf("want Partial (fresher FastRun verdict preserved), got %q", e.IntegrityHealth)
	}
}

func TestSlowRun_SkipsNonHealthy(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")
	// Manually set to Partial (non-Healthy) before slow run.
	if err := store.SetIntegrityHealth("h1", index.HealthPartial); err != nil {
		t.Fatalf("SetIntegrityHealth: %v", err)
	}

	// pullReachable=false — but slow run must skip Partial entries.
	r := reconcile.New(store, &fakeChecker{pullReachable: false})
	if err := r.SlowRun(t.Context()); err != nil {
		t.Fatalf("SlowRun: %v", err)
	}

	// Health must remain Partial (slow loop skipped it).
	e, _ := store.GetByCasHash("h1")
	if e.IntegrityHealth != index.HealthPartial {
		t.Errorf("SlowRun must not change non-Healthy entries; got %q", e.IntegrityHealth)
	}
}

// ── ReconcileOne ─────────────────────────────────────────────────────────────

func TestReconcileOne_TargetedUpdate(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")
	appendEntry(t, store, "h2", "samtools@1.17", "sha256:bbb")

	// Only h1 will trigger reconcile; checker sees image missing.
	r := reconcile.New(store, &fakeChecker{imageExists: false, referrerExists: false})
	if err := r.ReconcileOne(t.Context(), "h1"); err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}

	e1, _ := store.GetByCasHash("h1")
	e2, _ := store.GetByCasHash("h2")

	if e1.IntegrityHealth != index.HealthMissing {
		t.Errorf("h1: want Missing, got %q", e1.IntegrityHealth)
	}
	// h2 must be untouched.
	if e2.IntegrityHealth != index.HealthHealthy {
		t.Errorf("h2 must remain Healthy (not targeted), got %q", e2.IntegrityHealth)
	}
}

// ── Axis independence: reconcile NEVER changes lifecycle_phase ────────────────

func TestFastRun_NeverChangesLifecyclePhase(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")
	// Manually retract to simulate operator intent.
	if err := store.SetLifecyclePhase("h1", index.PhaseRetracted); err != nil {
		t.Fatalf("SetLifecyclePhase: %v", err)
	}

	r := reconcile.New(store, &fakeChecker{imageExists: false, referrerExists: false})
	if err := r.FastRun(t.Context()); err != nil {
		t.Fatalf("FastRun: %v", err)
	}

	e, _ := store.GetByCasHash("h1")
	if e.LifecyclePhase != index.PhaseRetracted {
		t.Errorf("FastRun must NOT change lifecycle_phase; got %q", e.LifecyclePhase)
	}
}

func TestSlowRun_NeverChangesLifecyclePhase(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")
	if err := store.SetLifecyclePhase("h1", index.PhaseRetracted); err != nil {
		t.Fatalf("SetLifecyclePhase: %v", err)
	}
	// Reset to Healthy so slow loop would process it if it didn't check lifecycle.
	if err := store.SetIntegrityHealth("h1", index.HealthHealthy); err != nil {
		t.Fatalf("SetIntegrityHealth: %v", err)
	}

	r := reconcile.New(store, &fakeChecker{pullReachable: false})
	if err := r.SlowRun(t.Context()); err != nil {
		t.Fatalf("SlowRun: %v", err)
	}

	e, _ := store.GetByCasHash("h1")
	if e.LifecyclePhase != index.PhaseRetracted {
		t.Errorf("SlowRun must NOT change lifecycle_phase; got %q", e.LifecyclePhase)
	}
}

// ── Multiple artifacts ────────────────────────────────────────────────────────

func TestFastRun_MultipleArtifacts_EachUpdated(t *testing.T) {
	store := newTestStore(t)
	appendEntry(t, store, "h1", "bwa@0.7.17", "sha256:aaa")
	appendEntry(t, store, "h2", "samtools@1.17", "sha256:bbb")

	// Checker always returns image ok, referrer missing → all Partial.
	r := reconcile.New(store, &fakeChecker{imageExists: true, referrerExists: false})
	if err := r.FastRun(t.Context()); err != nil {
		t.Fatalf("FastRun: %v", err)
	}

	for _, hash := range []string{"h1", "h2"} {
		e, _ := store.GetByCasHash(hash)
		if e.IntegrityHealth != index.HealthPartial {
			t.Errorf("%s: want Partial, got %q", hash, e.IntegrityHealth)
		}
	}
}

// ── End-to-end spec-type honesty (real HarborChecker, stub registry) ──────────
//
// The fakes above pin judgeHealth's mapping; these two pin the whole path —
// index entry → registry evidence → integrity_health — against a stub registry,
// so a regression in what counts as spec-referrer evidence surfaces as the wrong
// health verdict rather than only as a helper-level failure.

// stubRegistry serves an image manifest plus a referrers index whose sole
// referrer carries specMediaType in its config, the way sori pushes today.
// stubRegistry serves an image carrying one referrer of specMediaType whose
// payload names ownerCasHash. The referrer is complete: a valid config digest
// and a retrievable config blob, so a successful witness is actually reachable
// rather than the check bottoming out in an indeterminate error.
func stubRegistry(t *testing.T, specMediaType, ownerCasHash string) (host string) {
	t.Helper()
	referrerDigest := "sha256:" + strings.Repeat("3", 64)
	payload := fmt.Sprintf(`{"cas_hash":%q}`, ownerCasHash)
	sum := sha256.Sum256([]byte(payload))
	cfgDigest := "sha256:" + hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/manifests/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v2/library/tool/referrers/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w,
			`{"manifests":[{"digest":%q,"artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`,
			referrerDigest)
	})
	mux.HandleFunc("/v2/library/tool/manifests/"+referrerDigest, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"config":{"mediaType":%q,"digest":%q}}`, specMediaType, cfgDigest)
	})
	mux.HandleFunc("/v2/library/tool/blobs/"+cfgDigest, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return strings.TrimPrefix(ts.URL, "http://")
}

// startingHealth is the state entries are seeded with in the end-to-end tests.
// It is deliberately neither Healthy nor Partial, so a checker that errors — and
// therefore leaves integrity_health alone — cannot satisfy any assertion below.
const startingHealth = index.HealthUnreachable

func healthAfterFastRun(t *testing.T, specMediaType string) index.IntegrityHealth {
	t.Helper()
	host := stubRegistry(t, specMediaType, "e2e")

	store := newTestStore(t)
	if err := store.Append(index.Entry{
		CasHash:         "e2e",
		ArtifactKind:    index.KindTool,
		StableRef:       "tool@1",
		ImageRef:        host + "/library/tool:latest",
		ImageDigest:     "sha256:img",
		LifecyclePhase:  index.PhaseActive,
		IntegrityHealth: startingHealth,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	checker, err := registry.NewHarborChecker(registryconfig.Config{Scheme: "http"})
	if err != nil {
		t.Fatalf("NewHarborChecker: %v", err)
	}
	if runErr := reconcile.New(store, checker).FastRun(context.Background()); runErr != nil {
		t.Fatalf("FastRun: %v", runErr)
	}
	e, err := store.GetByCasHash("e2e")
	if err != nil {
		t.Fatalf("GetByCasHash: %v", err)
	}
	return e.IntegrityHealth
}

func TestFastRun_ToolProfileReferrerAlone_IsPartialNotHealthy(t *testing.T) {
	got := healthAfterFastRun(t, "application/vnd.nodevault.toolprofile.v1+json")
	if got == index.HealthHealthy {
		t.Fatal("image + ToolProfile referrer only must not reconcile to Healthy")
	}
	if got == startingHealth {
		t.Fatalf("integrity_health is still %q: reconcile never reached a verdict", got)
	}
	if got != index.HealthPartial {
		t.Errorf("integrity_health = %q, want %q (image present, spec referrer missing)", got, index.HealthPartial)
	}
}

func TestFastRun_LegacyToolSpecReferrer_TransitionsToHealthy(t *testing.T) {
	// Seeded non-Healthy, so this only passes if the witness genuinely succeeded
	// and drove the transition — not if the checker errored and health was kept.
	got := healthAfterFastRun(t, "application/vnd.nodevault.toolspec.v1+json")
	if got == startingHealth {
		t.Fatalf("integrity_health is still %q: the witness never succeeded, health was merely preserved", got)
	}
	if got != index.HealthHealthy {
		t.Errorf("integrity_health = %q, want %q for a legacy-typed ToolSpec referrer whose payload names this entry",
			got, index.HealthHealthy)
	}
}

// ── Per-entry witness, end to end ─────────────────────────────────────────────

// sharedImageRegistry serves one image whose subject listing carries exactly one
// ToolSpec referrer, whose payload names ownerCasHash.
func sharedImageRegistry(t *testing.T, ownerCasHash string) string {
	t.Helper()
	specDigest := "sha256:" + strings.Repeat("a", 64)
	payload := fmt.Sprintf(`{"cas_hash":%q}`, ownerCasHash)
	sum := sha256.Sum256([]byte(payload))
	cfgDigest := "sha256:" + hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/manifests/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v2/library/tool/referrers/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w,
			`{"manifests":[{"digest":%q,"artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`, specDigest)
	})
	mux.HandleFunc("/v2/library/tool/manifests/"+specDigest, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w,
			`{"config":{"mediaType":"application/vnd.nodevault.toolspec.v1+json","digest":%q}}`, cfgDigest)
	})
	mux.HandleFunc("/v2/library/tool/blobs/"+cfgDigest, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return strings.TrimPrefix(ts.URL, "http://")
}

// TestFastRun_TwoEntriesShareImage_OnlyOwnerIsHealthy is the central regression:
// two entries share ImageDigest X and only entry A's ToolSpec referrer exists,
// so A must reconcile to Healthy and B to Partial.
func TestFastRun_TwoEntriesShareImage_OnlyOwnerIsHealthy(t *testing.T) {
	host := sharedImageRegistry(t, "A")
	store := newTestStore(t)
	for _, casHash := range []string{"A", "B"} {
		if err := store.Append(index.Entry{
			CasHash:         casHash,
			ArtifactKind:    index.KindTool,
			StableRef:       "tool@" + casHash,
			ImageRef:        host + "/library/tool:latest",
			ImageDigest:     "sha256:img",
			LifecyclePhase:  index.PhaseActive,
			IntegrityHealth: startingHealth,
		}); err != nil {
			t.Fatalf("Append %s: %v", casHash, err)
		}
	}

	checker, err := registry.NewHarborChecker(registryconfig.Config{Scheme: "http"})
	if err != nil {
		t.Fatalf("NewHarborChecker: %v", err)
	}
	if runErr := reconcile.New(store, checker).FastRun(context.Background()); runErr != nil {
		t.Fatalf("FastRun: %v", runErr)
	}

	for _, tc := range []struct {
		casHash string
		want    index.IntegrityHealth
	}{
		{"A", index.HealthHealthy},
		{"B", index.HealthPartial},
	} {
		e, getErr := store.GetByCasHash(tc.casHash)
		if getErr != nil {
			t.Fatalf("GetByCasHash %s: %v", tc.casHash, getErr)
		}
		if e.IntegrityHealth == startingHealth {
			t.Errorf("entry %s integrity_health is still %q: reconcile never reached a verdict",
				tc.casHash, e.IntegrityHealth)
			continue
		}
		if e.IntegrityHealth != tc.want {
			t.Errorf("entry %s integrity_health = %q, want %q", tc.casHash, e.IntegrityHealth, tc.want)
		}
	}
}

// TestFastRun_IndeterminateRegistry_LeavesHealthUntouched pins that an
// unreadable registry never downgrades an entry: no Partial, no Missing.
func TestFastRun_IndeterminateRegistry_LeavesHealthUntouched(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/manifests/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v2/library/tool/referrers/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	store := newTestStore(t)
	if err := store.Append(index.Entry{
		CasHash:         "stable",
		ArtifactKind:    index.KindTool,
		StableRef:       "tool@1",
		ImageRef:        host + "/library/tool:latest",
		ImageDigest:     "sha256:img",
		LifecyclePhase:  index.PhaseActive,
		IntegrityHealth: index.HealthHealthy,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	checker, err := registry.NewHarborChecker(registryconfig.Config{Scheme: "http"})
	if err != nil {
		t.Fatalf("NewHarborChecker: %v", err)
	}
	if runErr := reconcile.New(store, checker).FastRun(context.Background()); runErr != nil {
		t.Fatalf("FastRun: %v", runErr)
	}

	e, err := store.GetByCasHash("stable")
	if err != nil {
		t.Fatalf("GetByCasHash: %v", err)
	}
	if e.IntegrityHealth != index.HealthHealthy {
		t.Errorf("integrity_health = %q, want it left at %q — indeterminate evidence must not mutate health",
			e.IntegrityHealth, index.HealthHealthy)
	}
}

// TestFastRun_MalformedExpectedSpecReferrerDigest_LeavesHealthUntouched covers
// the case where the entry records an unusable SpecReferrerDigest and the
// registry echoes that very value back as a typed ToolSpec descriptor. Nothing
// there identifies a manifest, so it must not witness the entry — and because
// the outcome is indeterminate rather than absent, health must not move at all.
func TestFastRun_MalformedExpectedSpecReferrerDigest_LeavesHealthUntouched(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/manifests/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v2/library/tool/referrers/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(
			`{"manifests":[{"digest":"not-a-digest","artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	store := newTestStore(t)
	if err := store.Append(index.Entry{
		CasHash:            "malformed",
		ArtifactKind:       index.KindTool,
		StableRef:          "tool@1",
		ImageRef:           host + "/library/tool:latest",
		ImageDigest:        "sha256:img",
		SpecReferrerDigest: "not-a-digest",
		LifecyclePhase:     index.PhaseActive,
		IntegrityHealth:    startingHealth,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	checker, err := registry.NewHarborChecker(registryconfig.Config{Scheme: "http"})
	if err != nil {
		t.Fatalf("NewHarborChecker: %v", err)
	}
	if runErr := reconcile.New(store, checker).FastRun(context.Background()); runErr != nil {
		t.Fatalf("FastRun: %v", runErr)
	}

	e, err := store.GetByCasHash("malformed")
	if err != nil {
		t.Fatalf("GetByCasHash: %v", err)
	}
	if e.IntegrityHealth == index.HealthHealthy {
		t.Fatal("a malformed digest echoed back as a typed ToolSpec must never prove Healthy")
	}
	if e.IntegrityHealth != startingHealth {
		t.Errorf("integrity_health = %q, want it left at %q — an unusable expected digest is indeterminate",
			e.IntegrityHealth, startingHealth)
	}
}

// TestFastRun_VanishedTypedToolSpec_ReconcilesToPartial pins the health-level
// consequence: a ToolSpec that is listed but already deleted is a confirmed
// absence, so the entry must actually move to Partial rather than being frozen
// at its previous status by a spurious indeterminate error.
func TestFastRun_VanishedTypedToolSpec_ReconcilesToPartial(t *testing.T) {
	specDigest := "sha256:" + strings.Repeat("a", 64)
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/manifests/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v2/library/tool/referrers/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w,
			`{"manifests":[{"digest":%q,"artifactType":"application/vnd.nodevault.toolspec.v1+json"}]}`, specDigest)
	})
	mux.HandleFunc("/v2/library/tool/manifests/"+specDigest, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	store := newTestStore(t)
	if err := store.Append(index.Entry{
		CasHash:         "vanished",
		ArtifactKind:    index.KindTool,
		StableRef:       "tool@1",
		ImageRef:        host + "/library/tool:latest",
		ImageDigest:     "sha256:img",
		LifecyclePhase:  index.PhaseActive,
		IntegrityHealth: startingHealth,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	checker, err := registry.NewHarborChecker(registryconfig.Config{Scheme: "http"})
	if err != nil {
		t.Fatalf("NewHarborChecker: %v", err)
	}
	if runErr := reconcile.New(store, checker).FastRun(context.Background()); runErr != nil {
		t.Fatalf("FastRun: %v", runErr)
	}

	e, err := store.GetByCasHash("vanished")
	if err != nil {
		t.Fatalf("GetByCasHash: %v", err)
	}
	if e.IntegrityHealth == startingHealth {
		t.Fatalf("integrity_health is still %q: a confirmed absence was mistaken for indeterminate", e.IntegrityHealth)
	}
	if e.IntegrityHealth != index.HealthPartial {
		t.Errorf("integrity_health = %q, want %q", e.IntegrityHealth, index.HealthPartial)
	}
}

// TestFastRun_ToolSpecPayloadDeleted_ReconcilesToPartial pins the health-level
// consequence of a deleted payload: the entry must actually move to Partial
// rather than being frozen at a stale status by a spurious indeterminate.
func TestFastRun_ToolSpecPayloadDeleted_ReconcilesToPartial(t *testing.T) {
	specDigest := "sha256:" + strings.Repeat("a", 64)
	cfgDigest := "sha256:" + strings.Repeat("8", 64)
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/library/tool/manifests/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v2/library/tool/referrers/sha256:img", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w,
			`{"manifests":[{"digest":%q,"artifactType":"application/vnd.oci.image.manifest.v1+json"}]}`, specDigest)
	})
	mux.HandleFunc("/v2/library/tool/manifests/"+specDigest, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w,
			`{"config":{"mediaType":"application/vnd.nodevault.toolspec.v1+json","digest":%q}}`, cfgDigest)
	})
	mux.HandleFunc("/v2/library/tool/blobs/"+cfgDigest, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	store := newTestStore(t)
	if err := store.Append(index.Entry{
		CasHash:         "payload-gone",
		ArtifactKind:    index.KindTool,
		StableRef:       "tool@1",
		ImageRef:        host + "/library/tool:latest",
		ImageDigest:     "sha256:img",
		LifecyclePhase:  index.PhaseActive,
		IntegrityHealth: startingHealth,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	checker, err := registry.NewHarborChecker(registryconfig.Config{Scheme: "http"})
	if err != nil {
		t.Fatalf("NewHarborChecker: %v", err)
	}
	if runErr := reconcile.New(store, checker).FastRun(context.Background()); runErr != nil {
		t.Fatalf("FastRun: %v", runErr)
	}

	e, err := store.GetByCasHash("payload-gone")
	if err != nil {
		t.Fatalf("GetByCasHash: %v", err)
	}
	if e.IntegrityHealth == startingHealth {
		t.Fatalf("integrity_health is still %q: a deleted payload was mistaken for indeterminate", e.IntegrityHealth)
	}
	if e.IntegrityHealth != index.HealthPartial {
		t.Errorf("integrity_health = %q, want %q", e.IntegrityHealth, index.HealthPartial)
	}
}
