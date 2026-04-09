package build

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"
	"google.golang.org/grpc/metadata"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
)

// ─── fakeStream — minimal grpc.ServerStreamingServer[nfv1.BuildEvent] mock ───

type fakeStream struct {
	ctx    context.Context
	events []*nfv1.BuildEvent
}

func newFakeStream() *fakeStream { return &fakeStream{ctx: context.Background()} }

func (f *fakeStream) Send(ev *nfv1.BuildEvent) error {
	f.events = append(f.events, ev)
	return nil
}
func (f *fakeStream) Context() context.Context     { return f.ctx }
func (f *fakeStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeStream) SetTrailer(metadata.MD)       {}
func (f *fakeStream) SendMsg(any) error            { return nil }
func (f *fakeStream) RecvMsg(any) error            { return nil }

func (f *fakeStream) kindsSent() []nfv1.BuildEventKind {
	kinds := make([]nfv1.BuildEventKind, 0, len(f.events))
	for _, ev := range f.events {
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

// ─── helper: build a Job with given conditions ────────────────────────────────

func jobWithCondition(name, ns string, condType batchv1.JobConditionType) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: condType, Status: corev1.ConditionTrue},
			},
		},
	}
}

// ─── buildNamespace ───────────────────────────────────────────────────────────

func TestBuildNamespace_Default(t *testing.T) {
	t.Setenv("NODEFORGE_BUILD_NAMESPACE", "")
	if got := buildNamespace(); got != defaultBuildNamespace {
		t.Errorf("got %q, want %q", got, defaultBuildNamespace)
	}
}

func TestBuildNamespace_EnvOverride(t *testing.T) {
	t.Setenv("NODEFORGE_BUILD_NAMESPACE", "custom-builds")
	if got := buildNamespace(); got != "custom-builds" {
		t.Errorf("got %q, want %q", got, "custom-builds")
	}
}

// ─── registryAddr ─────────────────────────────────────────────────────────────

func TestRegistryAddr_Default(t *testing.T) {
	t.Setenv("NODEFORGE_REGISTRY_ADDR", "")
	if got := registryAddr(); got != defaultRegistryAddr {
		t.Errorf("got %q, want %q", got, defaultRegistryAddr)
	}
}

func TestRegistryAddr_EnvOverride(t *testing.T) {
	t.Setenv("NODEFORGE_REGISTRY_ADDR", "localhost:5000")
	if got := registryAddr(); got != "localhost:5000" {
		t.Errorf("got %q, want %q", got, "localhost:5000")
	}
}

// ─── sanitizeName ─────────────────────────────────────────────────────────────

func TestSanitizeName_LowercasesInput(t *testing.T) {
	if got := sanitizeName("BWA-MEM2"); got != "bwa-mem2" {
		t.Errorf("got %q, want %q", got, "bwa-mem2")
	}
}

func TestSanitizeName_ReplacesSpecialChars(t *testing.T) {
	got := sanitizeName("tool_v1.2@beta")
	// underscores, dots, @ become '-'
	if strings.ContainsAny(got, "_@.") {
		t.Errorf("special chars not replaced: %q", got)
	}
}

func TestSanitizeName_TrimsDashes(t *testing.T) {
	got := sanitizeName("---bwa---")
	if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
		t.Errorf("leading/trailing dashes not trimmed: %q", got)
	}
}

func TestSanitizeName_TruncatesAt50(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := sanitizeName(long)
	if len(got) > 50 {
		t.Errorf("length %d exceeds 50", len(got))
	}
}

func TestSanitizeName_PreservesAlphanumericAndDash(t *testing.T) {
	in := "bwa-0.7.17"
	got := sanitizeName(in)
	// dots become dashes; alphanumeric and dashes kept
	if !strings.Contains(got, "bwa") || !strings.Contains(got, "0") {
		t.Errorf("sanitizeName mangled valid chars: %q → %q", in, got)
	}
}

// ─── jobName ──────────────────────────────────────────────────────────────────

func TestJobName_TruncatesRequestIdAt8(t *testing.T) {
	req := &nfv1.BuildRequest{RequestId: "abcdefghijklmnop"}
	got := jobName(req)
	// "nfbuild-" + first 8 chars of sanitized id
	if !strings.HasPrefix(got, "nfbuild-") {
		t.Errorf("missing prefix: %q", got)
	}
	suffix := strings.TrimPrefix(got, "nfbuild-")
	if len(suffix) > 8 {
		t.Errorf("suffix longer than 8 chars: %q", suffix)
	}
}

func TestJobName_ShortRequestId(t *testing.T) {
	req := &nfv1.BuildRequest{RequestId: "abc"}
	got := jobName(req)
	if got != "nfbuild-abc" {
		t.Errorf("got %q, want %q", got, "nfbuild-abc")
	}
}

// ─── kanikoJob ────────────────────────────────────────────────────────────────

func TestKanikoJob_BasicFields(t *testing.T) {
	req := &nfv1.BuildRequest{RequestId: "req-001", ToolName: "bwa"}
	job := kanikoJob("build-001", "ctx-001", "registry/bwa:latest", req, "nodeforge-builds")

	if job.Name != "build-001" {
		t.Errorf("Name: got %q", job.Name)
	}
	if job.Namespace != "nodeforge-builds" {
		t.Errorf("Namespace: got %q", job.Namespace)
	}
	if *job.Spec.BackoffLimit != 0 {
		t.Errorf("BackoffLimit: got %d, want 0", *job.Spec.BackoffLimit)
	}
	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy: got %q", job.Spec.Template.Spec.RestartPolicy)
	}
}

func TestKanikoJob_KanikoImageAndArgs(t *testing.T) {
	req := &nfv1.BuildRequest{RequestId: "req-001", ToolName: "samtools"}
	destination := "10.96.0.1:5000/samtools:latest"
	job := kanikoJob("j", "ctx", destination, req, "nodeforge-builds")

	containers := job.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	if containers[0].Image != kanikoImage {
		t.Errorf("Image: got %q, want %q", containers[0].Image, kanikoImage)
	}

	args := containers[0].Args
	var hasDestination, hasDockerfile, hasContext bool
	for _, a := range args {
		if a == "--destination="+destination {
			hasDestination = true
		}
		if a == "--dockerfile=/workspace/Dockerfile" {
			hasDockerfile = true
		}
		if a == "--context=dir:///workspace" {
			hasContext = true
		}
	}
	if !hasDestination {
		t.Errorf("--destination arg missing in %v", args)
	}
	if !hasDockerfile {
		t.Errorf("--dockerfile arg missing in %v", args)
	}
	if !hasContext {
		t.Errorf("--context arg missing in %v", args)
	}
}

func TestKanikoJob_ConfigMapVolume(t *testing.T) {
	req := &nfv1.BuildRequest{RequestId: "r", ToolName: "t"}
	job := kanikoJob("j", "my-ctx-cm", "dest", req, "ns")

	volumes := job.Spec.Template.Spec.Volumes
	if len(volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(volumes))
	}
	if volumes[0].ConfigMap.Name != "my-ctx-cm" {
		t.Errorf("ConfigMap name: got %q, want %q", volumes[0].ConfigMap.Name, "my-ctx-cm")
	}

	mounts := job.Spec.Template.Spec.Containers[0].VolumeMounts
	if len(mounts) != 1 || mounts[0].MountPath != "/workspace" {
		t.Errorf("VolumeMount at /workspace missing: %+v", mounts)
	}
}

func TestKanikoJob_Labels(t *testing.T) {
	req := &nfv1.BuildRequest{RequestId: "req-abc", ToolName: "star"}
	job := kanikoJob("j", "cm", "dest", req, "ns")

	if job.Labels["app"] != "nodeforge-build" {
		t.Errorf("label app: got %q", job.Labels["app"])
	}
	if job.Labels["request-id"] != "req-abc" {
		t.Errorf("label request-id: got %q", job.Labels["request-id"])
	}
}

// ─── pushedDigestRe ───────────────────────────────────────────────────────────

func TestPushedDigestRe_MatchesKanikoLine(t *testing.T) {
	line := []byte("INFO Pushed registry.local/bwa:latest@sha256:deadbeef1234567890abcdef")
	m := pushedDigestRe.FindSubmatch(line)
	if len(m) < 2 {
		t.Fatal("regex did not match")
	}
	if string(m[1]) != "sha256:deadbeef1234567890abcdef" {
		t.Errorf("captured %q", m[1])
	}
}

func TestPushedDigestRe_NoMatchOnOtherLines(t *testing.T) {
	lines := []string{
		"INFO Unpacking rootfs",
		"INFO Building stage",
		"error: command not found",
	}
	for _, l := range lines {
		if m := pushedDigestRe.FindSubmatch([]byte(l)); len(m) >= 2 {
			t.Errorf("unexpected match on %q: %q", l, m[1])
		}
	}
}

// ─── extractDigestFromPodLogs ─────────────────────────────────────────────────

func TestExtractDigestFromPodLogs_NoPod(t *testing.T) {
	// Fake client with no pods → "pod not found" error.
	svc := &Service{kube: fake.NewSimpleClientset()}
	_, err := svc.extractDigestFromPodLogs(context.Background(), "job-xyz")
	if err == nil {
		t.Fatal("expected error when no pod exists")
	}
	if !strings.Contains(err.Error(), "pod not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ─── fetchDigest ──────────────────────────────────────────────────────────────

func TestFetchDigest_DockerContentDigestHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:abc123testdigest")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	host := strings.TrimPrefix(ts.URL, "http://")
	digest, err := fetchDigest(host + "/myimage:v1")
	if err != nil {
		t.Fatalf("fetchDigest: %v", err)
	}
	if digest != "sha256:abc123testdigest" {
		t.Errorf("digest: got %q, want %q", digest, "sha256:abc123testdigest")
	}
}

func TestFetchDigest_DigestFromJSONBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Docker-Content-Digest header → fall through to JSON body parse.
		fmt.Fprintln(w, `{"config":{"digest":"sha256:bodyjsondigest"}}`)
	}))
	defer ts.Close()

	host := strings.TrimPrefix(ts.URL, "http://")
	digest, err := fetchDigest(host + "/myimage:v1")
	if err != nil {
		t.Fatalf("fetchDigest: %v", err)
	}
	if digest != "sha256:bodyjsondigest" {
		t.Errorf("digest: got %q", digest)
	}
}

func TestFetchDigest_NoDigest_ReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{}`)
	}))
	defer ts.Close()

	host := strings.TrimPrefix(ts.URL, "http://")
	_, err := fetchDigest(host + "/myimage:v1")
	if err == nil {
		t.Fatal("expected error when no digest found")
	}
}

func TestFetchDigest_InvalidDestination(t *testing.T) {
	_, err := fetchDigest("nodestination")
	if err == nil {
		t.Fatal("expected error for destination with no '/'")
	}
}

func TestFetchDigest_NoTagInDestination(t *testing.T) {
	_, err := fetchDigest("host/imagewithoutcolontag")
	if err == nil {
		t.Fatal("expected error for destination with no ':'")
	}
}

// ─── waitForBuild ─────────────────────────────────────────────────────────────

func setupRegistryServer(t *testing.T, digest string) (host string, cleanup func()) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
	}))
	return strings.TrimPrefix(ts.URL, "http://"), ts.Close
}

func TestWaitForBuild_JobComplete_ReturnsDigest(t *testing.T) {
	host, cleanup := setupRegistryServer(t, "sha256:completedigest")
	defer cleanup()

	destination := host + "/bwa:latest"
	fw := watch.NewFake()

	svc := &Service{kube: fake.NewSimpleClientset()}
	stream := newFakeStream()

	go func() { fw.Modify(jobWithCondition("j", "nodeforge-builds", batchv1.JobComplete)) }()
	digest, err := svc.waitForBuild(context.Background(), stream, "j", fw, destination)
	if err != nil {
		t.Fatalf("waitForBuild: %v", err)
	}
	if digest != "sha256:completedigest" {
		t.Errorf("digest: got %q, want %q", digest, "sha256:completedigest")
	}
}

func TestWaitForBuild_JobFailed_ReturnsError(t *testing.T) {
	fw := watch.NewFake()

	svc := &Service{kube: fake.NewSimpleClientset()}
	stream := newFakeStream()

	go func() { fw.Modify(jobWithCondition("j", "nodeforge-builds", batchv1.JobFailed)) }()
	_, err := svc.waitForBuild(context.Background(), stream, "j", fw, "irrelevant/dest:tag")
	if err == nil {
		t.Fatal("expected error for failed job")
	}
	if !strings.Contains(err.Error(), "build job failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestWaitForBuild_WatchChannelClosed_ReturnsError(t *testing.T) {
	fw := watch.NewFake()
	fw.Stop() // close channel immediately

	svc := &Service{kube: fake.NewSimpleClientset()}
	stream := newFakeStream()

	_, err := svc.waitForBuild(context.Background(), stream, "j", fw, "dest/img:tag")
	if err == nil {
		t.Fatal("expected error when watch channel closes")
	}
	if !strings.Contains(err.Error(), "watch channel closed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWaitForBuild_ContextCancelled_ReturnsError(t *testing.T) {
	fw := watch.NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	svc := &Service{kube: fake.NewSimpleClientset()}
	stream := &fakeStream{ctx: ctx}

	_, err := svc.waitForBuild(ctx, stream, "j", fw, "dest/img:tag")
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}

func TestWaitForBuild_JobComplete_EmitsPushSucceededEvent(t *testing.T) {
	host, cleanup := setupRegistryServer(t, "sha256:any")
	defer cleanup()

	destination := host + "/tool:latest"
	fw := watch.NewFake()

	svc := &Service{kube: fake.NewSimpleClientset()}
	stream := newFakeStream()

	go func() { fw.Modify(jobWithCondition("j", "ns", batchv1.JobComplete)) }()
	if _, err := svc.waitForBuild(context.Background(), stream, "j", fw, destination); err != nil {
		t.Fatalf("waitForBuild: %v", err)
	}

	kinds := stream.kindsSent()
	found := false
	for _, k := range kinds {
		if k == nfv1.BuildEventKind_BUILD_EVENT_KIND_PUSH_SUCCEEDED {
			found = true
		}
	}
	if !found {
		t.Errorf("PUSH_SUCCEEDED event not emitted; got %v", kinds)
	}
}
