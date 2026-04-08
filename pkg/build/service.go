// Package build manages builder Job orchestration via kaniko.
// BuildService receives BuildRequests, creates K8s Jobs with kaniko,
// streams events back to the caller, and acquires the pushed image digest.
// After L2 succeeds it drives L3 (dry-run) → L4 (smoke run) → tool registration.
package build

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"google.golang.org/grpc"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"

	"github.com/HeaInSeo/NodeForge/pkg/catalog"
	"github.com/HeaInSeo/NodeForge/pkg/validate"
)

const (
	defaultBuildNamespace = "nodeforge-builds"
	defaultRegistryAddr   = "10.96.235.195:5000" // in-cluster registry ClusterIP
	kanikoImage           = "gcr.io/kaniko-project/executor:v1.23.2"
	jobTTLSeconds         = int32(300)
	jobDeadlineSeconds    = int64(600)
)

func buildNamespace() string {
	if v := os.Getenv("NODEFORGE_BUILD_NAMESPACE"); v != "" {
		return v
	}
	return defaultBuildNamespace
}

func registryAddr() string {
	if v := os.Getenv("NODEFORGE_REGISTRY_ADDR"); v != "" {
		return v
	}
	return defaultRegistryAddr
}

// Service implements BuildServiceServer.
type Service struct {
	nfv1.UnimplementedBuildServiceServer
	kube      kubernetes.Interface
	validator *validate.Service
	registry  *catalog.ToolRegistryService
}

// NewService creates a BuildService using local kubeconfig.
func NewService(validator *validate.Service, registry *catalog.ToolRegistryService) (*Service, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	restCfg, err := cfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kubeconfig: %w", err)
	}
	kube, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}

	// Ensure build namespace exists.
	ns := buildNamespace()
	ctx := context.Background()
	_, err = kube.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		if _, cerr := kube.CoreV1().Namespaces().Create(ctx, nsObj, metav1.CreateOptions{}); cerr != nil {
			return nil, fmt.Errorf("create namespace %s: %w", ns, cerr)
		}
	}

	return &Service{kube: kube, validator: validator, registry: registry}, nil
}

// BuildAndRegister implements BuildServiceServer.
// Full orchestration: L2 (kaniko build) → L3 (dry-run) → L4 (smoke run) → registration.
func (s *Service) BuildAndRegister(req *nfv1.BuildRequest, stream grpc.ServerStreamingServer[nfv1.BuildEvent]) error {
	ctx := stream.Context()
	jName := jobName(req)

	send := func(kind nfv1.BuildEventKind, msg string) error {
		return stream.Send(&nfv1.BuildEvent{
			Kind:      kind,
			Message:   msg,
			Timestamp: time.Now().UnixMilli(),
		})
	}

	bns := buildNamespace()
	destination := fmt.Sprintf("%s/%s:latest", registryAddr(), sanitizeName(req.ToolName))

	// ── L2: builder Job ──────────────────────────────────────────────────────

	cmName := jName + "-ctx"
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: bns},
		Data:       map[string]string{"Dockerfile": req.DockerfileContent},
	}
	if _, err := s.kube.CoreV1().ConfigMaps(bns).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create configmap: %w", err)
	}
	defer func() {
		_ = s.kube.CoreV1().ConfigMaps(bns).Delete(
			context.Background(), cmName, metav1.DeleteOptions{})
	}()

	job := kanikoJob(jName, cmName, destination, req, bns)
	if _, err := s.kube.BatchV1().Jobs(bns).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_JOB_CREATED, fmt.Sprintf("Job %s created", jName))
	slog.Info("kaniko job created", "job", jName, "destination", destination)

	watcher, err := s.kube.BatchV1().Jobs(bns).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + jName,
	})
	if err != nil {
		return fmt.Errorf("watch job: %w", err)
	}
	defer watcher.Stop()

	digest, err := s.waitForBuild(ctx, stream, jName, watcher, destination)
	if err != nil {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_FAILED, err.Error())
		return err
	}

	imageWithDigest := destination + "@" + digest
	_ = stream.Send(&nfv1.BuildEvent{
		Kind:      nfv1.BuildEventKind_BUILD_EVENT_KIND_DIGEST_ACQUIRED,
		Message:   imageWithDigest,
		Digest:    digest,
		Timestamp: time.Now().UnixMilli(),
	})

	// ── L3: dry-run ──────────────────────────────────────────────────────────

	smokeJob := validate.SmokeJobSpec("nfsmoke-"+sanitizeName(req.RequestId[:8]), imageWithDigest)
	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "L3: submitting dry-run...")

	dryResult := s.validator.DryRunJob(ctx, smokeJob)
	if !dryResult.Success {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_FAILED, "L3 dry-run failed: "+dryResult.ErrorMessage)
		return fmt.Errorf("L3 dry-run failed: %s", dryResult.ErrorMessage)
	}
	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "L3 dry-run passed")

	// ── L4: smoke run ────────────────────────────────────────────────────────

	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "L4: starting smoke run...")

	smokeResult := s.validator.SmokeRunJob(ctx, smokeJob)
	if !smokeResult.Success {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_FAILED, "L4 smoke run failed: "+smokeResult.ErrorMessage)
		return fmt.Errorf("L4 smoke run failed: %s", smokeResult.ErrorMessage)
	}
	if smokeResult.LogOutput != "" {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "smoke log: "+strings.TrimSpace(smokeResult.LogOutput))
	}
	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "L4 smoke run passed")

	// ── 등록 ─────────────────────────────────────────────────────────────────

	regResp, regErr := s.registry.RegisterTool(ctx, &nfv1.RegisterToolRequest{
		RequestId:        req.RequestId,
		ToolDefinitionId: req.ToolDefinitionId,
		ToolName:         req.ToolName,
		ImageUri:         destination,
		Digest:           digest,
		InputNames:       req.InputNames,
		OutputNames:      req.OutputNames,
	})
	if regErr != nil {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "registration warning: "+regErr.Error())
	} else {
		_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG, "tool registered: cas="+regResp.CasHash)
	}

	_ = send(nfv1.BuildEventKind_BUILD_EVENT_KIND_SUCCEEDED,
		fmt.Sprintf("build+register complete: %s@%s", destination, digest))
	return nil
}

// waitForBuild watches the kaniko Job until Succeeded or Failed, streaming logs.
// Returns the image digest on success.
func (s *Service) waitForBuild(
	ctx context.Context,
	stream grpc.ServerStreamingServer[nfv1.BuildEvent],
	jName string,
	watcher watch.Interface,
	destination string,
) (string, error) {
	send := func(kind nfv1.BuildEventKind, msg string) {
		_ = stream.Send(&nfv1.BuildEvent{Kind: kind, Message: msg, Timestamp: time.Now().UnixMilli()})
	}

	running := false
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case ev, ok := <-watcher.ResultChan():
			if !ok {
				return "", fmt.Errorf("watch channel closed before job completion")
			}
			j, ok := ev.Object.(*batchv1.Job)
			if !ok {
				continue
			}
			if ev.Type == watch.Deleted {
				return "", fmt.Errorf("job deleted unexpectedly")
			}
			if !running && j.Status.Active > 0 {
				running = true
				send(nfv1.BuildEventKind_BUILD_EVENT_KIND_JOB_RUNNING, "kaniko build running")
				go s.streamPodLogs(ctx, jName, stream)
			}
			for _, cond := range j.Status.Conditions {
				if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
					return "", fmt.Errorf("build job failed: %s", cond.Message)
				}
				if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
					send(nfv1.BuildEventKind_BUILD_EVENT_KIND_PUSH_SUCCEEDED, "image pushed to "+destination)
					digest, derr := fetchDigest(destination)
					if derr != nil {
						slog.Warn("digest fetch failed", "err", derr)
						digest = "unknown"
					}
					return digest, nil
				}
			}
		}
	}
}

// streamPodLogs tails kaniko pod logs and sends them as LOG events.
func (s *Service) streamPodLogs(ctx context.Context, jName string, stream grpc.ServerStreamingServer[nfv1.BuildEvent]) {
	time.Sleep(2 * time.Second)

	bns := buildNamespace()
	pods, err := s.kube.CoreV1().Pods(bns).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jName,
	})
	if err != nil || len(pods.Items) == 0 {
		return
	}
	podName := pods.Items[0].Name

	rc, err := s.kube.CoreV1().Pods(bns).GetLogs(podName, &corev1.PodLogOptions{
		Follow:    true,
		Container: "kaniko",
	}).Stream(ctx)
	if err != nil {
		return
	}
	defer rc.Close()

	buf := make([]byte, 4096)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			line := strings.TrimRight(string(buf[:n]), "\n")
			_ = stream.Send(&nfv1.BuildEvent{
				Kind:      nfv1.BuildEventKind_BUILD_EVENT_KIND_LOG,
				Message:   line,
				Timestamp: time.Now().UnixMilli(),
			})
		}
		if err == io.EOF || err != nil {
			return
		}
	}
}

// kanikoJob returns a Job spec that builds a Dockerfile using kaniko.
func kanikoJob(name, cmName, destination string, req *nfv1.BuildRequest, namespace string) *batchv1.Job {
	ttl := jobTTLSeconds
	deadline := jobDeadlineSeconds
	backoff := int32(0)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":        "nodeforge-build",
				"request-id": req.RequestId,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &deadline,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Volumes: []corev1.Volume{
						{
							Name: "dockerfile-ctx",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "kaniko",
							Image: kanikoImage,
							Args: []string{
								"--dockerfile=/workspace/Dockerfile",
								"--context=dir:///workspace",
								"--destination=" + destination,
								"--insecure",
								"--skip-tls-verify",
								"--cache=false",
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "dockerfile-ctx",
									MountPath: "/workspace",
								},
							},
						},
					},
				},
			},
		},
	}
}

// fetchDigest queries the registry manifest API for the image digest.
func fetchDigest(destination string) (string, error) {
	parts := strings.SplitN(destination, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid destination: %s", destination)
	}
	host := parts[0]
	nameTagParts := strings.SplitN(parts[1], ":", 2)
	if len(nameTagParts) != 2 {
		return "", fmt.Errorf("invalid name:tag in: %s", destination)
	}
	name, tag := nameTagParts[0], nameTagParts[1]

	accepts := []string{
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.oci.image.index.v1+json",
	}

	url := fmt.Sprintf("http://%s/v2/%s/manifests/%s", host, name, tag)
	for _, accept := range accepts {
		httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		if err != nil {
			return "", err
		}
		httpReq.Header.Set("Accept", accept)

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close() //nolint:gocritic

		if digest := resp.Header.Get("Docker-Content-Digest"); digest != "" {
			return digest, nil
		}

		var m struct {
			Config struct {
				Digest string `json:"digest"`
			} `json:"config"`
		}
		if jerr := json.NewDecoder(resp.Body).Decode(&m); jerr == nil && m.Config.Digest != "" {
			return m.Config.Digest, nil
		}
	}

	return "", fmt.Errorf("digest not found in registry response for %s", destination)
}

// jobName returns a deterministic Job name from the request ID.
func jobName(req *nfv1.BuildRequest) string {
	id := req.RequestId
	if len(id) > 8 {
		id = id[:8]
	}
	return "nfbuild-" + sanitizeName(id)
}

// sanitizeName makes a string safe for use as a Kubernetes label/name component.
func sanitizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 50 {
		result = result[:50]
	}
	return result
}
