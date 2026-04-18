package podbridge5

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrRuntimeConnectionUnavailable = errors.New("podbridge5 runtime connection unavailable")
	ErrRuntimeStoreUnavailable      = errors.New("podbridge5 runtime store unavailable")
	ErrRuntimeNotInitialized        = errors.New("podbridge5 runtime not initialized")
)

type runtimeConnectionTarget struct {
	URI        string
	Source     string
	RuntimeDir string
}

func resolveRuntimeConnectionTarget(uid int, containerHost, xdgRuntimeDir string, rootless bool) runtimeConnectionTarget {
	containerHost = strings.TrimSpace(containerHost)
	if containerHost != "" {
		return runtimeConnectionTarget{
			URI:    containerHost,
			Source: "CONTAINER_HOST",
		}
	}

	runtimeDir := strings.TrimSpace(xdgRuntimeDir)
	source := "XDG_RUNTIME_DIR"
	if runtimeDir == "" {
		if rootless {
			runtimeDir = fmt.Sprintf("/run/user/%d", uid)
			source = "rootless-default"
		} else {
			runtimeDir = "/run"
			source = "root-default"
		}
	}

	return runtimeConnectionTarget{
		URI:        "unix:" + runtimeDir + "/podman/podman.sock",
		Source:     source,
		RuntimeDir: runtimeDir,
	}
}

func wrapRuntimeConnectionError(target runtimeConnectionTarget, err error) error {
	return errors.Join(
		ErrRuntimeConnectionUnavailable,
		fmt.Errorf("connect %s via %s: %w", target.URI, target.Source, err),
	)
}

func wrapRuntimeStoreError(err error) error {
	return errors.Join(
		ErrRuntimeStoreUnavailable,
		fmt.Errorf("create store: %w", err),
	)
}
