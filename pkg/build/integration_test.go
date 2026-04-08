//go:build integration

// Package build integration tests require a running kind cluster with NodeForge deployed.
// Run with: KUBECONFIG=~/.kube/config go test -v -tags=integration ./pkg/build/... -timeout 10m
package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"
)

const nodeforgeAddr = "localhost:50051" // assumes port-forward active

func TestBuildAndRegister_SimpleDockerfile(t *testing.T) {
	if os.Getenv("KUBECONFIG") == "" {
		t.Skip("KUBECONFIG not set — skipping integration test")
	}

	conn, err := grpc.NewClient(nodeforgeAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	client := nfv1.NewBuildServiceClient(conn)

	req := &nfv1.BuildRequest{
		RequestId:        fmt.Sprintf("inttest-%d", time.Now().UnixMilli()),
		ToolDefinitionId: "test-tool-001",
		ToolName:         "test-alpine-tool",
		ImageUri:         "docker.io/library/alpine:3.19",
		DockerfileContent: `FROM alpine:3.19 AS builder
RUN echo "hello nodeforge" > /hello.txt
`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	stream, err := client.BuildAndRegister(ctx, req)
	if err != nil {
		t.Fatalf("BuildAndRegister RPC: %v", err)
	}

	var finalDigest string
	var succeeded bool

	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream.Recv: %v", err)
		}

		t.Logf("[%s] %s", ev.Kind, ev.Message)

		switch ev.Kind {
		case nfv1.BuildEventKind_BUILD_EVENT_KIND_DIGEST_ACQUIRED:
			finalDigest = ev.Digest
		case nfv1.BuildEventKind_BUILD_EVENT_KIND_SUCCEEDED:
			succeeded = true
		case nfv1.BuildEventKind_BUILD_EVENT_KIND_FAILED:
			t.Fatalf("build failed: %s", ev.Message)
		}
	}

	if !succeeded {
		t.Fatal("build did not succeed")
	}
	if finalDigest == "" {
		t.Fatal("no digest acquired")
	}
	t.Logf("Gate passed: digest=%s", finalDigest)
}
