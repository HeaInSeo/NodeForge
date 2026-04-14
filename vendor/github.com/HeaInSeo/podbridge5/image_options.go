package podbridge5

import (
	"fmt"
	"io"
	"strings"

	"github.com/containers/buildah"
	"github.com/containers/buildah/define"
	imageTypes "github.com/containers/image/v5/types"
)

func DefaultImageBuildOptions(outputRef string) define.BuildOptions {
	return define.BuildOptions{
		ContextDirectory: ".",
		PullPolicy:       define.PullIfMissing,
		Isolation:        define.IsolationOCI,
		SystemContext:    &imageTypes.SystemContext{},
		Output:           outputRef,
		OutputFormat:     buildah.Dockerv2ImageManifest,
	}
}

func NormalizePushDestination(destination string) (string, error) {
	trimmed := strings.TrimSpace(destination)
	if trimmed == "" {
		return "", fmt.Errorf("destination must not be empty")
	}
	if strings.Contains(trimmed, "://") {
		return trimmed, nil
	}
	return "docker://" + trimmed, nil
}

func newBuilderOptions(baseImage string, caps []string) (*buildah.BuilderOptions, error) {
	if strings.TrimSpace(baseImage) == "" {
		return nil, fmt.Errorf("from image cannot be empty")
	}

	return &buildah.BuilderOptions{
		FromImage:        baseImage,
		Isolation:        define.IsolationOCI,
		CommonBuildOpts:  &buildah.CommonBuildOptions{},
		SystemContext:    &imageTypes.SystemContext{},
		ConfigureNetwork: buildah.NetworkDefault,
		Format:           buildah.Dockerv2ImageManifest,
		Capabilities:     append([]string(nil), caps...),
	}, nil
}

func newDefaultAddAndCopyOptions(hasher io.Writer) buildah.AddAndCopyOptions {
	return NewAddAndCopyOptions(
		WithChmod("0o755"),
		WithChown("0:0"),
		WithHasher(hasher),
		WithContextDir("."),
		WithDryRun(false),
	)
}
