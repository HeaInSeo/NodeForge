package oras

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	orasoras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

const (
	// MediaTypeToolSpec is the OCI media type for a NodeVault tool spec referrer.
	MediaTypeToolSpec = "application/vnd.nodevault.toolspec.v1+json"

	// MediaTypeDataSpec is the OCI media type for a NodeVault data spec referrer.
	MediaTypeDataSpec = "application/vnd.nodevault.dataspec.v1+json"
)

// Target is an oras-go content store that can accept pushes.
// Satisfied by *oci.Store (local) and *remote.Repository (Harbor).
type Target interface {
	orasoras.Target
}

// PushResult is returned by a successful referrer push.
type PushResult struct {
	// ReferrerDigest is the digest of the pushed referrer manifest.
	ReferrerDigest string
	// SubjectDigest is the subject image digest the referrer is attached to.
	SubjectDigest string
	// MediaType is the artifact media type used for the referrer config.
	MediaType string
}

// PushToolSpecReferrer attaches specJSON as an OCI referrer artifact linked
// to subjectDigest in the given target store.
// mediaType of the config blob is MediaTypeToolSpec.
//
// Both oci.Store (local testing) and remote.Repository (Harbor) satisfy Target.
func PushToolSpecReferrer(ctx context.Context, target Target, subjectDigest string, specJSON []byte) (PushResult, error) {
	return pushReferrer(ctx, target, subjectDigest, specJSON, MediaTypeToolSpec)
}

// PushDataSpecReferrer attaches specJSON as an OCI referrer artifact linked
// to subjectDigest in the given target store.
// mediaType of the config blob is MediaTypeDataSpec.
func PushDataSpecReferrer(ctx context.Context, target Target, subjectDigest string, specJSON []byte) (PushResult, error) {
	return pushReferrer(ctx, target, subjectDigest, specJSON, MediaTypeDataSpec)
}

// NewLocalStore opens (or creates) an OCI layout store at the given path.
// Useful for local testing without a running registry.
func NewLocalStore(path string) (Target, error) {
	return oci.New(path)
}

// NewRemoteRepository creates a remote.Repository pointed at repoRef.
// If plainHTTP is true, HTTP is used instead of HTTPS.
// credential may be nil for anonymous access.
func NewRemoteRepository(repoRef string, plainHTTP bool, credential *auth.Credential) (Target, error) {
	repo, err := remote.NewRepository(repoRef)
	if err != nil {
		return nil, fmt.Errorf("oras: new repository %q: %w", repoRef, err)
	}
	repo.PlainHTTP = plainHTTP
	if credential != nil {
		repo.Client = &auth.Client{
			Client:     retry.DefaultClient,
			Cache:      auth.NewCache(),
			Credential: auth.StaticCredential(repo.Reference.Registry, *credential),
		}
	}
	return repo, nil
}

// ── internal ──────────────────────────────────────────────────────────────────

func pushReferrer(
	ctx context.Context,
	target Target,
	subjectDigest string,
	specJSON []byte,
	mediaType string,
) (PushResult, error) {
	if subjectDigest == "" {
		return PushResult{}, fmt.Errorf("oras: subjectDigest must not be empty")
	}
	if len(specJSON) == 0 {
		return PushResult{}, fmt.Errorf("oras: specJSON must not be empty")
	}

	// 1. Config blob = specJSON
	configDesc := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    godigest.FromBytes(specJSON),
		Size:      int64(len(specJSON)),
	}
	if err := pushBlobIfNeeded(ctx, target, configDesc, specJSON); err != nil {
		return PushResult{}, fmt.Errorf("oras: push config blob: %w", err)
	}

	// 2. Empty layers array (referrer artifact carries payload in config)
	layers := []ocispec.Descriptor{}

	// 3. Build the referrer manifest (subject points to the tool image)
	subjectDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    godigest.Digest(subjectDigest),
	}
	manifestDesc, err := orasoras.PackManifest(
		ctx, target,
		orasoras.PackManifestVersion1_1,
		ocispec.MediaTypeImageManifest,
		orasoras.PackManifestOptions{
			Subject:          &subjectDesc,
			ConfigDescriptor: &configDesc,
			Layers:           layers,
		},
	)
	if err != nil {
		return PushResult{}, fmt.Errorf("oras: pack referrer manifest: %w", err)
	}

	return PushResult{
		ReferrerDigest: manifestDesc.Digest.String(),
		SubjectDigest:  subjectDigest,
		MediaType:      mediaType,
	}, nil
}

// pushBlobIfNeeded pushes data to target; "already exists" errors are silently ignored.
// Target always satisfies content.Pusher (via orasoras.Target), so no type assertion is needed.
func pushBlobIfNeeded(ctx context.Context, target Target, desc ocispec.Descriptor, data []byte) error {
	if err := target.Push(ctx, desc, bytes.NewReader(data)); err != nil {
		if !isAlreadyExists(err) {
			return err
		}
	}
	return nil
}

// isAlreadyExists reports whether err indicates a blob already exists in the target.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// oras-go and OCI registries use various "already exists" messages.
	for _, needle := range []string{"already exists", "conflict", "409"} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// MarshalSpec marshals v to JSON for use as specJSON in Push* functions.
func MarshalSpec(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("oras: marshal spec: %w", err)
	}
	return data, nil
}
