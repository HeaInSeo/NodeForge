package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	schemaVersion   = 1
	defaultIndexDir = "assets/index"
	indexFileName   = "vault-index.json"
)

// Store is the single control layer for the NodeVault artifact index.
// All index reads and writes MUST go through this type.
// Direct access to the underlying file from other packages is forbidden.
//
// State transition rules (enforced by callers, not the store itself):
//   - SetLifecyclePhase: called only by NodeVault explicit operations.
//   - SetIntegrityHealth: called only by the reconcile loop.
type Store struct {
	mu   sync.RWMutex
	path string     // path to vault-index.json
	idx  *indexFile // in-memory cache; nil before first load
}

// ErrNotFound is returned when a requested entry does not exist.
var ErrNotFound = errors.New("index: entry not found")

// New creates a Store backed by the JSON file at dir/vault-index.json.
// The directory is created if it does not exist.
// INDEX_DIR env overrides the default directory.
func New() (*Store, error) {
	dir := os.Getenv("INDEX_DIR")
	if dir == "" {
		dir = defaultIndexDir
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("index: mkdir %s: %w", dir, err)
	}
	s := &Store{path: filepath.Join(dir, indexFileName)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// NewAt creates a Store at a specific path — useful for testing.
func NewAt(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("index: mkdir %s: %w", dir, err)
	}
	s := &Store{path: filepath.Join(dir, indexFileName)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Append adds a new entry to the index.
// Returns an error if an entry with the same CasHash already exists.
func (s *Store) Append(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e.CasHash == "" {
		return errors.New("index: CasHash must not be empty")
	}
	for _, existing := range s.idx.Entries {
		if existing.CasHash == e.CasHash {
			return fmt.Errorf("index: entry %q already exists", e.CasHash)
		}
	}
	now := time.Now().UTC()
	if e.RegisteredAt.IsZero() {
		e.RegisteredAt = now
	}
	if e.LifecycleUpdatedAt.IsZero() {
		e.LifecycleUpdatedAt = now
	}
	if e.HealthCheckedAt.IsZero() {
		e.HealthCheckedAt = now
	}
	s.idx.Entries = append(s.idx.Entries, e)
	return s.save()
}

// GetByCasHash returns the entry with the given CAS hash.
// Returns ErrNotFound if no such entry exists.
func (s *Store) GetByCasHash(casHash string) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, e := range s.idx.Entries {
		if e.CasHash == casHash {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("%w: cas_hash=%q", ErrNotFound, casHash)
}

// ListByStableRef returns all entries with the given stableRef.
// Returns an empty slice (not an error) if none match.
func (s *Store) ListByStableRef(stableRef string) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []Entry
	for _, e := range s.idx.Entries {
		if e.StableRef == stableRef {
			out = append(out, e)
		}
	}
	return out, nil
}

// ListActive returns all entries with lifecycle_phase == Active.
// Catalog exposure rule: Active only. IntegrityHealth is not checked here.
func (s *Store) ListActive() ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []Entry
	for _, e := range s.idx.Entries {
		if e.LifecyclePhase == PhaseActive {
			out = append(out, e)
		}
	}
	return out, nil
}

// SetLifecyclePhase updates the lifecycle_phase of the entry identified by casHash.
//
// IMPORTANT: This method MUST be called only by NodeVault explicit operations
// (register, retract, delete). The reconcile loop must never call this method.
func (s *Store) SetLifecyclePhase(casHash string, phase LifecyclePhase) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := s.findIndex(casHash)
	if err != nil {
		return err
	}
	s.idx.Entries[idx].LifecyclePhase = phase
	s.idx.Entries[idx].LifecycleUpdatedAt = time.Now().UTC()
	return s.save()
}

// SetSpecReferrerDigest records the OCI referrer digest after a successful spec push.
// Called by pkg/oras after PushToolSpecReferrer or PushDataSpecReferrer succeeds.
func (s *Store) SetSpecReferrerDigest(casHash, referrerDigest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := s.findIndex(casHash)
	if err != nil {
		return err
	}
	s.idx.Entries[idx].SpecReferrerDigest = referrerDigest
	return s.save()
}

// SetIntegrityHealth updates the integrity_health of the entry identified by casHash.
//
// IMPORTANT: This method MUST be called only by the reconcile loop.
// Lifecycle operations (register, retract, delete) must never call this method.
func (s *Store) SetIntegrityHealth(casHash string, health IntegrityHealth) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := s.findIndex(casHash)
	if err != nil {
		return err
	}
	s.idx.Entries[idx].IntegrityHealth = health
	s.idx.Entries[idx].HealthCheckedAt = time.Now().UTC()
	return s.save()
}

// All returns a snapshot of all entries. Safe for read-only iteration.
func (s *Store) All() ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Entry, len(s.idx.Entries))
	copy(out, s.idx.Entries)
	return out, nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

func (s *Store) findIndex(casHash string) (int, error) {
	for i, e := range s.idx.Entries {
		if e.CasHash == casHash {
			return i, nil
		}
	}
	return -1, fmt.Errorf("%w: cas_hash=%q", ErrNotFound, casHash)
}

// Reload re-reads vault-index.json from disk, replacing the in-memory cache.
// Called by NodePalette before handling each HTTP request to pick up changes
// written by NodeVault (separate process, shared filesystem).
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.idx = &indexFile{SchemaVersion: schemaVersion}
			return nil
		}
		return fmt.Errorf("index: read %s: %w", s.path, err)
	}
	var f indexFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("index: parse %s: %w", s.path, err)
	}
	s.idx = &f
	return nil
}

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.idx, "", "  ")
	if err != nil {
		return fmt.Errorf("index: marshal: %w", err)
	}
	//nolint:gosec // path is operator-configured and not from user input
	if err := os.WriteFile(s.path, data, 0o640); err != nil {
		return fmt.Errorf("index: write %s: %w", s.path, err)
	}
	return nil
}
