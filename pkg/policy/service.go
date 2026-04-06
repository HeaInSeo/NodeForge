// Package policy manages DockGuard policy bundles.
// PolicyService compiles .rego files to .wasm via opa build and serves them to NodeKit.
package policy

// Service is a stub — full implementation in Phase 2.
type Service struct{}

// NewService creates a new PolicyService stub.
func NewService() *Service {
	return &Service{}
}
