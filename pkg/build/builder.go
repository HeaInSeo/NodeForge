package build

import (
	"context"
	"fmt"

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

// podbridge5Builder implements Builder using podbridge5.
type podbridge5Builder struct {
	store storage.Store
}

// newPodbridge5Builder creates a Builder backed by podbridge5.
func newPodbridge5Builder() (Builder, error) {
	store, err := podbridge5.NewStore()
	if err != nil {
		return nil, fmt.Errorf("podbridge5 NewStore: %w", err)
	}
	return &podbridge5Builder{store: store}, nil
}

func (b *podbridge5Builder) Build(
	ctx context.Context, dockerfileContent, outputRef string,
) (imageID, remoteDigest string, err error) {
	imageID, remoteDigest, err = podbridge5.BuildAndPushDockerfileContent(ctx, b.store, dockerfileContent, outputRef)
	if err != nil {
		return "", "", fmt.Errorf("build and push image: %w", err)
	}
	return imageID, remoteDigest, nil
}

func (b *podbridge5Builder) Close() error {
	_, err := b.store.Shutdown(false)
	return err
}
