package podbridge5

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/containers/image/v5/manifest"
)

const (
	ContainerAppDir             = "/app"
	ContainerScriptsDir         = "/app/scripts"
	ContainerExecutorPath       = "/app/executor.sh"
	ContainerInstallPath        = "/app/install.sh"
	ContainerHealthcheckPath    = "/app/healthcheck.sh"
	ContainerUserScriptPath     = "/app/scripts/user_script.sh"
	ContainerResultLogPath      = "/app/result.log"
	ContainerExitCodeLogPath    = "/app/exit_code.log"
	ContainerDefaultWorkDir     = "/app"
	ContainerDefaultHealthcheck = "CMD-SHELL /app/healthcheck.sh"
)

func DefaultExecutorCommand() []string {
	return []string{"/bin/sh", "-c", ContainerExecutorPath}
}

func DefaultHealthcheckCommand() string {
	return ContainerDefaultHealthcheck
}

func ParseHealthcheckConfig(inCmd, interval string, retries uint, timeout, startPeriod string) (*manifest.Schema2HealthConfig, error) {
	cmdArr := strings.Fields(strings.TrimSpace(inCmd))
	if len(cmdArr) < 2 || cmdArr[0] != "CMD-SHELL" {
		return nil, errors.New("invalid command format: must start with CMD-SHELL")
	}

	hc := manifest.Schema2HealthConfig{Test: cmdArr}

	if interval == "disable" {
		interval = "0"
	}
	intervalDuration, err := time.ParseDuration(interval)
	if err != nil {
		return nil, fmt.Errorf("invalid healthcheck-interval: %w", err)
	}
	hc.Interval = intervalDuration

	if retries < 1 {
		return nil, errors.New("healthcheck-retries must be greater than 0")
	}
	hc.Retries = int(retries)

	timeoutDuration, err := time.ParseDuration(timeout)
	if err != nil {
		return nil, fmt.Errorf("invalid healthcheck-timeout: %w", err)
	}
	if timeoutDuration < time.Second {
		return nil, errors.New("healthcheck-timeout must be at least 1 second")
	}
	hc.Timeout = timeoutDuration

	startPeriodDuration, err := time.ParseDuration(startPeriod)
	if err != nil {
		return nil, fmt.Errorf("invalid healthcheck-start-period: %w", err)
	}
	if startPeriodDuration < 0 {
		return nil, errors.New("healthcheck-start-period must be 0 seconds or greater")
	}
	hc.StartPeriod = startPeriodDuration

	return &hc, nil
}
