package index

import "time"

// ArtifactKind identifies the type of platform artifact.
type ArtifactKind string

const (
	KindTool ArtifactKind = "tool"
	KindData ArtifactKind = "data" // reserved for P3 data artifact axis
)

// LifecyclePhase is the operator-intent axis of an artifact.
// Changed only by explicit NodeVault operations — never by the reconcile loop.
type LifecyclePhase string

const (
	PhasePending   LifecyclePhase = "Pending"
	PhaseActive    LifecyclePhase = "Active"
	PhaseRetracted LifecyclePhase = "Retracted"
	PhaseDeleted   LifecyclePhase = "Deleted"
)

// IntegrityHealth is the observation axis derived from reconciling Harbor state.
// Changed only by the reconcile loop — never by lifecycle operations.
type IntegrityHealth string

const (
	HealthHealthy     IntegrityHealth = "Healthy"
	HealthPartial     IntegrityHealth = "Partial"     // image OK, spec referrer missing
	HealthMissing     IntegrityHealth = "Missing"     // both image and spec missing
	HealthUnreachable IntegrityHealth = "Unreachable" // transient access failure
	HealthOrphaned    IntegrityHealth = "Orphaned"    // spec referrer present, image missing
)

// Entry is a single record in the NodeVault artifact index.
// It carries two independent state axes — they must never be merged.
//
// Catalog visibility rule: lifecycle_phase == Active only.
// integrity_health has NO effect on Catalog visibility; it is for monitoring/alerting.
type Entry struct {
	// CasHash is the SHA256 of the tool spec JSON (content without cas_hash field).
	// Primary key. Used by pipelines for immutable pinning.
	CasHash string `json:"cas_hash"`

	// ArtifactKind distinguishes tool from data artifacts.
	ArtifactKind ArtifactKind `json:"artifact_kind"`

	// StableRef is the human-readable identifier used for UI search and Catalog listing.
	// Format: "{tool_name}@{version}". Multiple casHashes may share the same StableRef.
	// stableRef:casHash cardinality is 1:N.
	StableRef string `json:"stable_ref"`

	// ToolName and Version are the constituent parts of StableRef.
	ToolName string `json:"tool_name,omitempty"`
	Version  string `json:"version,omitempty"`

	// ImageDigest is the OCI digest of the built tool image in Harbor.
	ImageDigest string `json:"image_digest,omitempty"`

	// SpecReferrerDigest is the OCI digest of the attached spec referrer artifact.
	// Empty until pkg/oras pushes the referrer (TODO-07).
	SpecReferrerDigest string `json:"spec_referrer_digest,omitempty"`

	// ── State axis 1: operator intent ────────────────────────────────────────
	// Changed only by NodeVault explicit operations (Register, Retract, Delete).
	LifecyclePhase LifecyclePhase `json:"lifecycle_phase"`

	// ── State axis 2: Harbor observation ─────────────────────────────────────
	// Changed only by the reconcile loop. Never by lifecycle operations.
	IntegrityHealth IntegrityHealth `json:"integrity_health"`

	// ── Timestamps ───────────────────────────────────────────────────────────
	RegisteredAt       time.Time `json:"registered_at"`
	LifecycleUpdatedAt time.Time `json:"lifecycle_updated_at"`
	HealthCheckedAt    time.Time `json:"health_checked_at"`
}

// indexFile is the on-disk representation of the index.
type indexFile struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}
