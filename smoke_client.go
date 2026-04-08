// smoke_client.go — NodeKit ↔ NodeForge gRPC 통신 검증
// 실행: go run smoke_client.go
// 사전 조건: NodeForge가 localhost:50051에서 실행 중이어야 한다.

//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	nfv1 "github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1"
)

func main() {
	conn, err := grpc.NewClient("localhost:50051",
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pass := true

	// ── 1. Ping ───────────────────────────────────────────────────────────────
	pingClient := nfv1.NewPingServiceClient(conn)
	pingResp, err := pingClient.Ping(ctx, &nfv1.PingRequest{Message: "hello"})
	if err != nil {
		fmt.Printf("[FAIL] Ping: %v\n", err)
		pass = false
	} else {
		fmt.Printf("[PASS] Ping → message=%q serverId=%q\n", pingResp.Message, pingResp.ServerId)
	}

	// ── 2. ListPolicies ───────────────────────────────────────────────────────
	policyClient := nfv1.NewPolicyServiceClient(conn)
	listResp, err := policyClient.ListPolicies(ctx, &nfv1.ListPoliciesRequest{})
	if err != nil {
		fmt.Printf("[FAIL] ListPolicies: %v\n", err)
		pass = false
	} else {
		fmt.Printf("[PASS] ListPolicies → bundleVersion=%q policies=%d\n",
			listResp.BundleVersion, len(listResp.Policies))
		for _, p := range listResp.Policies {
			fmt.Printf("       rule=%s name=%q\n", p.RuleId, p.Name)
		}
	}

	// ── 3. GetPolicyBundle ────────────────────────────────────────────────────
	bundleResp, err := policyClient.GetPolicyBundle(ctx, &nfv1.GetPolicyBundleRequest{})
	if err != nil {
		fmt.Printf("[FAIL] GetPolicyBundle: %v\n", err)
		pass = false
	} else {
		fmt.Printf("[PASS] GetPolicyBundle → version=%q wasmBytes=%d builtAt=%d\n",
			bundleResp.Version, len(bundleResp.WasmBytes), bundleResp.BuiltAt)
		if len(bundleResp.WasmBytes) == 0 {
			fmt.Println("[WARN] wasmBytes is empty!")
			pass = false
		}
	}

	// ── 4. ListTools (빈 카탈로그) ───────────────────────────────────────────
	registryClient := nfv1.NewToolRegistryServiceClient(conn)
	toolsResp, err := registryClient.ListTools(ctx, &nfv1.ListToolsRequest{})
	if err != nil {
		fmt.Printf("[FAIL] ListTools: %v\n", err)
		pass = false
	} else {
		fmt.Printf("[PASS] ListTools → count=%d\n", len(toolsResp.Tools))
	}

	// ── 결과 ──────────────────────────────────────────────────────────────────
	fmt.Println()
	if pass {
		fmt.Println("=== SMOKE: ALL PASS ===")
	} else {
		fmt.Println("=== SMOKE: FAILED ===")
		log.Fatal("smoke test failed")
	}
}
