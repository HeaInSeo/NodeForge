# RegisteredTool v0.2 계약 (TODO-01 완료 기준)

버전: v0.2.0  
작성일: 2026-04-14  
상태: **확정**

---

## 1. 핵심 원칙

- `stableRef` — 사람이 검색·탐색에 사용하는 이름 (`tool_name@version`)
- `casHash` — 시스템이 실행 pin에 사용하는 불변 식별자 (`SHA256(spec JSON)`)
- 파이프라인 저장·실행은 **항상 casHash** 기준. stableRef는 UI 전용.

---

## 2. 전체 필드 목록

| 필드 | 타입 | 출처 | 의미 |
|------|------|------|------|
| `cas_hash` | string | NodeForge 계산 | SHA256(spec JSON) — 파이프라인 toolRef pin |
| `tool_definition_id` | string | NodeKit | 작성 초안 식별자 |
| `tool_name` | string | NodeKit | 툴 이름 (예: "bwa-mem2") |
| `version` | string | NodeKit | 버전 (예: "2.2.1") |
| `stable_ref` | string | NodeForge 조립 | `tool_name@version`. UI 검색 전용. |
| `image_uri` | string | NodeKit | 베이스 이미지 URI (digest 포함 필수) |
| `digest` | string | NodeForge | 빌드 결과 이미지 digest |
| `environment_spec` | string | NodeKit | conda/pip 환경 스펙 (선택) |
| `command` | string | NodeKit | 컨테이너 진입점 (선택) |
| `inputs` | PortSpec[] | NodeKit | 입력 포트 계약 |
| `outputs` | PortSpec[] | NodeKit | 출력 포트 계약 |
| `display` | DisplaySpec | NodeKit | UI 팔레트 표시 메타데이터 |
| `lifecycle_phase` | string | NodeVault 명시적 호출 | `Pending` / `Active` / `Retracted` / `Deleted` |
| `integrity_health` | string | reconcile loop | `Healthy` / `Partial` / `Missing` / `Unreachable` / `Orphaned` |
| `validation` | ValidationStatus | NodeForge | L3/L4 검증 결과 |
| `registered_at` | int64 | NodeForge | Unix timestamp (등록 시각) |

---

## 3. 상태 이중 축

`lifecycle_phase`와 `integrity_health`는 **절대 같은 필드에 섞지 않는다**.

| 축 | 변경 주체 | 값 | 용도 |
|----|-----------|-----|------|
| `lifecycle_phase` | NodeVault 명시적 호출 | Pending / Active / Retracted / Deleted | Catalog 노출 결정 |
| `integrity_health` | reconcile loop | Healthy / Partial / Missing / Unreachable / Orphaned | 알람/모니터링 전용 |

**Catalog 노출 조건**: `lifecycle_phase = Active`만. `integrity_health`는 Catalog 노출에 영향 없음.

---

## 4. PortSpec 필드

| 필드 | 의미 |
|------|------|
| `name` | 포트 이름 (예: "reads") |
| `role` | 의미적 역할 (예: "sample-fastq") |
| `format` | 데이터 형식 (예: "fastq") |
| `shape` | "single" / "pair" |
| `required` | input 전용 |
| `class` | output 전용: "primary" / "secondary" |
| `constraints` | output 전용: key-value 제약 (예: sorted=coordinate) |

---

## 5. 계층별 라운드트립

```
NodeKit C# DTO (ToolDefinition)
    ↓  BuildRequest gRPC
NodeForge Go (BuildService)
    ↓  L2/L3/L4 통과 후
NodeForge Go (ToolRegistryService.RegisterTool)
    ↓  CAS 저장 (assets/catalog/{casHash}.tooldefinition)
RegisteredToolDefinition (v0.2 전체 필드 보존)
    ↓  ListTools / GetTool gRPC
NodeKit C# RegisteredTool
```

---

## 6. 완료 기준 체크리스트 (TODO-01)

- [x] v0.2 계약 문서 작성 (이 파일)
- [x] 문서 내 모든 필드가 proto에 존재 (`nodeforge.proto` lifecycle_phase/integrity_health 추가)
- [x] proto 필드가 NodeKit C# 모델에 반영 (`RegisteredTool.LifecyclePhase`)
- [x] 저장 구조(CAS JSON)가 v0.2 필드 보존 (`catalog.go` LifecyclePhase/IntegrityHealth)
- [x] ListTools `stable_ref` 필터 지원 (`ListToolsRequest.stable_ref`)
