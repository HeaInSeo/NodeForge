.PHONY: fmt lint lint-fix test test-integration test-integration-multipass \
        deploy-multipass undeploy-multipass build proto clean

GOLANGCI_LINT ?= golangci-lint
PROTOC        ?= protoc
PROTO_OUT     ?= ./gen/go

# ── multipass-k8s-lab 설정 ────────────────────────────────────────────────────
# multipass-k8s-lab 클러스터 kubeconfig (기본값: 프로젝트 상대경로)
MULTIPASS_KUBECONFIG ?= $(shell realpath ../multipass-k8s-lab/kubeconfig 2>/dev/null || echo "")
# 클러스터 마스터 노드 IP + NodePort 레지스트리 주소
MULTIPASS_MASTER_IP  ?= 10.87.127.18
MULTIPASS_REGISTRY   ?= $(MULTIPASS_MASTER_IP):31500

# ── 포맷 ──────────────────────────────────────────────────────────────────────
fmt:
	go fmt ./...

# ── 린트 ──────────────────────────────────────────────────────────────────────
lint:
	$(GOLANGCI_LINT) run --config=.golangci.yml ./...

lint-fix:
	$(GOLANGCI_LINT) run --config=.golangci.yml --fix ./...

# ── 단위 테스트 ───────────────────────────────────────────────────────────────
test:
	go test -v -race -cover ./...

# ── 통합 테스트 (kind 클러스터) ───────────────────────────────────────────────
test-integration:
	KUBECONFIG=~/.kube/config go test -v -tags=integration ./... -timeout 10m

# ── 통합 테스트 (multipass-k8s-lab VM 클러스터) ───────────────────────────────
# 사전 조건:
#   1. multipass-k8s-lab 클러스터가 실행 중이어야 합니다.
#   2. make deploy-multipass 가 먼저 실행되어 레지스트리/RBAC이 배포돼야 합니다.
#
# 실행 방법:
#   make deploy-multipass          # 클러스터 리소스 배포 (최초 1회)
#   make test-integration-multipass
#
# 또는 한 번에:
#   make deploy-multipass test-integration-multipass
test-integration-multipass: build
	@if [ -z "$(MULTIPASS_KUBECONFIG)" ]; then \
	    echo "ERROR: multipass-k8s-lab/kubeconfig not found. Run cluster first." >&2; exit 1; \
	fi
	@echo "==> Cluster: $$(KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl get nodes --no-headers 2>&1 | awk '{print $$1, $$2}' | tr '\n' '  ')"
	@echo "==> Registry: $(MULTIPASS_REGISTRY)"
	@echo "==> Starting NodeForge (local binary → remote cluster)..."
	@KUBECONFIG=$(MULTIPASS_KUBECONFIG) \
	    NODEFORGE_REGISTRY_ADDR=$(MULTIPASS_REGISTRY) \
	    ./bin/nodeforge &
	@NF_PID=$$!; \
	sleep 3; \
	echo "==> Running integration tests (pid=$$NF_PID)..."; \
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) \
	    NODEFORGE_REGISTRY_ADDR=$(MULTIPASS_REGISTRY) \
	    go test -v -tags=integration ./pkg/build/... -timeout 12m; \
	TEST_EXIT=$$?; \
	echo "==> Stopping NodeForge (pid=$$NF_PID)..."; \
	kill $$NF_PID 2>/dev/null || true; \
	exit $$TEST_EXIT

# ── multipass 클러스터 리소스 배포 ────────────────────────────────────────────
deploy-multipass:
	@if [ -z "$(MULTIPASS_KUBECONFIG)" ]; then \
	    echo "ERROR: multipass-k8s-lab/kubeconfig not found." >&2; exit 1; \
	fi
	@echo "==> Applying NodeForge cluster resources..."
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl apply -f deploy/
	@echo "==> Waiting for registry pod to be ready..."
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl rollout status deployment/nodeforge-registry \
	    -n nodeforge-system --timeout=120s
	@echo "==> Registry ready at $(MULTIPASS_REGISTRY)"

# ── multipass 클러스터 리소스 제거 ────────────────────────────────────────────
undeploy-multipass:
	@if [ -z "$(MULTIPASS_KUBECONFIG)" ]; then \
	    echo "ERROR: multipass-k8s-lab/kubeconfig not found." >&2; exit 1; \
	fi
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl delete -f deploy/ --ignore-not-found=true

# ── 빌드 ──────────────────────────────────────────────────────────────────────
build:
	go build -o bin/nodeforge ./cmd/controlplane/...

# ── proto 생성 ────────────────────────────────────────────────────────────────
# api-protos 레포의 .proto 파일을 기준으로 생성.
# protoc-gen-go, protoc-gen-go-grpc 플러그인 필요.
proto:
	@mkdir -p $(PROTO_OUT)
	$(PROTOC) --proto_path=../api-protos/protos \
	  --go_out=$(PROTO_OUT) --go_opt=paths=source_relative \
	  --go-grpc_out=$(PROTO_OUT) --go-grpc_opt=paths=source_relative \
	  $(shell find ../api-protos/protos -name '*.proto')

# ── 커버리지 ──────────────────────────────────────────────────────────────────
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

# ── 정리 ──────────────────────────────────────────────────────────────────────
clean:
	rm -rf bin/ coverage.out $(PROTO_OUT)

# ── 전체 (포맷 → 린트 → 테스트) ──────────────────────────────────────────────
all: fmt lint test
