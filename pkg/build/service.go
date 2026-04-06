// Package build manages builder Job orchestration.
// BuildService receives BuildRequests, creates K8s Jobs, tracks status,
// collects logs, and confirms registry push + digest acquisition.
package build

// Service is a stub — full implementation in Phase 2.
type Service struct{}

// NewService creates a new BuildService stub.
func NewService() *Service {
	return &Service{}
}
