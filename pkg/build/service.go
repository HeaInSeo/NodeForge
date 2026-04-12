// Package build manages builder orchestration via buildah/podbridge5.
// BuildService receives BuildRequests, calls podbridge5 to build and push images,
// streams events back to the caller, and acquires the pushed image digest.
// After L2 succeeds it drives L3 (dry-run) → L4 (smoke run) → tool registration.
package build

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"

	"github.com/HeaInSeo/NodeForge/pkg/catalog"
	"github.com/HeaInSeo/NodeForge/pkg/validate"
)

const defaultRegistryAddr = "harbor.10.113.24.96.nip.io"

func registryAddr() string {
	if v := os.Getenv("NODEFORGE_REGISTRY_ADDR"); v != "" {
		return v
	}
	return defaultRegistryAddr
}

// Service implements BuildServiceServer.
type Service struct {
	nfv1.UnimplementedBuildServiceServer
	builder   Builder
	validator *validate.Service
	registry  *catalog.ToolRegistryService
}

// NewService creates a BuildService backed by buildah/podbridge5.
func NewService(validator *validate.Service, registry *catalog.ToolRegistryService) (*Service, error) {
	builder, err := newPodbridge5Builder()
	if err != nil {
		return nil, fmt.Errorf("build service init: %w", err)
	}
	return &Service{builder: builder, validator: validator, registry: registry}, nil
}

// Close releases the underlying buildah storage.
func (s *Service) Close() error {
	return s.builder.Close()
}

// BuildAndRegister implements BuildServiceServer.
// Full orchestration: L2 (buildah build+push) → L3 (dry-run) → L4 (smoke run) → registration.
func (s *Service) BuildAndRegister(req *nfv1.BuildRequest, stream grpc.ServerStreamingServer[nfv1.BuildEvent]) error {
	ctx := stream.Context()

	send := func(kind nfv1.BuildEventKind, msg string) error {
		return stream.Send(&nfv1.BuildEvent{
			Kind:      kind,
			Message:   msg,
			Timestamp: time.Now().UnixMilli(),
		})
	}

	destination := fmt.Sprintf("%s/library/%s:latest", registryAddr(), sanitizeName(req.ToolName))

	// ── L2: buildah build + push ─────────────────────────────────────────────────

	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_JOB_CREATED, "buildah build starting: "+destination)
	slog.Info("buildah build starting", "destination", destination)

	_, digest, err := s.builder.Build(ctx, req.DockerfileContent, destination)
	if err != nil {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_FAILED, err.Error())
		return fmt.Errorf("buildah build: %w", err)
	}

	slog.Info("buildah build succeeded", "destination", destination, "digest", digest)
	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_PUSH_SUCCEEDED, "image pushed to "+destination)

	imageWithDigest := destination + "@" + digest
	_ = stream.Send(&nfv1.BuildEvent{
		Kind:      nfv1.BuildEventKind_BUILD_EVENT_KIND_DIGEST_ACQUIRED,
		Message:   imageWithDigest,
		Digest:    digest,
		Timestamp: time.Now().UnixMilli(),
	})

	// ── L3: dry-run ──────────────────────────────────────────────────────────────

	reqID := req.RequestId
	if len(reqID) > 8 {
		reqID = reqID[:8]
	}
	jobSuffix := sanitizeName(reqID)
	if jobSuffix == "" {
		jobSuffix = fmt.Sprintf("%04x", time.Now().UnixMilli()%0xFFFF)
	}
	smokeJob := validate.SmokeJobSpec("nfsmoke-"+jobSuffix, imageWithDigest)
	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "L3: submitting dry-run...")

	dryResult := s.validator.DryRunJob(ctx, smokeJob)
	if !dryResult.Success {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_FAILED, "L3 dry-run failed: "+dryResult.ErrorMessage)
		return fmt.Errorf("L3 dry-run failed: %s", dryResult.ErrorMessage)
	}
	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "L3 dry-run passed")

	// ── L4: smoke run ────────────────────────────────────────────────────────────

	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "L4: starting smoke run...")

	smokeResult := s.validator.SmokeRunJob(ctx, smokeJob)
	if !smokeResult.Success {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_FAILED, "L4 smoke run failed: "+smokeResult.ErrorMessage)
		return fmt.Errorf("L4 smoke run failed: %s", smokeResult.ErrorMessage)
	}
	if smokeResult.LogOutput != "" {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "smoke log: "+strings.TrimSpace(smokeResult.LogOutput))
	}
	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "L4 smoke run passed")

	// ── 등록 ─────────────────────────────────────────────────────────────────────

	regResp, regErr := s.registry.RegisterTool(ctx, &nfv1.RegisterToolRequest{
		RequestId:        req.RequestId,
		ToolDefinitionId: req.ToolDefinitionId,
		ToolName:         req.ToolName,
		ImageUri:         destination,
		Digest:           digest,
		EnvironmentSpec:  req.EnvironmentSpec,
		Version:          req.Version,
		Inputs:           req.Inputs,
		Outputs:          req.Outputs,
		Display:          req.Display,
		Command:          req.Command,
	})
	if regErr != nil {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "registration warning: "+regErr.Error())
	} else {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "tool registered: cas="+regResp.CasHash)
	}

	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_SUCCEEDED,
		fmt.Sprintf("build+register complete: %s@%s", destination, digest))
	return nil
}

// sanitizeName makes a string safe for use as an image name component.
func sanitizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 50 {
		result = result[:50]
	}
	return result
}
