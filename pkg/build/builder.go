package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/HeaInSeo/podbridge5"
	"github.com/containers/storage"
)

// Builder builds and pushes a container image from Dockerfile content.
// outputRef is the full destination reference, e.g. "harbor.example.com/myimage:latest".
// Build builds the image, pushes it to the registry, and returns the remote digest
// as reported by the registry after push.
type Builder interface {
	Build(ctx context.Context, dockerfileContent, outputRef string) (imageID, digest string, err error)
	Close() error
}

// podbridge5Builder implements Builder using the buildah library via podbridge5.
type podbridge5Builder struct {
	store storage.Store
}

// newPodbridge5Builder creates a Builder backed by buildah via podbridge5.
func newPodbridge5Builder() (Builder, error) {
	store, err := podbridge5.NewStore()
	if err != nil {
		return nil, fmt.Errorf("podbridge5 NewStore: %w", err)
	}
	return &podbridge5Builder{store: store}, nil
}

func (b *podbridge5Builder) Build(ctx context.Context, dockerfileContent, outputRef string) (string, string, error) {
	imageID, _, err := podbridge5.BuildDockerfileContent(ctx, b.store, dockerfileContent, outputRef)
	if err != nil {
		return "", "", err
	}
	remoteDigest, err := b.push(ctx, outputRef)
	if err != nil {
		return "", "", fmt.Errorf("push to registry: %w", err)
	}
	return imageID, remoteDigest, nil
}

// push pushes the locally built image to the registry using buildah and returns
// the digest as assigned by the registry (via --digestfile).
func (b *podbridge5Builder) push(ctx context.Context, outputRef string) (string, error) {
	digestFile, err := os.CreateTemp("", "nodeforge-digest-*")
	if err != nil {
		return "", fmt.Errorf("create digest temp file: %w", err)
	}
	digestPath := digestFile.Name()
	digestFile.Close()
	defer os.Remove(digestPath)

	cmd := exec.CommandContext(ctx, "buildah", "push", "--tls-verify=false", "--digestfile", digestPath, outputRef)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("buildah push %s: %w\n%s", outputRef, err, out)
	}

	raw, err := os.ReadFile(digestPath)
	if err != nil {
		return "", fmt.Errorf("read digest file: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func (b *podbridge5Builder) Close() error {
	_, err := b.store.Shutdown(false)
	return err
}
