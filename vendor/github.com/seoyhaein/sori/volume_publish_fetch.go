package sori

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/seoyhaein/sori/archiveutil"
	"github.com/seoyhaein/sori/registryutil"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
)

// Deprecated: prefer Client.PackageVolume, Client.PackageVolumeWithOptions, or
// PackageVolumeToStore so new code stays on the preferred client-based core
// path.
func (vi *VolumeIndex) PublishVolume(ctx context.Context, volPath, volName string, configBlob []byte) (*VolumeIndex, error) {
	return NewClient().PublishVolume(ctx, vi, volPath, volName, configBlob)
}

func (vi *VolumeIndex) publishVolumeToStore(ctx context.Context, storePath, volPath, volName string, configBlob []byte) (*VolumeIndex, error) {
	store, err := oci.New(storePath)
	if err != nil {
		return nil, transportError("VolumeIndex.publishVolumeToStore", "init OCI store", err)
	}

	anyPushed := false
	pushIfNeeded := func(desc ocispec.Descriptor, r io.Reader) (*bool, error) {
		exists, err := store.Exists(ctx, desc)
		if err != nil {
			return nil, transportError("VolumeIndex.publishVolumeToStore", fmt.Sprintf("check exists %s", desc.Digest), err)
		}
		if exists {
			Log.Infof("blob %s already exists, skipping", desc.Digest)
			skipped := false
			return &skipped, nil
		}
		if err := store.Push(ctx, desc, r); err != nil {
			return nil, transportError("VolumeIndex.publishVolumeToStore", fmt.Sprintf("push blob %s", desc.Digest), err)
		}
		pushed := true
		return &pushed, nil
	}

	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(configBlob),
		Size:      int64(len(configBlob)),
	}
	pushedPtr, err := pushIfNeeded(configDesc, bytes.NewReader(configBlob))
	if err != nil {
		return nil, err
	}
	if pushedPtr != nil && *pushedPtr {
		anyPushed = true
	}

	rootBase := filepath.Base(volPath)
	layers := make([]ocispec.Descriptor, 0, len(vi.Partitions))

	if len(vi.Partitions) == 0 {
		layerData, err := archiveutil.TarGzDir(volPath, rootBase)
		if err != nil {
			return nil, transportError("VolumeIndex.publishVolumeToStore", fmt.Sprintf("tar.gz fallback %q", volPath), err)
		}
		desc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageLayerGzip,
			Digest:    digest.FromBytes(layerData),
			Size:      int64(len(layerData)),
			Annotations: map[string]string{
				"org.example.partitionPath": rootBase,
			},
		}
		pushedPtr, err := pushIfNeeded(desc, bytes.NewReader(layerData))
		if err != nil {
			return nil, transportError("VolumeIndex.publishVolumeToStore", "push fallback layer", err)
		}
		if pushedPtr != nil && *pushedPtr {
			anyPushed = true
		}
		layers = append(layers, desc)
	} else {
		for i := range vi.Partitions {
			part := &vi.Partitions[i]
			fsPath := filepath.Join(volPath, strings.TrimPrefix(part.Path, rootBase+"/"))
			layerData, err := archiveutil.TarGzDir(fsPath, part.Path)
			if err != nil {
				return nil, transportError("VolumeIndex.publishVolumeToStore", fmt.Sprintf("tar.gz %q", fsPath), err)
			}
			desc := ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageLayerGzip,
				Digest:    digest.FromBytes(layerData),
				Size:      int64(len(layerData)),
				Annotations: map[string]string{
					"org.example.partitionPath": part.Path,
				},
			}
			pushedPtr, err := pushIfNeeded(desc, bytes.NewReader(layerData))
			if err != nil {
				return nil, transportError("VolumeIndex.publishVolumeToStore", fmt.Sprintf("push layer %s", part.Name), err)
			}
			if pushedPtr != nil && *pushedPtr {
				anyPushed = true
			}
			part.ManifestRef = desc.Digest.String()
			layers = append(layers, desc)
		}
	}

	if !anyPushed {
		existingDesc, err := store.Resolve(ctx, volName)
		if err == nil {
			Log.Infof("No changes detected (config+layers), skipping manifest update for %q", volName)
			vi.VolumeRef = existingDesc.Digest.String()
			return vi, nil
		}
	}

	manifestDesc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1,
		ocispec.MediaTypeImageManifest,
		oras.PackManifestOptions{
			ConfigDescriptor: &configDesc,
			Layers:           layers,
			ManifestAnnotations: map[string]string{
				ocispec.AnnotationCreated: time.Now().UTC().Format(time.RFC3339),
			},
		},
	)
	if err != nil {
		return nil, transportError("VolumeIndex.publishVolumeToStore", "pack manifest", err)
	}
	if err := store.Tag(ctx, manifestDesc, volName); err != nil {
		return nil, transportError("VolumeIndex.publishVolumeToStore", fmt.Sprintf("tag manifest %q", volName), err)
	}
	vi.VolumeRef = manifestDesc.Digest.String()

	Log.Infof("Volume artifact %s tagged as %s", volName, manifestDesc.Digest)
	return vi, nil
}

// Deprecated: prefer Client.PushPackagedVolume or PushPackagedVolume so new
// code stays on the preferred core push path.
func PushLocalToRemote(ctx context.Context, localRepoPath, tag, remoteRepo, user, pass string, plainHTTP bool) (*PushResult, error) {
	repo, err := registryutil.NewRepository(remoteRepo, registryutil.RemoteConfig{
		PlainHTTP: plainHTTP,
		Username:  user,
		Password:  pass,
	})
	if err != nil {
		return nil, err
	}
	return pushLocalTagToRepository(ctx, localRepoPath, tag, repo)
}

func pushLocalTagToRepository(ctx context.Context, localRepoPath, tag string, repo *remote.Repository) (*PushResult, error) {
	srcStore, err := oci.New(localRepoPath)
	if err != nil {
		return nil, transportError("pushLocalTagToRepository", "init local OCI store", err)
	}
	pushedDesc, err := oras.Copy(ctx, srcStore, tag, repo, tag, oras.DefaultCopyOptions)
	if err != nil {
		return nil, transportError("pushLocalTagToRepository", "push to remote registry", err)
	}

	ref := fmt.Sprintf("%s:%s", repo.Reference.String(), tag)
	Log.Infof("Pushed to remote: %s -> %s (%s)", tag, ref, pushedDesc.Digest)
	return &PushResult{
		Reference:      ref,
		Repository:     repo.Reference.String(),
		Tag:            tag,
		ManifestDigest: pushedDesc.Digest.String(),
	}, nil
}

func FetchVolSeq(ctx context.Context, destRoot, repo, tag string) (*VolumeIndex, error) {
	store, err := oci.New(repo)
	if err != nil {
		return nil, transportError("FetchVolSeq", "open OCI store", err)
	}

	ref := fmt.Sprintf("%s:%s", repo, tag)
	manifestDesc, err := store.Resolve(ctx, tag)
	if err != nil {
		return nil, notFoundError("FetchVolSeq", fmt.Sprintf("resolve reference %q", ref), err)
	}

	rc, err := store.Fetch(ctx, manifestDesc)
	if err != nil {
		return nil, transportError("FetchVolSeq", "fetch manifest", err)
	}
	defer rc.Close()

	var manifest ocispec.Manifest
	if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
		return nil, integrityError("FetchVolSeq", "decode manifest", err)
	}

	vi := &VolumeIndex{
		VolumeRef:  manifestDesc.Digest.String(),
		Partitions: make([]Partition, len(manifest.Layers)),
	}
	seen := make(map[string]struct{})

	for i, layerDesc := range manifest.Layers {
		layerRC, err := store.Fetch(ctx, layerDesc)
		if err != nil {
			return nil, transportError("FetchVolSeq", fmt.Sprintf("fetch layer %s", layerDesc.Digest), err)
		}
		partPath := layerDesc.Annotations["org.example.partitionPath"]
		if partPath == "" {
			layerRC.Close()
			return nil, integrityError("FetchVolSeq", fmt.Sprintf("missing partitionPath annotation for layer %s", layerDesc.Digest), nil)
		}
		if _, dup := seen[partPath]; dup {
			layerRC.Close()
			return nil, conflictError("FetchVolSeq", fmt.Sprintf("duplicate partition path %q", partPath), nil)
		}
		seen[partPath] = struct{}{}

		if err := os.MkdirAll(destRoot, 0o755); err != nil {
			layerRC.Close()
			return nil, transportError("FetchVolSeq", fmt.Sprintf("create destination root %s", destRoot), err)
		}
		if err := archiveutil.UntarGzDir(layerRC, destRoot); err != nil {
			layerRC.Close()
			return nil, integrityError("FetchVolSeq", fmt.Sprintf("extract layer %s", layerDesc.Digest), err)
		}
		if err := layerRC.Close(); err != nil {
			return nil, transportError("FetchVolSeq", fmt.Sprintf("close layer reader %s", layerDesc.Digest), err)
		}
		vi.Partitions[i] = Partition{Name: partPath, Path: partPath, ManifestRef: layerDesc.Digest.String()}
	}

	if err := writeVolumeIndex(destRoot, vi); err != nil {
		return nil, err
	}
	return vi, nil
}

func FetchVolParallel(ctx context.Context, destRoot, repo, tag string, concurrency int) (*VolumeIndex, error) {
	store, err := oci.New(repo)
	if err != nil {
		return nil, transportError("FetchVolParallel", "open OCI store", err)
	}

	manifestDesc, err := store.Resolve(ctx, tag)
	if err != nil {
		return nil, notFoundError("FetchVolParallel", fmt.Sprintf("resolve reference %s:%s", repo, tag), err)
	}

	rc, err := store.Fetch(ctx, manifestDesc)
	if err != nil {
		return nil, transportError("FetchVolParallel", "fetch manifest", err)
	}
	defer rc.Close()

	var manifest ocispec.Manifest
	if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
		return nil, integrityError("FetchVolParallel", "decode manifest", err)
	}

	n := len(manifest.Layers)
	vi := &VolumeIndex{
		VolumeRef:  manifestDesc.Digest.String(),
		Partitions: make([]Partition, n),
	}

	seen := make(map[string]struct{}, n)
	type layerMeta struct {
		idx  int
		desc ocispec.Descriptor
		path string
	}
	metas := make([]layerMeta, 0, n)

	for i, layer := range manifest.Layers {
		partPath := layer.Annotations["org.example.partitionPath"]
		if partPath == "" {
			return nil, integrityError("FetchVolParallel", fmt.Sprintf("missing partitionPath annotation for layer %s", layer.Digest), nil)
		}
		if _, dup := seen[partPath]; dup {
			return nil, conflictError("FetchVolParallel", fmt.Sprintf("duplicate partition path %q", partPath), nil)
		}
		seen[partPath] = struct{}{}
		metas = append(metas, layerMeta{i, layer, partPath})
	}

	if concurrency <= 0 || concurrency > n {
		cpu := runtime.NumCPU()
		if cpu < 1 {
			cpu = 1
		}
		if cpu > n {
			cpu = n
		}
		concurrency = cpu
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type jobResult struct {
		idx int
		p   Partition
		err error
	}

	jobs := make(chan layerMeta)
	results := make(chan jobResult, concurrency)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for meta := range jobs {
			select {
			case <-ctx.Done():
				return
			default:
			}

			layerRC, err := store.Fetch(ctx, meta.desc)
			if err != nil {
				results <- jobResult{idx: meta.idx, err: transportError("FetchVolParallel", fmt.Sprintf("fetch layer %s", meta.desc.Digest), err)}
				cancel()
				continue
			}
			if err := os.MkdirAll(destRoot, 0o755); err != nil {
				layerRC.Close()
				results <- jobResult{idx: meta.idx, err: transportError("FetchVolParallel", fmt.Sprintf("mkdir %s", destRoot), err)}
				cancel()
				continue
			}
			if err := archiveutil.UntarGzDir(layerRC, destRoot); err != nil {
				layerRC.Close()
				results <- jobResult{idx: meta.idx, err: integrityError("FetchVolParallel", fmt.Sprintf("extract layer %s", meta.desc.Digest), err)}
				cancel()
				continue
			}
			if err := layerRC.Close(); err != nil {
				results <- jobResult{idx: meta.idx, err: transportError("FetchVolParallel", fmt.Sprintf("close reader %s", meta.desc.Digest), err)}
				cancel()
				continue
			}

			results <- jobResult{
				idx: meta.idx,
				p:   Partition{Name: meta.path, Path: meta.path, ManifestRef: meta.desc.Digest.String()},
			}
		}
	}

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go worker()
	}

	go func() {
		defer close(jobs)
		for _, m := range metas {
			select {
			case <-ctx.Done():
				return
			case jobs <- m:
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var firstErr error
	for r := range results {
		if r.err != nil && firstErr == nil {
			firstErr = r.err
			cancel()
		}
		if r.err == nil {
			vi.Partitions[r.idx] = r.p
		}
	}

	if firstErr != nil {
		return nil, firstErr
	}
	if err := writeVolumeIndex(destRoot, vi); err != nil {
		return nil, err
	}
	return vi, nil
}

func pushDataSpecManifest(ctx context.Context, target content.Pusher, subjectDesc ocispec.Descriptor, spec *DataSpec) (*ReferrerPushResult, error) {
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, transportError("pushDataSpecManifest", "marshal data spec", err)
	}

	configDesc, err := oras.PushBytes(ctx, target, DataSpecMediaType, specBytes)
	if err != nil {
		return nil, transportError("pushDataSpecManifest", "push data spec blob", err)
	}

	manifestDesc, err := oras.PackManifest(ctx, target, oras.PackManifestVersion1_1,
		ocispec.MediaTypeImageManifest,
		oras.PackManifestOptions{
			Subject:          &subjectDesc,
			ConfigDescriptor: &configDesc,
			ManifestAnnotations: map[string]string{
				ocispec.AnnotationCreated: time.Now().UTC().Format(time.RFC3339),
			},
		},
	)
	if err != nil {
		return nil, transportError("pushDataSpecManifest", "pack data spec manifest", err)
	}

	return &ReferrerPushResult{
		SubjectDigest:  subjectDesc.Digest.String(),
		ManifestDigest: manifestDesc.Digest.String(),
		ConfigDigest:   configDesc.Digest.String(),
		ArtifactType:   DataSpecMediaType,
	}, nil
}
