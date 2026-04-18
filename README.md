# NodeForge

NodeForge는 바이오인포매틱스 파이프라인용 컨테이너 이미지 빌드 및 등록 제어 플레인입니다.
NodeKit(어드민 UI)으로부터 `BuildRequest`를 받아 kaniko로 이미지를 빌드하고,
DockGuard 정책 검증 후 `RegisteredToolDefinition`을 CAS(Content-Addressable Storage)에 저장합니다.

---

## 전체 구조

```
NodeKit (C# 어드민 UI)
    │  BuildRequest (gRPC)
    ▼
NodeForge (이 프로젝트 — Go gRPC 서버)
    │
    ├── L2: kaniko Job (이미지 빌드 + 내부 레지스트리 push)
    ├── L3: kind dry-run (K8s API 스키마 검증)
    ├── L4: smoke run (컨테이너 실행 검증)
    └── 등록: RegisteredToolDefinition CAS 저장
```

---

## gRPC 서비스 목록

| 서비스 | 패키지 | 설명 |
|--------|--------|------|
| `PingService` | `pkg/ping` | 연결 확인 |
| `PolicyService` | `pkg/policy` | DockGuard `.wasm` 번들 제공 (NodeKit이 L1 정책 평가에 사용) |
| `BuildService` | `pkg/build` | `BuildAndRegister` — L2→L3→L4→등록 전체 파이프라인 |
| `ValidateService` | `pkg/validate` | L3 dry-run / L4 smoke run (BuildService 내부 호출) |
| `ToolRegistryService` | `pkg/catalog` | `RegisteredToolDefinition` 조회 (`ListTools`, `GetTool`) |

프로토 정의: [`protos/nodeforge/v1`](protos/nodeforge/v1/nodeforge.proto)

---

## 빠른 시작

### 사전 조건

| 도구 | 용도 |
|------|------|
| Go 1.22+ | 빌드 |
| kubectl | 클러스터 접근 |
| kind 또는 multipass-k8s-lab | 통합 테스트 클러스터 |

private 모듈 접근이 필요한 환경에서는 아래를 같이 설정한다.

```bash
export GOPRIVATE=github.com/HeaInSeo/*
export GONOSUMDB=github.com/HeaInSeo/*
export GOPROXY=direct
```

### 빌드 및 실행

```bash
# 빌드
make build

# 실행 (기본: 로컬 ~/.kube/config 클러스터, 포트 :50051)
./bin/nodeforge

# 환경 변수로 설정 변경
NODEFORGE_ADDR=:9090 \
NODEFORGE_REGISTRY_ADDR=10.87.127.18:31500 \
KUBECONFIG=/path/to/kubeconfig \
./bin/nodeforge
```

---

## 환경 변수

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `NODEFORGE_ADDR` | `:50051` | gRPC 서버 바인딩 주소 |
| `NODEFORGE_REGISTRY_ADDR` | `10.96.0.1:5000` | kaniko 이미지 push 대상 레지스트리 |
| `NODEFORGE_BUILD_NAMESPACE` | `nodeforge-builds` | kaniko Job 실행 네임스페이스 |
| `DOCKGUARD_WASM_PATH` | `assets/policy/dockguard.wasm` | DockGuard 정책 번들 경로 |
| `CATALOG_DIR` | `assets/catalog` | RegisteredToolDefinition CAS 저장 디렉토리 |
| `KUBECONFIG` | `~/.kube/config` | K8s 클러스터 인증 |

---

## 테스트

### 단위 테스트

```bash
GOPRIVATE=github.com/HeaInSeo/* \
GONOSUMDB=github.com/HeaInSeo/* \
GOPROXY=direct \
make test
# 또는
GOPRIVATE=github.com/HeaInSeo/* \
GONOSUMDB=github.com/HeaInSeo/* \
GOPROXY=direct \
go test ./...
```

**결과**: 62개 PASS (pkg/build 28 · pkg/catalog 6 · pkg/policy 6 · pkg/registry 10 · pkg/validate 12)

### 통합 테스트 — kind

```bash
# NodeForge를 별도 터미널에서 실행한 상태에서
KUBECONFIG=~/.kube/config make test-integration
```

### 통합 테스트 — multipass-k8s-lab (권장)

실제 VM 3노드 클러스터(Ubuntu 24.04, containerd, v1.32.13)에서 실행합니다.
단일 노드 kind보다 네트워크 현실성이 높고 멀티노드 스케줄링을 검증합니다.

```bash
# 최초 1회: 클러스터 리소스 배포
make deploy-multipass

# 통합 테스트 실행 (NodeForge 자동 시작/종료 포함)
make test-integration-multipass
```

자세한 내용은 [`docs/MULTIPASS_K8S_TESTING.md`](docs/MULTIPASS_K8S_TESTING.md)를 참조하세요.

---

## 패키지 구조

```
cmd/controlplane/     — gRPC 서버 진입점 (main.go)
pkg/
  build/              — BuildService: kaniko Job 생성·감시·로그 수집·digest 확보
  catalog/            — ToolRegistryService: RegisteredToolDefinition CAS 저장/조회
  ping/               — PingService: 헬스체크
  policy/             — PolicyService: DockGuard .wasm 번들 제공
  registry/           — OCI 레지스트리 클라이언트 (GetDigest)
  validate/           — ValidateService: L3 dry-run / L4 smoke run
assets/
  policy/             — dockguard.wasm (DockGuard 정책 빌드 결과물)
  catalog/            — RegisteredToolDefinition CAS 파일 저장소
deploy/
  00-namespaces.yaml  — nodeforge-system / nodeforge-builds / nodeforge-smoke
  01-registry.yaml    — registry:2 Deployment + NodePort 31500
  02-rbac.yaml        — ServiceAccount + ClusterRole + ClusterRoleBinding
docs/
  MULTIPASS_K8S_TESTING.md  — multipass-k8s-lab 통합 테스트 가이드
```

---

## 오케스트레이션 흐름

`BuildService.BuildAndRegister` 스트리밍 RPC의 실행 순서:

```
1. ConfigMap 생성    — Dockerfile 내용을 K8s ConfigMap으로 저장
2. kaniko Job 생성   — ConfigMap을 /workspace에 마운트, 내부 레지스트리로 push
3. Job 감시          — Watch API로 Succeeded / Failed 감지
4. Digest 확보       — 레지스트리 REST API 또는 kaniko 로그에서 sha256 추출
5. L3 dry-run        — smoke Job spec을 K8s dry-run으로 스키마 검증
6. L4 smoke run      — 빌드된 이미지로 실제 Job 실행, 정상 종료 확인
7. 등록              — RegisteredToolDefinition 생성 + SHA256 CAS 저장
```

이벤트는 `BuildEvent` 스트림으로 클라이언트(NodeKit)에 실시간 전달됩니다.

---

## DockGuard 정책

NodeKit의 L1 정책 평가에 사용되는 `.wasm` 번들은 [`DockGuard`](https://github.com/HeaInSeo/DockGuard) 레포에서 빌드됩니다.

| 패키지 | 규칙 | 설명 |
|--------|------|------|
| `dockerfile.multistage` | DFM001–DFM004 | 멀티스테이지 빌드 강제 |
| `dockerfile.security` | DSF001–DSF003 | root 실행 금지, 시크릿 노출 금지, ADD URL 금지 |
| `dockerfile.genomics` | DGF001–DGF002 | conda/pip 버전 고정 강제 |

번들 재빌드:
```bash
# DockGuard 레포에서
opa build -t wasm \
  -e dockerfile/multistage/deny \
  -e dockerfile/security/deny \
  -e dockerfile/genomics/deny \
  policy/dockerfile policy/security policy/genomics \
  -o /tmp/bundle.tar.gz
tar -xzf /tmp/bundle.tar.gz /policy.wasm
cp /policy.wasm /path/to/NodeForge/assets/policy/dockguard.wasm
```

---

## 관련 프로젝트

| 프로젝트 | 역할 |
|----------|------|
| [`NodeKit`](https://github.com/HeaInSeo/NodeKit) | C# 어드민 UI — ToolDefinition 편집, L1 정책 평가, BuildRequest 전송 |
| [`DockGuard`](https://github.com/HeaInSeo/DockGuard) | OPA/Rego Dockerfile 정책 + wasm 번들 빌드 |
| `protos/` | NodeForge canonical gRPC 프로토 정의 |
| [`multipass-k8s-lab`](https://github.com/HeaInSeo/multipass-k8s-lab) | VM 기반 K8s 테스트 클러스터 |
