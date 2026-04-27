# 툴 노드 관련 YAML/JSON 현황

작성일: 2026-04-18  
상태: 현황 기록 (구현 완료/미완료 포함)

이 문서는 플랫폼에서 "툴 노드"가 어떤 형태의 YAML/JSON/spec으로 표현되는지
5개 계층으로 분리해 현재 상태를 기록한다.

---

## 계층 구조 요약

```
[1] 빌드 레시피        Dockerfile + conda spec (사용자 작성)
         ↓ L2 빌드 (podbridge5)
[2] 배포 인프라 YAML   NodeVault K8s Deployment/ConfigMap/Service
         ↓ gRPC BuildRequest
[3] ToolDefinition     NodeKit 작성 → NodeVault 수신 → CAS JSON 저장
         ↓ TODO-07 (미구현)
[4] OCI referrer spec  Harbor에 첨부되는 spec artifact ← 현재 MISSING
         ↓ TODO-DagEdit (미설계)
[5] 파이프라인 노드    DagEdit RunnerNode 안에 casHash 기록 ← 현재 MISSING
```

---

## 계층 1 — 빌드 레시피 (Dockerfile + conda spec)

### 역할

사용자(관리자)가 NodeKit UI에서 작성하는 빌드 입력이다.
이 내용이 `BuildRequest.dockerfile_content` 필드로 NodeVault에 전송된다.

### 형식

NodeKit이 서버로 전송하는 proto 필드:

```protobuf
message BuildRequest {
  string dockerfile_content = 3;  // Dockerfile 전체 텍스트
  string environment_spec   = 6;  // conda spec (YAML 텍스트)
  ...
}
```

NodeVault가 받아서 podbridge5 in-process 빌드로 전달한다.

### 현재 상태: 동작 중

- `pkg/build/service.go:BuildAndRegister` 가 `req.DockerfileContent` 를 그대로 podbridge5에 전달
- L1 검증(NodeKit): `latest` 태그, digest 미고정, 버전 미고정 패키지 → 차단
- L2(podbridge5): 이미지 빌드 + Harbor push + digest 획득
- L3: K8s dry-run
- L4: smoke run

### 주의

Dockerfile 원본은 빌드 후 **어디에도 보존되지 않는다**.
`BuildRequest` 메시지가 gRPC 스트림으로 전송되고 소모되면 끝이다.
빌드 이력 재현을 위해서는 buildContextRef 또는 sourceDraftRef 저장이 필요하지만
현재 범위에서 의도적으로 제외됐다 (`REGISTERED_TOOL_V0_2_DESIGN.md` 의도적 제외 항목 참조).

---

## 계층 2 — 배포 인프라 YAML (NodeVault K8s 리소스)

### 역할

NodeVault 바이너리가 K8s 위에서 실행되기 위한 Deployment / ConfigMap / Service / GRPCRoute.

### 현재 위치

```
NodeVault/deploy/
├── deployment.yaml     # Deployment (1/1 Running, seoy lab cluster)
├── configmap.yaml      # 환경변수 설정
├── service.yaml        # ClusterIP Service
└── grpcroute.yaml      # Cilium GRPCRoute → nodeforge.10.113.24.96.nip.io:80
```

### 현재 상태: 동작 중

- NodeVault: `nodeforge` namespace, 1/1 Running (2026-04-18 기준)
- gRPC 노출: `nodeforge.10.113.24.96.nip.io:80` (Cilium Gateway API)
- Harbor 연결: `harbor.10.113.24.96.nip.io` (Cilium LB VIP, seoy 호스트에서 route 필요)

### 이름 불일치 주의

현재 리소스 이름은 모두 `nodeforge`다.
최종 목표는 `nodevault`로 rename. api-protos 저장소 제거 완료로 **rename 가능 상태**.

---

## 계층 3 — ToolDefinition / CAS JSON (NodeKit ↔ NodeVault 계약)

### 역할

NodeKit에서 관리자가 툴을 정의한 내용을 proto 직렬화해 NodeVault에 전송하고,
NodeVault가 이를 CAS 파일로 저장하는 중간 표현이다.

### 계약 문서

**TOOL_CONTRACT_V0_2.md** 가 확정 계약이다.

### Proto 흐름

```
NodeKit:
  ToolDefinition (C# 모델)
    → BuildRequest (proto)
      → gRPC stream → NodeVault

NodeVault:
  BuildRequest
    → Build (podbridge5)
    → RegisterToolRequest (proto)
      → pkg/catalog (CAS JSON 저장)
      → pkg/index (vault-index.json 기록)
```

### CAS JSON 저장 위치

```
assets/catalog/{casHash}.tooldefinition
```

JSON 키 (TOOL_CONTRACT_V0_2.md 기준):

```json
{
  "tool_name":          "bwa-mem",
  "version":            "0.7.17",
  "stable_ref":         "bwa-mem@0.7.17",
  "image_uri":          "harbor.../library/bwa-mem:latest",
  "digest":             "sha256:...",
  "command":            ["/app/run.sh"],
  "inputs":             [...],
  "outputs":            [...],
  "display":            { "label": "...", "category": "...", "tags": [...] },
  "environment_spec":   "name: bwa\n...",
  "cas_hash":           "sha256:..."
}
```

### 현재 상태: 동작 중

- NodeKit `HttpCatalogClient`: Catalog REST API 호출로 AdminToolList 표시
- NodeVault `pkg/catalogrest`: `GET /api/v1/tools` → `index.Store.ListActive()`
- NodeVault `pkg/index`: 이중 축 상태 관리 (lifecycle_phase + integrity_health), 15개 테스트 통과

### 알려진 미완료

- NodeKit `dotnet build` 276개 경고 존재 (CA1062, HttpCatalogClient.cs 등)
  → CLAUDE.md §8 위반 — 다음 작업에서 수정 필요
- TODO-04 (v0.2 전체 필드 라운드트립 검증) 미완료

---

## 계층 4 — OCI referrer spec JSON (Harbor 첨부 spec artifact)

### 역할

툴 이미지가 Harbor에 push된 후, 해당 이미지의 **OCI referrer**로 spec JSON을 첨부한다.
이 referrer artifact가 존재해야 `integrity_health = Healthy`가 된다.
referrer 없이 이미지만 있으면 `integrity_health = Partial`.

### 설계

```
Harbor:
  library/bwa-mem:latest
    └── [referrer] mediaType: application/vnd.nodevault.toolspec.v1+json
                  subject: library/bwa-mem@sha256:img-digest
                  content: tool spec JSON
```

referrer spec JSON 구조 (NODEVAULT_DESIGN.md / TOOL_CONTRACT_V0_2.md 기반):

```json
{
  "identity":   { "tool": "bwa-mem", "version": "0.7.17", "stableRef": "bwa-mem@0.7.17" },
  "runtime":    { "image": "harbor.../library/bwa-mem:latest", "imageDigest": "sha256:...", "command": ["/app/run.sh"] },
  "ports":      { "inputs": [...], "outputs": [...] },
  "display":    { "label": "BWA-MEM 0.7.17", "category": "Alignment", ... },
  "provenance": { "toolDefinitionId": "...", "imageDigest": "sha256:...", "registeredAt": "..." }
}
```

### 현재 상태: **미구현 (TODO-07)**

- `pkg/oras/doc.go`: 스텁만 존재 (패키지 선언 + 역할 주석)
- `pkg/index/schema.go`:
  ```go
  // SpecReferrerDigest is the OCI digest of the attached spec referrer artifact.
  // Empty until pkg/oras pushes the referrer (TODO-07).
  SpecReferrerDigest string `json:"spec_referrer_digest,omitempty"`
  ```
  → 현재 등록된 모든 툴의 `spec_referrer_digest`가 empty
- `pkg/build/service.go:BuildAndRegister`: L4 이후 `RegisterTool` 호출,
  referrer push 단계 없음 (TODO-07 구현 후 이 위치에 추가됨)

### 영향

- 현재 등록된 모든 툴: `integrity_health = Partial` (이미지는 있지만 spec referrer 없음)
- reconcile loop 구현 후에도 이 상태가 유지됨 (TODO-15b 구현 조건)
- TODO-07 완료 전까지 `integrity_health = Healthy` 상태는 불가능

### 구현 순서

TODO-07은 TODO-06(index 설계 완료) + TODO-08(pkg/index 구현) 이후에 진행.
P1 마지막 항목 — 현재 TODO-06, TODO-08 모두 완료됨 → **TODO-07이 P1 다음 작업**.

---

## 계층 5 — 파이프라인 노드 JSON (DagEdit RunnerNode)

### 역할

파이프라인 빌더(DagEdit)에서 사용자가 툴을 캔버스에 추가할 때
해당 노드에 어떤 툴의 어떤 revision(`casHash`)을 사용하는지 기록한 것.

재현성 원칙: 파이프라인에 저장되는 것은 `stableRef`가 아니라 `casHash`여야 한다.

### 현재 상태: **미설계 (DagEdit 별도 작업)**

현재 DagEdit (`/opt/dotnet/src/github.com/HeaInSeo/DagEdit/DagItems.cs`):

```csharp
public enum DagItemsType
{
    StartNode,
    EndNode,
    RunnerNode,   // 툴 노드
    Connection,
}
```

`RunnerNode`에 `casHash` / `RegisteredTool` 참조가 없다.
DagEdit는 Catalog REST API와 연결되어 있지 않다.

### 필요한 연결 경로 (설계되지 않음)

```
사용자: 툴 팔레트에서 "BWA-MEM 0.7.17" 선택
  ↓
DagEdit: GET /api/v1/tools?q=bwa-mem (Catalog REST API 호출)
  ↓
DagEdit: 응답의 casHash를 RunnerNode에 기록
  ↓
파이프라인 저장 JSON:
  {
    "nodeType": "runner",
    "casHash": "sha256:...",     ← 재현성 보장 pin
    "displayName": "BWA-MEM 0.7.17"
  }
```

### 비고

DagEdit 연동은 NODEVAULT_TRANSITION_PLAN.md에서 TODO-17 이후 (P5) 단계다.
NodeKit의 AdminToolList(관리자 전용)와 달리, DagEdit의 palette는 파이프라인 빌더 사용자 대상이다.
이 두 소비자의 요구사항은 다를 수 있으며 별도 설계가 필요하다.

---

## 계층별 완료 현황 요약

| 계층 | 내용 | 상태 |
|------|------|------|
| 1. 빌드 레시피 | Dockerfile → podbridge5 빌드 | 동작 중 |
| 2. 배포 인프라 YAML | NodeVault K8s Deployment | 동작 중 (이름은 NodeVault) |
| 3. ToolDefinition / CAS | NodeKit ↔ NodeVault 계약, pkg/catalog + pkg/index | 동작 중 (TODO-04 일부 미완) |
| 4. OCI referrer spec | Harbor spec artifact 첨부 | **미구현 (TODO-07)** |
| 5. 파이프라인 노드 | DagEdit RunnerNode casHash 연결 | **미설계 (P5 이후)** |

---

## 다음 작업

### 즉시 가능 (P1 last)
- **TODO-07**: `pkg/oras` 구현 — image manifest에 spec referrer push
  - 선행 조건: TODO-06 (완료), TODO-08 (완료)
  - 작업 위치: `pkg/oras/`, `pkg/build/service.go` (BuildAndRegister 내 referrer push 추가)

### 선행 필요
- **NodeKit 경고 수정**: `HttpCatalogClient.cs` CA1062 등 276개 경고 해결
- **NodeVault → NodeVault rename**: api-protos 제거 완료 → Ready
- **TODO-04**: proto/API 계약 갭 메우기 (NodeKit dotnet build 경고 해결 포함)

### P5 이후
- **DagEdit Catalog 연동**: RunnerNode에 casHash 기록, Catalog REST API 연결
