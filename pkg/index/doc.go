// Package index implements the NodeVault artifact index — the single source of truth
// for all platform-registered artifacts (tools and data).
//
// The index records approval facts, not storage facts. An artifact present in Harbor
// is not official until it appears in this index with lifecycle_phase = Active.
//
// # State model
//
// Each index entry carries two independent state axes:
//
//	lifecycle_phase: Pending | Active | Retracted | Deleted
//	  Changed only by explicit NodeVault operations (register, retract, delete).
//	  Determines Catalog visibility: only Active entries are exposed.
//
//	integrity_health: Healthy | Partial | Missing | Unreachable | Orphaned
//	  Changed only by the reconcile loop, never by lifecycle operations.
//	  Used for monitoring and alerting only — does NOT affect Catalog visibility.
//
// These two axes must never be merged into a single field. Mixing them makes it
// impossible to distinguish "intentionally retracted" from "disappeared from Harbor",
// and breaks Catalog exposure logic.
//
// # Access rules
//
// All reads and writes to the index MUST go through this package.
// Direct file or database access from other packages is forbidden.
// Other packages (build, validate, catalog, oras) call index functions; they do not
// manipulate the underlying store directly.
//
// # Transition plan
//
// See docs/NODEVAULT_TRANSITION_PLAN.md TODO-06 and TODO-08 for the full design
// and completion criteria for this package.
package index
