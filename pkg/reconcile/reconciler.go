// Package reconcile implements the NodeVault artifact integrity reconcile loop.
//
// Design principles:
//   - reconcile-first: correct state even without webhooks.
//   - Only modifies integrity_health. Never touches lifecycle_phase.
//   - Two separate loops: fast (manifest existence) and slow (reachability pull).
//
// State judgment model:
//
//	image ✓ / spec ✓              → Healthy
//	image ✓ / spec ✗              → Partial   (image ok, spec referrer missing)
//	image ✗ / spec ✗              → Missing   (both gone — alert)
//	image ✗ / spec ✓              → Orphaned  (ghost referrer — do NOT auto-delete)
//	image ✓ / spec ✓ / pull fail  → Unreachable (transient)
//
// lifecycle_phase is NEVER changed by this package.
// Only integrity_health is changed, via index.Store.SetIntegrityHealth().
package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/HeaInSeo/NodeVault/pkg/index"
)

// RegistryChecker is the interface for checking artifact state in the OCI registry.
// Implemented by pkg/registry.Client in production; replaced by fakes in tests.
type RegistryChecker interface {
	// ImageExists checks whether a manifest with the given digest exists in the registry.
	ImageExists(ctx context.Context, imageDigest string) (bool, error)

	// ReferrerExists checks whether a spec referrer artifact attached to subjectDigest exists.
	ReferrerExists(ctx context.Context, subjectDigest string) (bool, error)

	// PullReachable checks whether the image can actually be pulled (slow check).
	PullReachable(ctx context.Context, imageDigest string) (bool, error)
}

// Reconciler reconciles the NodeVault index against the actual registry state.
type Reconciler struct {
	store   *index.Store
	checker RegistryChecker
}

// New creates a Reconciler.
func New(store *index.Store, checker RegistryChecker) *Reconciler {
	return &Reconciler{store: store, checker: checker}
}

// ── Fast loop ─────────────────────────────────────────────────────────────────

// FastRun runs one pass of the fast reconcile loop.
// Checks manifest existence + referrer existence for all index entries.
// Updates integrity_health only. Never touches lifecycle_phase.
func (r *Reconciler) FastRun(ctx context.Context) error {
	entries, err := r.store.All()
	if err != nil {
		return fmt.Errorf("reconcile fast run: list all: %w", err)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.reconcileExistence(ctx, e); err != nil {
			// Log and continue — one artifact failure must not halt the whole loop.
			fmt.Printf("reconcile: fast check %s: %v\n", e.CasHash, err)
		}
	}
	return nil
}

// ReconcileOne runs a targeted fast check for a single artifact by CAS hash.
// Called by the webhook fast path when Harbor sends a push or delete event.
func (r *Reconciler) ReconcileOne(ctx context.Context, casHash string) error {
	e, err := r.store.GetByCasHash(casHash)
	if err != nil {
		return fmt.Errorf("reconcile one: %w", err)
	}
	return r.reconcileExistence(ctx, e)
}

// reconcileExistence checks image + referrer existence and updates integrity_health.
func (r *Reconciler) reconcileExistence(ctx context.Context, e index.Entry) error {
	imageOK, err := r.checker.ImageExists(ctx, e.ImageDigest)
	if err != nil {
		return fmt.Errorf("image exists check: %w", err)
	}

	referrerOK, err := r.checker.ReferrerExists(ctx, e.ImageDigest)
	if err != nil {
		return fmt.Errorf("referrer exists check: %w", err)
	}

	health := judgeHealth(imageOK, referrerOK)
	return r.store.SetIntegrityHealth(e.CasHash, health)
}

// ── Slow loop ─────────────────────────────────────────────────────────────────

// SlowRun runs one pass of the slow reconcile loop.
// Attempts actual pull verification for Healthy entries.
// Only transitions Healthy → Unreachable (not the reverse — fast loop handles recovery).
// Never touches lifecycle_phase.
func (r *Reconciler) SlowRun(ctx context.Context) error {
	entries, err := r.store.All()
	if err != nil {
		return fmt.Errorf("reconcile slow run: list all: %w", err)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Only pull-verify Healthy entries — non-Healthy is already flagged by fast loop.
		if e.IntegrityHealth != index.HealthHealthy {
			continue
		}
		if err := r.reconcileReachability(ctx, e); err != nil {
			fmt.Printf("reconcile: slow check %s: %v\n", e.CasHash, err)
		}
	}
	return nil
}

// reconcileReachability attempts a pull and updates integrity_health if unreachable.
func (r *Reconciler) reconcileReachability(ctx context.Context, e index.Entry) error {
	ok, err := r.checker.PullReachable(ctx, e.ImageDigest)
	if err != nil {
		return fmt.Errorf("pull reachable check: %w", err)
	}
	if !ok {
		return r.store.SetIntegrityHealth(e.CasHash, index.HealthUnreachable)
	}
	return nil
}

// ── Loop runners (background goroutine helpers) ───────────────────────────────

// RunFastLoop starts a background goroutine that calls FastRun every fastInterval.
// Stops when ctx is cancelled.
func (r *Reconciler) RunFastLoop(ctx context.Context, fastInterval time.Duration) {
	go func() {
		ticker := time.NewTicker(fastInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.FastRun(ctx); err != nil && ctx.Err() == nil {
					fmt.Printf("reconcile: fast loop error: %v\n", err)
				}
			}
		}
	}()
}

// RunSlowLoop starts a background goroutine that calls SlowRun every slowInterval.
// Stops when ctx is cancelled.
func (r *Reconciler) RunSlowLoop(ctx context.Context, slowInterval time.Duration) {
	go func() {
		ticker := time.NewTicker(slowInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.SlowRun(ctx); err != nil && ctx.Err() == nil {
					fmt.Printf("reconcile: slow loop error: %v\n", err)
				}
			}
		}
	}()
}

// ── Health judgment ───────────────────────────────────────────────────────────

// judgeHealth maps (imageOK, referrerOK) → IntegrityHealth.
//
//	image ✓ / referrer ✓  → Healthy
//	image ✓ / referrer ✗  → Partial
//	image ✗ / referrer ✗  → Missing
//	image ✗ / referrer ✓  → Orphaned  (DO NOT auto-delete)
func judgeHealth(imageOK, referrerOK bool) index.IntegrityHealth {
	switch {
	case imageOK && referrerOK:
		return index.HealthHealthy
	case imageOK && !referrerOK:
		return index.HealthPartial
	case !imageOK && !referrerOK:
		return index.HealthMissing
	default: // !imageOK && referrerOK
		return index.HealthOrphaned
	}
}
