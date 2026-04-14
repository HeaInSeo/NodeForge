package podbridge5

import (
	"context"
	"errors"
	"fmt"
	"github.com/containers/podman/v5/libpod/define"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/containers/podman/v5/pkg/bindings/images"
	"github.com/containers/podman/v5/pkg/specgen"
	"github.com/seoyhaein/utils"
	"strings"
)

type ContainerStatus int

const (
	Created   ContainerStatus = iota //0
	Running                          // 1
	Exited                           // 2
	ExitedErr                        // 3
	Healthy                          // 4
	Unhealthy                        // 5
	Dead                             // 6
	Paused                           // 7
	UnKnown                          // 8
	None                             // 9
)

type ContainerOptions func(spec *specgen.SpecGenerator) error

// CreateContainerResult 컨테이너 생성 정보를 담는 구조체
type (
	CreateContainerResult struct {
		Name     string
		ID       string
		Warnings []string
		Status   ContainerStatus
	}
)

// NewSpec creates a new SpecGenerator.
func NewSpec(opts ...ContainerOptions) (*specgen.SpecGenerator, error) {
	spec := &specgen.SpecGenerator{}
	for _, opt := range opts {
		if err := opt(spec); err != nil {
			return nil, err
		}
	}
	return spec, nil
}

func WithImageName(imgName string) ContainerOptions {
	return func(spec *specgen.SpecGenerator) error {
		spec.Image = imgName
		return nil
	}
}

func WithName(name string) ContainerOptions {
	return func(spec *specgen.SpecGenerator) error {
		spec.Name = name
		return nil
	}
}

func WithTerminal(terminal bool) ContainerOptions {
	return func(spec *specgen.SpecGenerator) error {
		spec.Terminal = &terminal
		return nil
	}
}

// WithPod sets the pod ID for a container spec, allowing the container to join the given pod.
func WithPod(podID string) ContainerOptions {
	return func(spec *specgen.SpecGenerator) error {
		spec.Pod = podID
		return nil
	}
}

func WithSysAdmin() ContainerOptions {
	return func(spec *specgen.SpecGenerator) error {
		spec.CapAdd = append(spec.CapAdd, "SYS_ADMIN")
		return nil
	}
}

// WithUnconfinedSeccomp sets the container’s seccomp policy to “unconfined”,
// allowing syscalls like mount(2) that the default profile would block.
func WithUnconfinedSeccomp() ContainerOptions {
	return func(spec *specgen.SpecGenerator) error {
		spec.SeccompPolicy = "unconfined"
		return nil
	}
}

// WithEnv 단일 키/값 환경변수 추가
func WithEnv(key, value string) ContainerOptions {
	return func(spec *specgen.SpecGenerator) error {
		if spec.Env == nil {
			spec.Env = make(map[string]string)
		}
		spec.Env[key] = value
		return nil
	}
}

// WithEnvs 여러 개를 한 번에 추가하고 싶다면 (옵션)
func WithEnvs(envs map[string]string) ContainerOptions {
	return func(spec *specgen.SpecGenerator) error {
		if spec.Env == nil {
			spec.Env = make(map[string]string)
		}
		for k, v := range envs {
			spec.Env[k] = v
		}
		return nil
	}
}

func WithCommand(cmd []string) ContainerOptions {
	return func(spec *specgen.SpecGenerator) error {
		spec.Command = cmd
		return nil
	}
}

// WithHealthChecker healthcheck 설정에 문제가 발생하면 에러를 반환
func WithHealthChecker(inCmd, interval string, retries uint, timeout, startPeriod string) ContainerOptions {
	// 한 번만 파싱/검증
	hc, err := ParseHealthcheckConfig(inCmd, interval, retries, timeout, startPeriod)
	return func(spec *specgen.SpecGenerator) error {
		if err != nil {
			// 옵션 생성 시점에 실패 원인을 그대로 반환
			return fmt.Errorf("invalid healthcheck config: %w", err)
		}
		spec.HealthConfig = hc
		return nil
	}
}

// StartContainer 컨테이너를 만들고 시작함.
func StartContainer(ctx context.Context, spec *specgen.SpecGenerator) (string, error) {
	if ctx == nil {
		return "", errors.New("context is nil")
	}

	if spec == nil {
		return "", errors.New("spec is nil")
	}

	ccr, err := CreateContainer(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	if err := containers.Start(ctx, ccr.ID, &containers.StartOptions{}); err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	return ccr.ID, nil
}

// CreateContainer 컨테이너 생성
func CreateContainer(ctx context.Context, conSpec *specgen.SpecGenerator) (*CreateContainerResult, error) {
	if err := conSpec.Validate(); err != nil {
		Log.Errorf("validation failed: %v", err)
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	if utils.IsEmptyString(conSpec.Name) || utils.IsEmptyString(conSpec.Image) {
		Log.Error("Container's name or image's name is not set")
		return nil, errors.New("container name or image's name is not set")
	}

	// 컨테이너가 local storage 에 존재하는지 확인
	containerExists, err := containers.Exists(ctx, conSpec.Name, &containers.ExistsOptions{External: utils.PFalse})
	if err != nil {
		Log.Errorf("Failed to check if container exists: %v", err)
		return nil, fmt.Errorf("failed to check if container exists: %w", err)
	}

	if containerExists {
		return handleExistingContainer(ctx, conSpec.Name)
	}

	// 이미지가 존재하는지 확인
	imageExists, err := images.Exists(ctx, conSpec.Image, nil)
	if err != nil {
		Log.Errorf("Failed to check if image exists: %v", err)
		return nil, fmt.Errorf("failed to check if image exists: %w", err)
	}

	if !imageExists {
		Log.Infof("Pulling %s image...", conSpec.Image)
		if _, err := images.Pull(ctx, conSpec.Image, &images.PullOptions{}); err != nil {
			Log.Errorf("Failed to pull image: %v", err)
			return nil, fmt.Errorf("failed to pull image: %w", err)
		}
	}

	Log.Infof("Creating %s container using %s image...", conSpec.Name, conSpec.Image)
	createResponse, err := containers.CreateWithSpec(ctx, conSpec, &containers.CreateOptions{})
	if err != nil {
		Log.Errorf("Failed to create container: %v", err)
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	return &CreateContainerResult{
		Name:     conSpec.Name,
		ID:       createResponse.ID,
		Warnings: createResponse.Warnings,
		Status:   Created,
	}, nil
}

func InspectContainer(ctx context.Context, containerID string) (*define.InspectContainerData, error) {
	data, err := containers.Inspect(ctx, containerID, &containers.InspectOptions{Size: utils.PFalse})
	if err != nil {
		return nil, fmt.Errorf("inspect container %q: %w", containerID, err)
	}
	return data, nil
}

// HealthCheckContainer returns the container's Status string and an exitCode:
//   - exitCode == -1 : no health information available
//   - exitCode ==  0 : healthy or exitCode=0
//   - exitCode  > 0 : the first nonzero exit code from health logs
func HealthCheckContainer(ctx context.Context, containerID string) (status string, exitCode int, err error) {
	// 1) Inspect
	data, err := InspectContainer(ctx, containerID)
	if err != nil {
		return "", -1, err
	}

	// 2) 상태
	if data.State.Status == "" {
		return "", -1, fmt.Errorf("container %q state status is empty", containerID)
	}
	status = data.State.Status

	// 3) 헬스 정보
	if data.State.Health == nil || len(data.State.Health.Log) == 0 {
		// 헬스체크가 설정되지 않았거나 로그가 없는 경우
		return status, -1, nil
	}

	// 로그에서 첫 번째 비정상 exitCode 찾기
	for _, entry := range data.State.Health.Log {
		if entry.ExitCode != 0 {
			return status, entry.ExitCode, nil
		}
	}

	// 모든 로그가 exitCode==0
	return status, 0, nil
}

// handleExistingContainer 컨테이너가 존재했을 경우 해당 컨테이너의 정보를 리턴함.
func handleExistingContainer(ctx context.Context, containerName string) (*CreateContainerResult, error) {
	info, err := containers.Inspect(ctx, containerName, &containers.InspectOptions{Size: utils.PFalse})
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %q: %w", containerName, err)
	}

	s := info.State
	var status ContainerStatus

	switch {
	case s.Running:
		status = Running
	case s.Paused:
		status = Paused
	case s.Dead:
		status = Dead
	case strings.EqualFold(s.Status, "created") || strings.EqualFold(s.Status, "configured"):
		status = Created
	case s.ExitCode >= 0:
		if s.ExitCode == 0 {
			status = Exited
		} else {
			status = ExitedErr
		}
	default:
		status = Created
	}

	return &CreateContainerResult{
		Name:   containerName,
		ID:     info.ID,
		Status: status,
	}, nil
}
