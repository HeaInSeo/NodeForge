# NodeVault 설계

버전: 0.1
작성일: 2026-04-13

---

## 역할 한 줄 정의

플랫폼 아티팩트(툴 이미지, 참조 데이터 이미지)의 생성부터 삭제까지
생명주기 전체를 통제하는 관리 서비스. Harbor 위의 권위 있는 관리자.

---

## 배경 — NodeForge에서 NodeVault로

기존 NodeForge는 툴 이미지 빌드와 CAS 등록만 담당했다.
아키텍처 결정에 따라 다음 두 가지 역할을 통합한다:

| 기존 컴포넌트 | 역할 | NodeVault 내 위치 |
|-------------|------|-----------------|
| NodeForge | 툴 이미지 빌드 + push | Tool Write Path |
| sori | 참조 데이터 패키징 + push | Data Write Path |

두 write path의 출력 형태가 동일하기 때문에 통합이 자연스럽다:
- OCI Image → Harbor push
- OCI referrer artifact → metadata 첨부
- 인덱스 기록 → Catalog 서비스가 쿼리

---

## 전체 구조

```
[NodeKit]  ─── 관리자 UI
    │ BuildRequest / DataRegisterRequest (gRPC)
    ▼
[NodeVault]  ─── 생명주기 통제, 인덱스 SoT
    ├── Tool Write Path     (buildah / podbridge5)
    ├── Data Write Path     (sori 패키징 로직)
    ├── 인덱스              (등록된 아티팩트 목록)
    ├── Lifecycle API       (삭제, 업데이트)
    └── Webhook Handler     (Harbor 직접 조작 보정)
    │
    ├── Harbor push / delete
    │
    ▼
[Harbor]  ─── OCI 아티팩트 스토리지
    ├── library/<tool>:latest  + tool spec referrer
    └── data/<dataset>:latest  + data spec referrer

    ↑ webhook (예외 경로)
    │
[NodeVault Webhook Handler]  ─── 인덱스 보정

[Catalog 서비스]  ─── NodeVault 인덱스 API → 파이프라인 빌더 palette
```

---

## 배포 환경

NodeVault는 buildah / podbridge5 의존성으로 인해 bare metal에서 실행한다.

| 항목 | 값 |
|------|-----|
| 실행 환경 | bare metal (systemd, 100.123.80.48) |
| 이유 | 툴 이미지 빌드에 buildah / CGo 필요 |
| 외부 노출 | gRPC :50051 (NodeKit), REST :8080 (Catalog 서비스 내부) |
| 인덱스 저장 | 로컬 embedded store (bbolt 또는 SQLite, TBD) |

---

## OCI Artifact 명세

### 툴 이미지 referrer

| 항목 | 값 |
|------|-----|
| mediaType | `application/vnd.nodevault.toolspec.v1+json` |
| subject | 툴 이미지 manifest descriptor |
| content | tool spec JSON |

tool spec JSON 핵심 필드 (RegisteredTool v0.2 기준):
```json
{
  "identity":   { "tool", "version", "stableRef" },
  "runtime":    { "image", "imageDigest", "command" },
  "ports":      { "inputs": [...], "outputs": [...] },
  "display":    { "label", "category", "description", "tags" },
  "provenance": { "toolDefinitionId", "imageDigest", "registeredAt" }
}
```

### 참조 데이터 이미지 referrer

| 항목 | 값 |
|------|-----|
| mediaType | `application/vnd.nodevault.dataspec.v1+json` |
| subject | 데이터 이미지 manifest descriptor |
| content | data spec JSON |

data spec JSON 핵심 필드:
```json
{
  "identity":   { "dataset", "version", "stableRef" },
  "data":       { "format", "species", "genomeVersion", "totalSize", "partitions" },
  "display":    { "label", "category", "description", "tags" },
  "provenance": { "source", "imageDigest", "createdAt" }
}
```

`data` 섹션의 세부 필드는 sori 재설계 시 확정한다.

---

## Write Path 1 — 툴 이미지

NodeKit이 BuildRequest를 전송하면 NodeVault가 수행하는 순서:

```
1. L1 검증 (NodeKit에서 이미 수행, NodeVault는 수신만)
2. L2: buildah로 이미지 빌드 → Harbor push → digest 획득
3. L3: K8s dry-run (Job manifest 검증)
4. L4: K8s smoke run (실제 컨테이너 실행 확인)
5. tool spec JSON 직렬화
6. oras-go: tool spec referrer push (subject = 이미지 digest)
7. 인덱스 기록 (stableRef, digest, mediaType, registeredAt)
8. 완료 이벤트 스트림 → NodeKit
```

---

## Write Path 2 — 참조 데이터 이미지

NodeKit이 DataRegisterRequest를 전송하면 NodeVault가 수행하는 순서:

```
1. 메타데이터 검증 (format, species 등 필수 필드)
2. 데이터 소스 확인 (경로 또는 URI)
3. sori 패키징: 파티션별 tar.gz → OCI Image Manifest 구성
4. Harbor push → digest 획득
5. data spec JSON 직렬화
6. oras-go: data spec referrer push (subject = 데이터 이미지 digest)
7. 인덱스 기록
8. 완료 이벤트 스트림 → NodeKit
```

sori 패키징 로직은 sori 재설계 완료 후 NodeVault에 통합한다.
현재 sori는 초기 프로토타입 상태로 재검토 예정.

---

## 인덱스 설계

인덱스는 NodeVault가 소유하는 SoT다.
Harbor에 직접 아티팩트가 존재하더라도 인덱스에 없으면 Catalog에 노출되지 않는다.

### 인덱스 항목

| 필드 | 설명 |
|------|------|
| `stableRef` | 식별자 (`bwa-mem@0.7.17`, `hg38-genome@2024`) |
| `kind` | `tool` / `data` |
| `imageDigest` | Harbor 이미지 digest |
| `specDigest` | referrer artifact digest |
| `harborRepo` | Harbor repository 경로 |
| `registeredAt` | 등록 시각 |
| `phase` | `Active` / `Retracted` |

### 인덱스 저장

- bare metal 로컬 embedded store (bbolt 또는 SQLite)
- Catalog 서비스는 NodeVault REST API를 통해 인덱스를 쿼리한다
- 인덱스 직접 파일 접근 금지 — API 경유만 허용

---

## Harbor Webhook 보정

Harbor 직접 조작(UI/API)으로 아티팩트가 삭제된 경우의 안전망.

- 이벤트: `DELETE_ARTIFACT`
- 처리: 해당 digest로 인덱스 검색 → `phase: Retracted` 처리 또는 항목 제거
- 신뢰성: best-effort (누락 허용 — 정상 경로에 영향 없음)
- 누락 복구: 관리자 수동 `reconcile` 커맨드 제공

webhook이 primary sync가 아니라 예외 보정 수단임을 명시한다.

---

## Lifecycle 관리

### 삭제

삭제는 반드시 NodeVault API를 통해서만 수행한다.

```
DELETE /api/v1/tools/{stableRef}
DELETE /api/v1/datasets/{stableRef}
```

처리 순서:
1. 인덱스에서 `phase: Retracted` 마킹 (즉시, Catalog 반영)
2. Harbor에서 이미지 삭제 (비동기)
3. referrer artifact 정리 (Harbor GC 처리)

Harbor UI/API 직접 삭제는 운영 규칙상 금지.
비상 시 직접 삭제 후 `nodevault reconcile` 커맨드로 인덱스 동기화.

### 업데이트

tool spec / data spec 메타데이터 수정:
- referrer artifact 재push (새 digest)
- 인덱스 갱신

이미지 자체 업데이트(리빌드)는 새 stableRef 또는 동일 stableRef로 신규 등록 처리.
stableRef 재사용 정책은 별도 결정.

---

## NodeKit 연동

NodeKit은 NodeVault의 gRPC 클라이언트다.

| 요청 | RPC |
|------|-----|
| 툴 이미지 빌드 요청 | `BuildService.BuildAndRegister` |
| 참조 데이터 등록 요청 | `DataService.RegisterData` (신규, TBD) |
| 툴 목록 조회 | → Catalog 서비스 REST API로 전환 |
| 데이터 목록 조회 | → Catalog 서비스 REST API로 전환 |
| 삭제 요청 | `ManagementService.Delete` (TBD) |

`DataService`, `ManagementService` proto 설계는 sori 재설계 및 NodeKit UI 설계와 병행.

---

## 기존 NodeForge와의 관계

NodeVault는 NodeForge를 대체한다.
NodeForge 코드베이스가 NodeVault의 시작점이 된다.

| 항목 | 처리 |
|------|------|
| `pkg/build` | 유지 — Tool Write Path 핵심 |
| `pkg/policy` | 유지 — PolicyService 유지 |
| `pkg/validate` | 유지 — L3/L4 |
| `pkg/catalog` (CAS) | **제거** — 인덱스로 대체 |
| `ToolRegistryService` gRPC | **제거** — Catalog 서비스로 대체 |
| `pkg/oras` | **신규** — referrer push |
| `pkg/index` | **신규** — 인덱스 관리 |
| `pkg/data` | **신규** — Data Write Path (sori 통합 후) |

---

## 미결 사항

- 서비스 정식 이름 확정 (repo명, 바이너리명)
- 인덱스 embedded store 선택 (bbolt vs SQLite)
- `DataService` proto 설계 (sori 재설계 병행)
- stableRef 재사용 정책 (동일 버전 리빌드 시)
- NodeKit DataRegisterRequest UI 상세 설계
- sori 재설계 범위 및 일정
