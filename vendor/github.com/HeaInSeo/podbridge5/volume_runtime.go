package podbridge5

import (
	"context"
	"fmt"

	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/containers/podman/v5/pkg/bindings/images"
	"github.com/containers/podman/v5/pkg/specgen"
)

const (
	volumeTransferImage       = "docker.io/library/alpine:latest"
	volumeWriterContainerName = "temp-folder-writer"
	volumeReaderContainerName = "temp-data-reader"
)

type volumeContainerRuntime interface {
	EnsureImage(ctx context.Context, imageRef string) error
	CreateContainer(ctx context.Context, spec *specgen.SpecGenerator) (string, error)
	StartContainer(ctx context.Context, containerID string) error
	StopContainer(ctx context.Context, containerID string) error
	RemoveContainer(ctx context.Context, containerID string) error
}

type podmanVolumeContainerRuntime struct{}

func (podmanVolumeContainerRuntime) EnsureImage(ctx context.Context, imageRef string) error {
	exists, err := images.Exists(ctx, imageRef, nil)
	if err != nil {
		return fmt.Errorf("image exists check: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := images.Pull(ctx, imageRef, &images.PullOptions{}); err != nil {
		return fmt.Errorf("image pull: %w", err)
	}
	return nil
}

func (podmanVolumeContainerRuntime) CreateContainer(ctx context.Context, spec *specgen.SpecGenerator) (string, error) {
	resp, err := containers.CreateWithSpec(ctx, spec, nil)
	if err != nil {
		return "", fmt.Errorf("container create: %w", err)
	}
	return resp.ID, nil
}

func (podmanVolumeContainerRuntime) StartContainer(ctx context.Context, containerID string) error {
	if err := containers.Start(ctx, containerID, nil); err != nil {
		return fmt.Errorf("container start: %w", err)
	}
	return nil
}

func (podmanVolumeContainerRuntime) StopContainer(ctx context.Context, containerID string) error {
	if err := containers.Stop(ctx, containerID, nil); err != nil {
		return fmt.Errorf("container stop: %w", err)
	}
	return nil
}

func (podmanVolumeContainerRuntime) RemoveContainer(ctx context.Context, containerID string) error {
	if _, err := containers.Remove(ctx, containerID, nil); err != nil {
		return fmt.Errorf("container remove: %w", err)
	}
	return nil
}

func newVolumeWriterSpec(volumeName, mountPath string) (*specgen.SpecGenerator, error) {
	return NewSpec(
		WithImageName(volumeTransferImage),
		WithName(volumeWriterContainerName),
		WithEnv("MOUNT", mountPath),
		WithCommand([]string{
			"sh", "-c",
			"mkdir -p \"$MOUNT\"; exec tail -f /dev/null",
		}),
		WithNamedVolume(volumeName, mountPath, ""),
	)
}

func newVolumeReaderSpec(volumeName, mountPath string) (*specgen.SpecGenerator, error) {
	return NewSpec(
		WithImageName(volumeTransferImage),
		WithName(volumeReaderContainerName),
		WithCommand([]string{"sh", "-c", "mkdir -p /data && sleep infinity"}),
		WithNamedVolume(volumeName, mountPath, ""),
	)
}

func startVolumeContainer(ctx context.Context, runtime volumeContainerRuntime, spec *specgen.SpecGenerator) (string, func(), error) {
	if err := runtime.EnsureImage(ctx, spec.Image); err != nil {
		return "", nil, err
	}

	containerID, err := runtime.CreateContainer(ctx, spec)
	if err != nil {
		return "", nil, err
	}

	cleanup := func() {
		if stopErr := runtime.StopContainer(ctx, containerID); stopErr != nil {
			Log.Warnf("stop container %s: %v", containerID, stopErr)
		}
		if rmErr := runtime.RemoveContainer(ctx, containerID); rmErr != nil {
			Log.Warnf("remove container %s: %v", containerID, rmErr)
		}
	}

	if err := runtime.StartContainer(ctx, containerID); err != nil {
		cleanup()
		return "", nil, err
	}

	return containerID, cleanup, nil
}
