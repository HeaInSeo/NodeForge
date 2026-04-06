.PHONY: fmt lint lint-fix test test-integration build proto clean

GOLANGCI_LINT ?= golangci-lint
PROTOC        ?= protoc
PROTO_OUT     ?= ./gen/go

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

# ── 통합 테스트 (kind 클러스터 필요) ──────────────────────────────────────────
test-integration:
	KUBECONFIG=~/.kube/config go test -v -tags=integration ./... -timeout 10m

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
