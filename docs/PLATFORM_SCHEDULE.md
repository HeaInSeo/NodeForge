# Platform Schedule

버전: 1.0  
작성일: 2026-04-18  
목적: **관련 프로젝트 전체 일정을 한 곳에서 관리. 개발 세션 시작 시 PLATFORM_MAP.md와 함께 확인.**

---

## 프로젝트별 현재 상태

| 프로젝트 | 상태 | 다음 작업 |
|----------|------|-----------|
| NodeVault | 운영 중, TODO-07 대기 | pkg/oras referrer push |
| NodeKit | 운영 중, warning 276개 | CA1062 warning 해소 + ApiProtosRoot 경로 전환 |
| api-protos | **Sprint 1-4 완료** (go.work 제거, vendor 정리 완료) | NodeVault→NodeVault rename, api-protos 저장소 삭제 |
| DockGuard | 완료 (9개 규칙, .wasm 번들) | 신규 정책 추가 시 재빌드 |
| DagEdit | 독립 운영 (Catalog 연결 없음) | P5에서 Catalog 연동 |
| sori | 프로토타입 (미통합) | P3에서 NodeVault 통합 |

---

## 현재 진행 중인 작업 (Active)

없음 — api-protos Sprint 1-4 완료로 Active 작업 없음.

---

## 다음 작업 큐 (Ready — 시작 가능)

### [NodeKit] compiler warning 276개 해소

- **우선순위**: 즉시
- **내용**: `HttpCatalogClient.cs`, `DataRegisterRequestFactory.cs` CA1062 null 검증 추가
- **완료 기준**: `dotnet build` warning 0 증가 (CLAUDE.md §8)
- **의존**: 없음

### [NodeKit] ApiProtosRoot → NodeVault/protos/ 경로 전환

- **우선순위**: 즉시 (api-protos Sprint 1-4 완료로 Ready)
- **내용**: `NodeKit.csproj` auto-detect 경로를 `NodeVault/protos/` 기준으로 업데이트
- **완료 기준**: `dotnet build` 시 `NodeVault/protos/nodeforge/v1/nodeforge.proto` 사용
- **의존**: 없음 (api-protos cleanup 완료됨)

### [NodeVault] TODO-07 — pkg/oras referrer push

- **우선순위**: P1 (선행 조건 TODO-06, TODO-08 모두 완료)
- **내용**: 이미지 빌드 후 spec JSON을 OCI referrer로 Harbor에 push
  - `pkg/oras/` 구현
  - `pkg/build/service.go:BuildAndRegisterAsync` 내 referrer push 추가
  - `SpecReferrerDigest` 필드 채워짐
- **완료 기준**: 등록 툴 `integrity_health = Healthy` (현재 모두 Partial)
- **의존**: 없음

---

## 단기 (P1 완료 후)

### [NodeVault] NodeVault → NodeVault rename

- **우선순위**: P1 완료 후 (api-protos cleanup 완료로 Ready)
- **내용**: repo명, 바이너리명, K8s resource명, gRPC 서비스명 일괄 변경
- **의존**: TODO-07 완료 후 진행 권장 (rename + referrer push 동시 PR은 리뷰 부담 큼)

### [NodeVault] TODO-09b — NodeVault runtime/deployment 전환

- **내용**: authority map 기반 NodeVault 단일 write authority 구현
- **선행 조건**: TODO-09a(완료), Cilium+Harbor 안정화(완료)
- **블로커**: rename 후 진행 권장

### [NodeVault] TODO-04 — proto/API 계약 갭 메우기

- **내용**: v0.2 전체 필드 라운드트립 검증, NodeKit dotnet build warning 해소 포함
- **의존**: NodeKit warning 해소 선행 필요

---

## 중기 (P3/P4)

### [NodeVault + NodeKit] TODO-12 — Data write path

- **내용**: DataDefinition UI 연결, DataRegisterRequest gRPC 전송, NodeVault data artifact 처리
- **현재**: `DataDefinition`, `DataRegisterRequest` C# 모델 존재, UI 미연결
- **의존**: TODO-06(완료), TODO-08(완료)

### [sori + NodeVault] TODO-13 — sori NodeVault 통합

- **내용**: sori 패키징 로직 NodeVault 흡수 범위 결정, API 계약
- **의존**: TODO-12

### [NodeVault] TODO-14 — Retract/Delete lifecycle

- **내용**: lifecycle_phase 전이 API, Harbor GC 연동
- **의존**: TODO-08(완료), TODO-09a(완료)

### [NodeVault] TODO-15a/b/c — reconcile loop + webhook

- **내용**: Harbor artifact 상태 주기적 대조, integrity_health 갱신
- **의존**: TODO-08(완료), Harbor 운영 중

---

## 장기 (P5)

### [DagEdit] Catalog 연동

- **내용**: RunnerNode에 casHash 기록, Catalog REST API 연결
- **의존**: TODO-10(완료), NodeVault Catalog 안정화

### [NodeVault] NodeVault → NodeVault rename

- **내용**: repo명, 바이너리명, K8s resource명, gRPC 서비스명 일괄 변경
- **의존**: api-protos cleanup 완료 (PROTO_OWNERSHIP_SPRINT_PLAN Sprint 3/4)

### [플랫폼] TODO-18 — README / 운영 문서 정리

- **내용**: 전체 아키텍처 다이어그램, authority map, 이중 축 상태 모델 통합 문서
- **의존**: P4 완료 후

---

## 의존성 흐름

```
[지금]
  api-protos Sprint 3/4  ──┬──► NodeVault → NodeVault rename
  NodeKit warning 해소    │
  TODO-07 (oras)         ─┘

[단기]
  TODO-07 완료 ──► TODO-09b ──► TODO-04

[중기]
  TODO-12 (Data) ──► TODO-13 (sori)
  TODO-14 (lifecycle)
  TODO-15a/b/c (reconcile)

[장기]
  DagEdit Catalog 연동
  TODO-18 (문서 정리)
```

---

## 프로젝트별 상세 일정 문서

| 프로젝트 | 문서 |
|----------|------|
| NodeVault 전체 TODO | `NodeVault/docs/NODEVAULT_TRANSITION_PLAN.md` |
| api-protos 이관 | `NodeVault/docs/PROTO_OWNERSHIP_SPRINT_PLAN.md` |
| NodeKit 아키텍처/이슈 | `NodeKit/docs/ARCHITECTURE.md` |
| 전체 플랫폼 구성 | `NodeVault/docs/PLATFORM_MAP.md` |

---

## 업데이트 규칙

- 작업 시작 시: 해당 항목을 "진행 중"으로 이동
- 작업 완료 시: 항목 제거 + `NODEVAULT_TRANSITION_PLAN.md` 체크박스 [x] 업데이트
- 새 작업 추가 시: 우선순위에 맞는 섹션에 삽입
