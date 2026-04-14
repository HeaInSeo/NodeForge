// Package oras implements OCI referrer push for NodeVault artifacts.
//
// After a tool image or data image is pushed to Harbor, this package attaches
// the corresponding spec (tool spec JSON or data spec JSON) as an OCI referrer
// artifact linked to the subject image digest.
//
// # mediaType conventions
//
//	Tool spec:  application/vnd.nodevault.toolspec.v1+json
//	Data spec:  application/vnd.nodevault.dataspec.v1+json
//
// Both tool and data artifacts use the same referrer attachment pattern.
// The subject is always the image manifest digest.
//
// # Relationship to pkg/index
//
// oras pushes the referrer artifact to Harbor; pkg/index records the spec digest
// in the index entry. Both must succeed before an artifact is considered registered.
// oras does not write to the index — that is pkg/index's responsibility.
//
// # Transition plan
//
// See docs/NODEVAULT_TRANSITION_PLAN.md TODO-07 for the full design
// and completion criteria for this package.
package oras
