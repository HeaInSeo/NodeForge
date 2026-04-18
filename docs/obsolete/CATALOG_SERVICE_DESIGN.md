# Catalog 서비스 설계

버전: 0.1
작성일: 2026-04-13

---

## 역할 한 줄 정의

NodeVault 인덱스를 읽어 파이프라인 빌더(사용자)에게
툴 이미지와 참조 데이터 이미지를 통합 palette로 제공하는 읽기 전용 서비스.

---

## 위치

```
[NodeVault]  ─── 인덱스 SoT, 생명주기 통제 (bare metal)
    │ REST API (내부)
    ▼
[Catalog 서비스]  ─── read-only (K8s Deployment)
    │ REST API (외부)
    ▼
[파이프라인 빌더 UI]  ─── 툴 + 데이터 palette
```

Catalog 서비스는 NodeVault 인덱스를 쿼리한다.
Harbor를 직접 쿼리하지 않는다.

---

## 배포 환경

| 항목 | 값 |
|------|-----|
| 런타임 | K8s Deployment |
| 이유 | buildah 불필요 — 순수 HTTP/gRPC 클라이언트 |
| NodeVault 접근 | bare metal REST API 경유 |
| 외부 노출 | Cilium HTTPRoute → Gateway → 파이프라인 빌더 UI |

---

## NodeVault 인덱스 연동

Catalog 서비스는 NodeVault가 노출하는 내부 REST API를 호출한다.

```
GET http://<nodevault-host>:8080/internal/v1/index
→ 전체 인덱스 스냅샷 반환

GET http://<nodevault-host>:8080/internal/v1/index/{stableRef}
→ 단건 조회
```

### 캐싱

NodeVault 인덱스 변경 시마다 Catalog 서비스에 반영되는 방식:

- **NodeVault push 방식**: 인덱스 변경 시 Catalog 서비스 캐시 무효화 알림
- **Catalog 폴링 fallback**: 주기적 전체 인덱스 재조회 (기본 30초)

Catalog 서비스는 in-memory 캐시를 유지하며 NodeVault가 응답 불가인 경우 캐시로 서빙한다.

---

## Artifact 종류

Catalog 서비스는 두 종류의 아티팩트를 통합해서 제공한다.

| kind | 설명 | referrer mediaType |
|------|------|-------------------|
| `tool` | 툴 이미지 | `application/vnd.nodevault.toolspec.v1+json` |
| `data` | 참조 데이터 이미지 | `application/vnd.nodevault.dataspec.v1+json` |

파이프라인 빌더는 `kind`로 두 종류를 구분해 palette를 렌더링한다:
- `tool` → 실행 노드 (컨테이너로 실행)
- `data` → 데이터 노드 (K8s Image Volume으로 마운트)

---

## API 설계 (파이프라인 빌더용)

### `GET /api/v1/artifacts`

툴 + 참조 데이터 통합 목록. palette 초기 로딩용.

쿼리 파라미터: `kind=tool|data`, `category=`, `tag=`, `q=` (검색)

응답:
```json
[
  {
    "stableRef": "bwa-mem@0.7.17",
    "kind": "tool",
    "display": {
      "label": "BWA-MEM 0.7.17",
      "category": "Alignment",
      "description": "paired-end FASTQ → coordinate-sorted BAM",
      "tags": ["dna", "alignment"]
    },
    "phase": "Active"
  },
  {
    "stableRef": "hg38-genome@2024",
    "kind": "data",
    "display": {
      "label": "GRCh38 Reference Genome 2024",
      "category": "Reference",
      "description": "Homo sapiens GRCh38 primary assembly",
      "tags": ["human", "genome", "grch38"]
    },
    "phase": "Active"
  }
]
```

### `GET /api/v1/artifacts/{stableRef}`

아티팩트 상세. 파이프라인 빌더가 노드를 canvas에 추가할 때 호출.

- `kind: tool` → ports (inputs/outputs), runtime, provenance 포함
- `kind: data` → data (format, species, genomeVersion, partitions), provenance 포함

### `GET /api/v1/tools`

툴만 필터링한 목록. `GET /api/v1/artifacts?kind=tool` 와 동일.

### `GET /api/v1/datasets`

참조 데이터만 필터링한 목록. `GET /api/v1/artifacts?kind=data` 와 동일.

---

## 파이프라인 빌더 palette 동작

파이프라인 빌더(DagEdit 등)에서 사용자가 툴을 선택해 노드를 추가할 때:

1. `GET /api/v1/artifacts/{stableRef}` 호출
2. 응답의 `imageDigest` (casHash 역할)를 파이프라인 노드에 기록
3. 사용자는 "BWA-MEM 0.7.17"을 선택했지만 파이프라인에 저장되는 것은 digest

재현성 원칙: 파이프라인은 stableRef가 아닌 digest로 아티팩트를 참조한다.

---

## 이 서비스가 하지 않는 것

- NodeVault에 쓰지 않는다 (write path = NodeVault 전담)
- Harbor를 직접 쿼리하지 않는다
- 툴 빌드 요청, 데이터 등록 요청을 받지 않는다
- 삭제/업데이트 요청을 처리하지 않는다
- 관리자 기능을 노출하지 않는다 (관리자 = NodeKit → NodeVault)

---

## NodeKit(AdminList)과의 관계

현재 NodeKit AdminToolList는 NodeForge `ToolRegistryService.ListTools` gRPC를 호출한다.
NodeVault 전환 후 NodeKit은 Catalog 서비스 REST API를 호출한다.

| 소비자 | 현재 | 전환 후 |
|--------|------|---------|
| NodeKit AdminToolList | NodeForge `ListTools` gRPC | Catalog `GET /api/v1/tools` |
| NodeKit AdminDataList | 없음 | Catalog `GET /api/v1/datasets` |
| 파이프라인 빌더 UI | 없음 | Catalog `GET /api/v1/artifacts` |

NodeKit 전환은 NodeVault + Catalog 서비스 구현 완료 후 별도 스프린트에서 진행한다.

---

## 미결 사항

- 서비스 정식 이름 확정
- NodeVault → Catalog 캐시 무효화 알림 방식 (push vs poll)
- 인증/인가 — 파이프라인 빌더 UI 접근 제어
- `imageDigest` 기반 단건 조회 API 필요 여부 (파이프라인 실행 경로)
- 다중 Harbor project 지원 여부
