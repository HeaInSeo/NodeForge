package podbridge5

import (
	"context"
	"fmt"
	"strings"

	"github.com/containers/podman/v5/libpod/define"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/containers/podman/v5/pkg/bindings/images"
	entitiesTypes "github.com/containers/podman/v5/pkg/domain/entities/types"
	"github.com/containers/podman/v5/pkg/specgen"
	"github.com/seoyhaein/utils"
)

type containerRuntime interface {
	ContainerExists(ctx context.Context, name string) (bool, error)
	EnsureImage(ctx context.Context, imageRef string) error
	CreateContainer(ctx context.Context, spec *specgen.SpecGenerator) (*entitiesTypes.ContainerCreateResponse, error)
	StartContainer(ctx context.Context, containerID string) error
	InspectContainer(ctx context.Context, containerID string) (*define.InspectContainerData, error)
}

type podmanContainerRuntime struct{}

func (podmanContainerRuntime) ContainerExists(ctx context.Context, name string) (bool, error) {
	exists, err := containers.Exists(ctx, name, &containers.ExistsOptions{External: utils.PFalse})
	if err != nil {
		return false, fmt.Errorf("failed to check if container exists: %w", err)
	}
	return exists, nil
}

func (podmanContainerRuntime) EnsureImage(ctx context.Context, imageRef string) error {
	exists, err := images.Exists(ctx, imageRef, nil)
	if err != nil {
		return fmt.Errorf("failed to check if image exists: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := images.Pull(ctx, imageRef, &images.PullOptions{}); err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	return nil
}

func (podmanContainerRuntime) CreateContainer(ctx context.Context, spec *specgen.SpecGenerator) (*entitiesTypes.ContainerCreateResponse, error) {
	createResponse, err := containers.CreateWithSpec(ctx, spec, &containers.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}
	return &createResponse, nil
}

func (podmanContainerRuntime) StartContainer(ctx context.Context, containerID string) error {
	if err := containers.Start(ctx, containerID, &containers.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

func (podmanContainerRuntime) InspectContainer(ctx context.Context, containerID string) (*define.InspectContainerData, error) {
	data, err := containers.Inspect(ctx, containerID, &containers.InspectOptions{Size: utils.PFalse})
	if err != nil {
		return nil, fmt.Errorf("inspect container %q: %w", containerID, err)
	}
	return data, nil
}

func startContainerWithRuntime(ctx context.Context, runtime containerRuntime, spec *specgen.SpecGenerator) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("context is nil")
	}
	if spec == nil {
		return "", fmt.Errorf("spec is nil")
	}

	ccr, err := createContainerWithRuntime(ctx, runtime, spec)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	if err := runtime.StartContainer(ctx, ccr.ID); err != nil {
		return "", err
	}

	return ccr.ID, nil
}

func createContainerWithRuntime(ctx context.Context, runtime containerRuntime, conSpec *specgen.SpecGenerator) (*CreateContainerResult, error) {
	if err := conSpec.Validate(); err != nil {
		Log.Errorf("validation failed: %v", err)
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	if utils.IsEmptyString(conSpec.Name) || utils.IsEmptyString(conSpec.Image) {
		Log.Error("Container's name or image's name is not set")
		return nil, fmt.Errorf("container name or image's name is not set")
	}

	containerExists, err := runtime.ContainerExists(ctx, conSpec.Name)
	if err != nil {
		Log.Errorf("Failed to check if container exists: %v", err)
		return nil, err
	}
	if containerExists {
		return handleExistingContainerWithRuntime(ctx, runtime, conSpec.Name)
	}

	if err := runtime.EnsureImage(ctx, conSpec.Image); err != nil {
		Log.Errorf("Failed to ensure image: %v", err)
		return nil, err
	}

	Log.Infof("Creating %s container using %s image...", conSpec.Name, conSpec.Image)
	createResponse, err := runtime.CreateContainer(ctx, conSpec)
	if err != nil {
		Log.Errorf("Failed to create container: %v", err)
		return nil, err
	}

	return &CreateContainerResult{
		Name:     conSpec.Name,
		ID:       createResponse.ID,
		Warnings: createResponse.Warnings,
		Status:   Created,
	}, nil
}

func inspectContainerWithRuntime(ctx context.Context, runtime containerRuntime, containerID string) (*define.InspectContainerData, error) {
	return runtime.InspectContainer(ctx, containerID)
}

func handleExistingContainerWithRuntime(ctx context.Context, runtime containerRuntime, containerName string) (*CreateContainerResult, error) {
	info, err := runtime.InspectContainer(ctx, containerName)
	if err != nil {
		return nil, err
	}

	return &CreateContainerResult{
		Name:   containerName,
		ID:     info.ID,
		Status: containerStatusFromInspectState(info.State),
	}, nil
}

func containerStatusFromInspectState(state *define.InspectContainerState) ContainerStatus {
	if state == nil {
		return Created
	}

	switch {
	case state.Running:
		return Running
	case state.Paused:
		return Paused
	case state.Dead:
		return Dead
	case strings.EqualFold(state.Status, "created") || strings.EqualFold(state.Status, "configured"):
		return Created
	case state.ExitCode >= 0:
		if state.ExitCode == 0 {
			return Exited
		}
		return ExitedErr
	default:
		return Created
	}
}
