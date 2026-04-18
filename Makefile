.PHONY: fmt lint lint-fix lint-config golangci-lint test test-integration test-integration-multipass \
        deploy-multipass undeploy-multipass build push-image vendor \
        proto coverage clean all

LOCALBIN      ?= $(CURDIR)/bin
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
GOLANGCI_LINT_VERSION ?= v2.11.3
PROTOC        ?= protoc
PROTO_OUT     ?= ./gen/go
PROTO_SRC     ?= ./protos

# ── 컨테이너 빌드 관련 태그 ───────────────────────────────────────────────────
# btrfs-progs-devel, gpgme-devel C 헤더 없이도 빌드 가능하도록
# containers/storage, containers/image의 선택적 드라이버를 제외한다.
BUILDTAGS ?= exclude_graphdriver_btrfs containers_image_openpgp exclude_graphdriver_devicemapper

# ── multipass-k8s-lab / Harbor 설정 ──────────────────────────────────────────
MULTIPASS_KUBECONFIG ?= $(shell realpath ../multipass-k8s-lab/kubeconfig 2>/dev/null || echo "")
MULTIPASS_REGISTRY   ?= harbor.10.113.24.96.nip.io
IMAGE                ?= $(MULTIPASS_REGISTRY)/nodeforge/controlplane:latest

# ── 포맷 ──────────────────────────────────────────────────────────────────────
fmt:
	go fmt ./...

# ── 린트 ──────────────────────────────────────────────────────────────────────
golangci-lint:
	@mkdir -p "$(LOCALBIN)"
	@test -x "$(GOLANGCI_LINT)" || bash -c '\
		set -euo pipefail; \
		curl -fsSL "https://api.github.com/repos/golangci/golangci-lint/releases/tags/$(GOLANGCI_LINT_VERSION)" >/dev/null; \
		OS="$$(uname | tr A-Z a-z)"; \
		ARCH="$$(uname -m)"; \
		case "$$ARCH" in x86_64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; *) echo "unsupported arch: $$ARCH"; exit 1 ;; esac; \
		VER="$(GOLANGCI_LINT_VERSION)"; \
		VER="$${VER#v}"; \
		FILE="golangci-lint-$$VER-$$OS-$$ARCH.tar.gz"; \
		URL="https://github.com/golangci/golangci-lint/releases/download/$(GOLANGCI_LINT_VERSION)/$$FILE"; \
		SUM_URL="https://github.com/golangci/golangci-lint/releases/download/$(GOLANGCI_LINT_VERSION)/golangci-lint-$$VER-checksums.txt"; \
		TMP="$$(mktemp -d)"; \
		curl -fsSL "$$URL" -o "$$TMP/lint.tgz"; \
		curl -fsSL "$$SUM_URL" -o "$$TMP/checksums.txt"; \
		EXPECTED="$$(awk -v f="$$FILE" "\$$2==f{print \$$1}" "$$TMP/checksums.txt")"; \
		if [ -z "$$EXPECTED" ]; then echo "checksum not found for $$FILE"; exit 1; fi; \
		if command -v sha256sum >/dev/null 2>&1; then \
			ACTUAL="$$(sha256sum "$$TMP/lint.tgz" | awk "{print \$$1}")"; \
		elif command -v shasum >/dev/null 2>&1; then \
			ACTUAL="$$(shasum -a 256 "$$TMP/lint.tgz" | awk "{print \$$1}")"; \
		else \
			echo "no sha256 tool found (sha256sum/shasum)"; exit 1; \
		fi; \
		if [ "$$EXPECTED" != "$$ACTUAL" ]; then echo "checksum mismatch for $$FILE"; exit 1; fi; \
		tar -xzf "$$TMP/lint.tgz" -C "$$TMP"; \
		cp "$$TMP/golangci-lint-$$VER-$$OS-$$ARCH/golangci-lint" "$(GOLANGCI_LINT)"; \
		chmod +x "$(GOLANGCI_LINT)"; \
		rm -rf "$$TMP"'

lint: golangci-lint
	$(GOLANGCI_LINT) run --config=.golangci.yml --build-tags "$(BUILDTAGS)" ./...

lint-fix: golangci-lint
	$(GOLANGCI_LINT) run --config=.golangci.yml --build-tags "$(BUILDTAGS)" --fix ./...

lint-config: golangci-lint
	$(GOLANGCI_LINT) config verify --config=.golangci.yml

# ── 단위 테스트 ───────────────────────────────────────────────────────────────
test:
	go test -tags "$(BUILDTAGS)" -v -race -cover ./...

# ── 통합 테스트 (multipass-k8s-lab VM 클러스터) ───────────────────────────────
# 사전 조건:
#   1. multipass-k8s-lab 클러스터가 실행 중
#   2. Harbor가 실행 중  (scripts/host/harbor-resume.sh)
#   3. make deploy-multipass 완료
#
# 실행:
#   make deploy-multipass test-integration-multipass
test-integration-multipass: build
	@if [ -z "$(MULTIPASS_KUBECONFIG)" ]; then \
	    echo "ERROR: multipass-k8s-lab/kubeconfig not found. 클러스터를 먼저 실행하세요." >&2; exit 1; \
	fi
	@echo "==> Cluster: $$(KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl get nodes --no-headers 2>&1 | awk '{print $$1, $$2}' | tr '\n' '  ')"
	@echo "==> Registry: $(MULTIPASS_REGISTRY) (Harbor)"
	@echo "==> NodeForge 로컬 바이너리 실행 중..."
	@KUBECONFIG=$(MULTIPASS_KUBECONFIG) \
	    NODEFORGE_REGISTRY_ADDR=$(MULTIPASS_REGISTRY) \
	    ./bin/nodeforge &
	@NF_PID=$$!; \
	sleep 3; \
	echo "==> 통합 테스트 실행 (pid=$$NF_PID)..."; \
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) \
	    NODEFORGE_REGISTRY_ADDR=$(MULTIPASS_REGISTRY) \
	    go test -v -tags "integration $(BUILDTAGS)" ./pkg/build/... -timeout 12m; \
	TEST_EXIT=$$?; \
	echo "==> NodeForge 종료 (pid=$$NF_PID)..."; \
	kill $$NF_PID 2>/dev/null || true; \
	exit $$TEST_EXIT

# ── 클러스터 리소스 배포 (deploy/ + k8s/) ─────────────────────────────────────
# 순서: 네임스페이스 → RBAC → NodeForge → GRPCRoute
deploy-multipass:
	@if [ -z "$(MULTIPASS_KUBECONFIG)" ]; then \
	    echo "ERROR: multipass-k8s-lab/kubeconfig not found." >&2; exit 1; \
	fi
	@echo "==> NodeForge 클러스터 리소스 배포..."
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl apply -f deploy/00-namespaces.yaml
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl apply -f deploy/02-rbac.yaml
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl apply -f deploy/03-nodeforge.yaml
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl apply -f deploy/04-grpcroute.yaml
	@echo "==> NodeForge Deployment 준비 대기..."
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl rollout status deployment/nodeforge-controlplane \
	    -n nodeforge-system --timeout=180s
	@echo "==> 배포 완료: grpc://nodeforge.10.113.24.96.nip.io:80"

# ── 클러스터 리소스 제거 ──────────────────────────────────────────────────────
undeploy-multipass:
	@if [ -z "$(MULTIPASS_KUBECONFIG)" ]; then \
	    echo "ERROR: multipass-k8s-lab/kubeconfig not found." >&2; exit 1; \
	fi
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl delete -f deploy/04-grpcroute.yaml --ignore-not-found=true
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl delete -f deploy/03-nodeforge.yaml --ignore-not-found=true
	KUBECONFIG=$(MULTIPASS_KUBECONFIG) kubectl delete -f deploy/02-rbac.yaml      --ignore-not-found=true

# ── 로컬 바이너리 빌드 ────────────────────────────────────────────────────────
build:
	go build -tags "$(BUILDTAGS)" -o bin/nodeforge ./cmd/controlplane/...

# ── vendor 생성 (컨테이너 이미지 빌드 전 필요) ────────────────────────────────
# go.mod의 replace directive(podbridge5)가 로컬 경로를 가리키므로
# vendor/ 에 복사해야 Dockerfile 내 빌드가 가능하다.
vendor:
	go mod vendor

# ── NodeForge 이미지 빌드 + Harbor push ───────────────────────────────────────
# 사전 조건:
#   podman login harbor.10.113.24.96.nip.io   (최초 1회)
#
# 실행:
#   make push-image
#   make push-image IMAGE=harbor.10.113.24.96.nip.io/nodeforge/controlplane:v1.0.0
push-image: vendor
	podman build \
	    -t $(IMAGE) \
	    -f Dockerfile \
	    .
	podman push $(IMAGE)

# ── proto 생성 ────────────────────────────────────────────────────────────────
proto:
	@mkdir -p $(PROTO_OUT)
	$(PROTOC) --proto_path=$(PROTO_SRC) \
	  --go_out=$(PROTO_OUT) --go_opt=paths=source_relative \
	  --go-grpc_out=$(PROTO_OUT) --go-grpc_opt=paths=source_relative \
	  $(shell find $(PROTO_SRC) -name '*.proto')

# ── 커버리지 ──────────────────────────────────────────────────────────────────
coverage:
	go test -tags "$(BUILDTAGS)" -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

# ── 정리 ──────────────────────────────────────────────────────────────────────
clean:
	rm -rf bin/ vendor/ coverage.out $(PROTO_OUT)

# ── 전체 (포맷 → 테스트 → 빌드) ──────────────────────────────────────────────
all: fmt test build
