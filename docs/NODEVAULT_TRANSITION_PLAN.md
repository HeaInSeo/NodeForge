# NodeVault 아키텍처 전환 계획

버전: 1.2  
작성일: 2026-04-14 / 갱신: 2026-04-19

관련 문서:
- 아키텍처 개요: [ARCHITECTURE.md](ARCHITECTURE.md)
- v0.2 스펙: [TOOL_CONTRACT_V0_2.md](TOOL_CONTRACT_V0_2.md)

이 문서는 NodeForge → NodeVault 전환의 전체 TODO 목록, 우선순위, 의존성,
완료 기준을 기록한다. 개발 시작 전 이 문서를 먼저 확인할 것.

---

## 현재 상태 (2026-04-18 기준)

### 플랫폼 구성

| 컴포넌트 | 위치 | 상태 |
|----------|------|------|
| NodeKit (C#/Avalonia) | NodeKit/ | L1 검증 + BuildRequest gRPC 전송 완성, AdminToolList REST 전환 완료 |
| NodeForge (Go) | NodeForge/ | BuildService/PolicyService/ValidateService 완성, pkg/index 구현 완료, pkg/catalogrest 완성 |
| proto canonical source | NodeForge/protos/ | `nodeforge`, `tool`, `volres` ownership 회수 + go.work 제거 완료 (Sprint 1-4 완료) |
| DockGuard (OPA/Rego) | DockGuard/ | 9개 규칙(DFM/DSF/DGF), .wasm 번들 완성 |
| Harbor | harbor.10.113.24.96.nip.io | 운영 중 (Helm, Cilium LB VIP, all components healthy) |

### 아직 존재하지 않는 것

- ORAS referrer push (`pkg/oras` 패키지 미존재 — TODO-07)
- DataDefinition / DataRegisterRequest (NodeKit에 미구현 — TODO-12)
- 삭제/철회 lifecycle (Retract/Delete API — TODO-14)
- DagEdit Catalog 연동 (P5 이후)
- NodeForge → NodeVault rename (api-protos cleanup 완료 → 다음 작업 가능)

---

## 설계 원칙

### 재현성 (non-negotiable)

same data + same method = same result.
`latest` 태그, digest 미고정, 버전 미고정 패키지는 L1에서 차단. bypass 플래그 금지.

### artifact 상태 이중 축

index의 상태는 두 축으로 분리한다. **이 두 축을 같은 필드에 섞지 않는다.**

| 축 | 값 | 변경 주체 | 의미 |
|----|-----|-----------|------|
| `lifecycle_phase` | Pending / Active / Retracted / Deleted | NodeVault 명시적 호출 | 관리자가 내린 결정 |
| `integrity_health` | Healthy / Partial / Missing / Unreachable / Orphaned | reconcile loop | Harbor 현실과의 대조 결과 |

두 축을 섞으면 발생하는 문제:
- Retracted(의도적 숨김)와 Missing(Harbor에서 사라짐)이 구분 안 됨
- 알람 규칙과 Catalog 노출 규칙이 엉킴
- Active + Partial(공식 artifact인데 spec referrer 없음)을 표현할 수 없음

**Catalog 노출 규칙**: `lifecycle_phase = Active` 기준만. `integrity_health`는 알람/모니터링 전용.

교차 상태 예시:

| lifecycle_phase | integrity_health | 의미 | 처리 |
|-----------------|------------------|------|------|
| Active | Healthy | 정상 | Catalog 노출 |
| Active | Partial | 공식이지만 spec referrer 없음 | 알람, 노출 유지 |
| Active | Missing | 심각 — image가 Harbor에서 사라짐 | 알람, 긴급 조사, 노출 유지 |
| Active | Unreachable | 일시적 접근 불가 | 모니터링, 노출 유지 |
| Retracted | * | 의도적 숨김 | Catalog 제외 |
| Retracted | Missing | 정상적 결과 — 삭제 후 Harbor 확인됨 | 정상 |
| Deleted | * | 완전 퇴출 | 모든 경로에서 제외 |

---

## TODO 목록

---

### P0 — 먼저 닫아야 하는 계약

---

#### TODO-01 | RegisteredTool v0.2 계약 최종 고정 ✓

**완료 기준**
- [x] v0.2 계약 문서 (한 페이지) 작성 → `TOOL_CONTRACT_V0_2.md`
- [x] 문서 내 모든 필드가 proto에 존재
- [x] proto 필드가 NodeKit C# 모델에 반영
- [x] 저장 구조(현재 CAS JSON)가 v0.2 필드를 보존
- [x] ListTools / GetTool 응답이 v0.2 전체 필드 반환

**선행 조건**: 없음

---

#### TODO-02 | stableRef / casHash 경계 규칙 확정 ✓

**완료 기준**
- [x] 규칙 문서화: UI 탐색 = stableRef / pipeline 저장 = casHash / 실행 = casHash
- [x] ListTools API가 stableRef 기준 필터 지원 (`index.Store.ListByStableRef`)
- [x] pipeline 저장 모델 설계 시 casHash pin 강제 명시

**선행 조건**: 없음

---

#### TODO-03 | 비목표 목록 고정 ✓

**완료 기준**
- [x] 비목표 항목 목록 문서화 → `NONGOALS.md`
- [x] PR 리뷰 체크포인트에 비목표 항목 포함

**선행 조건**: 없음

---

### P0.5 — TODO-06 설계에 필요한 최소 선행 결정

---

#### TODO-16a | stableRef cardinality / reuse 최소 정책 결정 ✓

**완료 기준**
- [x] 위 4가지 질문에 대한 답 문서화 → `INDEX_SCHEMA.md` §5
- [x] cardinality 모델이 index 스키마 설계에 반영 가능한 수준으로 닫힘

**선행 조건**: TODO-02

---

### P1 — 기반 구현

> **P1 내부 실행 순서**: TODO-04 → TODO-06 → TODO-05 → TODO-08 → TODO-07
>
> **TODO-04 미완료** (NodeKit 경고 276개, 라운드트립 검증 미완).
> TODO-06/08은 TODO-04와 독립적으로 완료됨. TODO-07이 남은 P1 작업.

---

#### TODO-04 | proto / API 계약 갭 메우기 (진행 중)

**현재 상태**
- `dotnet build` 경고 276개 존재 (CA1062: `HttpCatalogClient.cs`, `DataRegisterRequestFactory.cs`)
- v0.2 전체 필드 라운드트립 검증 미완료

**완료 기준**
- [ ] v0.2 전체 필드 라운드트립 검증
- [ ] NodeKit C# 모델이 proto 필드를 빠짐없이 매핑
- [ ] NodeForge CAS 저장 JSON이 v0.2 전체 필드 보존
- [ ] ListTools / GetTool 응답에 v0.2 전체 필드 포함
- [ ] `dotnet build` 경고 증가 없음 (NodeKit CLAUDE.md §8)

**선행 조건**: TODO-01

---

#### TODO-06 | NodeVault 인덱스 구조 설계 확정 ✓

**완료 기준**
- [x] index 스키마 문서화 (tool + data 모두 수용 가능한 구조) → `INDEX_SCHEMA.md`
- [x] `lifecycle_phase` / `integrity_health` 이중 축 스키마에 반영
- [x] TODO-16a cardinality 모델 반영 (stableRef : casHash = 1:N)
- [x] lifecycle_phase 전이 규칙 명문화 (운영 의도 기반)
- [x] integrity_health 전이 규칙 명문화 (reconcile 관찰 기반)
- [x] stableRef 기준 조회, casHash 기준 역조회 지원 구조
- [x] CAS와의 관계 정의

**선행 조건**: TODO-02, TODO-16a

---

#### TODO-05 | Catalog 저장소 / 조회 모델 재정의 ✓

**완료 기준**
- [x] stableRef 기준 필터 지원 (`index.Store.ListByStableRef`)
- [x] kind(tool/data) 기준 필터 지원 (`KindTool`, `KindData` 상수)
- [x] casHash 기준 단건 조회 지원 (`index.Store.GetByCasHash`)
- [x] Catalog 노출이 `lifecycle_phase = Active` 기준으로만 동작 확인
- [x] NodeKit AdminToolList / AdminDataList 표시에 충분한 응답 필드

**선행 조건**: TODO-06

---

#### TODO-08 | `pkg/index` 추가 — 인덱스 관리 모듈 ✓

**완료 기준**
- [x] 등록 시 index append (`Store.Append`)
- [x] stableRef 기준 조회 (`Store.ListByStableRef`)
- [x] casHash 기준 역조회 (`Store.GetByCasHash`)
- [x] lifecycle_phase 전이 (NodeVault 명시적 호출: `Store.SetLifecyclePhase`)
- [x] integrity_health 전이 (reconcile loop 호출: `Store.SetIntegrityHealth`)
- [x] active 목록 조회 (`Store.ListActive` — lifecycle_phase = Active 기준)
- [x] 테스트 설계 포함 (15개 테스트 통과)

**선행 조건**: TODO-06

---

#### TODO-07 | `pkg/oras` 추가 — referrer push 경로

**현재 상태**
`pkg/oras` 패키지 없음. `SpecReferrerDigest` 항상 empty. 등록된 모든 툴 `integrity_health = Partial`.

**해야 할 것**
image manifest와 spec(ToolDefinition JSON)을 OCI referrer artifact로 연결.
구현 위치: `pkg/oras/` 패키지 + `pkg/build/service.go:BuildAndRegister` 내 호출 추가.

**완료 기준**
- [ ] subject image digest에 spec referrer push 성공
- [ ] mediaType 명시 (tool: `application/vnd.nodevault.toolspec.v1+json` / data: `application/vnd.nodevault.dataspec.v1+json`)
- [ ] tool / data 모두 같은 패턴으로 referrer 연결 가능
- [ ] Harbor에서 referrer 조회 확인
- [ ] 등록된 툴의 `SpecReferrerDigest` 필드가 채워짐

**선행 조건**: TODO-06 (완료), TODO-08 (완료) | **지금 시작 가능**

---

### P2 — NodeVault 본체와 읽기 서비스 분리

---

#### TODO-09a | NodeForge → NodeVault 역할 재구성 **설계** ✓

**완료 기준**
- [x] write authority 범위 문서화 → `AUTHORITY_MAP.md`
- [x] NodeForge 하위 책임 경계 명시
- [x] lifecycle_phase 변경 authority = NodeVault only 명시
- [x] integrity_health 변경 authority = reconcile loop 명시
- [x] Delete / Retract authority = NodeVault only 명시
- [x] index mutation authority = NodeVault only 명시
- [x] NodeForge build 완료 → NodeVault index commit 핸드오프 프로토콜 명시
- [x] 결과물: authority map 표 (구두 합의 아님)

**선행 조건**: TODO-01, TODO-02

---

#### TODO-09b | NodeForge runtime / deployment 전환 **구현**

**진입 조건 상태**: Cilium 운영 중 ✓, Harbor 운영 중 ✓, NodeForge K8s Deployment 운영 중 ✓

**완료 기준**
- [ ] NodeVault가 authority map대로 단일 write authority로 동작
- [ ] lifecycle_phase 변경 경로 = NodeVault only
- [ ] integrity_health 변경 경로 = reconcile loop only
- [ ] NodeForge-NodeVault 핸드오프 경계 구현
- [ ] 기존 unit + integration 테스트 통과

**선행 조건**: TODO-09a (완료)

---

#### TODO-10 | Catalog 서비스 별도 구현 ✓

> 현재 구현: 별도 K8s Deployment가 아닌 NodeForge 바이너리 내 `pkg/catalogrest`.

**완료 기준**
- [x] Catalog REST API (tool 목록 / data 목록 / 단건 조회) → `pkg/catalogrest`
- [x] NodeKit이 Catalog REST API로 AdminToolList/AdminDataList 표시 → `HttpCatalogClient`
- [x] NodeKit이 NodeVault 내부 저장 구조를 직접 알지 않음
- [x] Catalog가 `lifecycle_phase = Active`만 노출하는지 확인

**선행 조건**: TODO-05, TODO-09a

---

#### TODO-11 | Catalog 캐시 전략 결정 ✓

**완료 기준**
- [x] 캐시 TTL 또는 invalidation 정책 문서화 → `CATALOG_CACHE_STRATEGY.md`
- [x] lifecycle_phase 변경(Retract 등) 후 Catalog 반영 지연 허용 범위 명시 (단일 프로세스: 즉시)
- [x] integrity_health 변화가 Catalog 노출에 영향을 주지 않음을 구현 수준에서 확인

**선행 조건**: TODO-10

---

### P3 — data artifact 축 추가

---

#### TODO-12 | Data write path 구체화

**현재 상태**
NodeKit DataDefinition / DataRegisterRequest 미구현. NodeForge data artifact 처리 없음.

**해야 할 것**
data artifact(참조 genome, annotation bundle 등)를 공식 artifact로 등록/탐색 가능하게.
data artifact도 `lifecycle_phase` / `integrity_health` 이중 축 적용.

> **주의**: 구현은 P3이지만 TODO-06 설계 시 data 자리를 잡았다 (`KindData`, `artifact_kind` 필드).

**완료 기준**
- [ ] DataDefinition 모델 (NodeKit)
- [ ] DataRegisterRequest (NodeKit → NodeVault gRPC)
- [ ] data artifact의 stableRef / casHash 지원
- [ ] data artifact가 TODO-06 index 스키마에 정상 등록/조회
- [ ] lifecycle_phase / integrity_health 이중 축이 data artifact에도 적용

**선행 조건**: TODO-06 (완료), TODO-08 (완료)

---

#### TODO-13 | sori 패키징 로직의 NodeVault 통합 계획 고정

**완료 기준**
- [ ] sori 담당 범위 / NodeVault 흡수 범위 경계 문서화
- [ ] NodeVault-sori 간 API 계약 초안

**선행 조건**: TODO-12

---

### P4 — 운영 lifecycle 및 정합성

---

#### TODO-14 | 삭제 / 철회 lifecycle 설계 구현

**현재 상태**
삭제/철회 개념 없음. 파일 직접 삭제 외 운영 경로 없음.

**완료 기준**
- [ ] Retract API (NodeVault, lifecycle_phase 전이)
- [ ] lifecycle_phase = Retracted 상태에서 Catalog 조회 결과 제외
- [ ] 물리 삭제 경로 (Harbor blob 삭제 또는 GC)
- [ ] TODO-09a authority map에서 Retract/Delete = NodeVault only 반영
- [ ] lifecycle_phase 변경과 integrity_health 변경이 분리된 경로로 처리됨 확인

**선행 조건**: TODO-08 (완료), TODO-09a (완료)

---

#### TODO-15a | Harbor 이벤트 표면 검증

**현재 상태**: Harbor 운영 중 (이벤트 목록 미검증).

**완료 기준**
- [ ] Harbor 버전에서 지원하는 webhook 이벤트 목록 문서화
- [ ] GC 완료 이벤트 포함 여부 확인
- [ ] 관찰 불가능한 이벤트 목록 명시 (reconcile이 커버해야 하는 범위)

**선행 조건**: Harbor 운영 중 (완료)

---

#### TODO-15b | Reconcile loop 설계 — Harbor artifact 상태 판정 모델 ✓

**원칙**: reconcile-first. **webhook이 없어도 결국 맞춰지는 구조.**

핵심 규칙:
- reconcile은 Harbor 현실과 index를 대조하여 **`integrity_health`만 변경**
- `lifecycle_phase`는 reconcile이 **절대 변경하지 않음** (운영 의도 축)

**완료 기준**
- [x] reconcile이 integrity_health만 변경하고 lifecycle_phase는 건드리지 않음 → `pkg/reconcile/reconciler.go`
- [x] 5가지 상태별 NodeVault 응답 행동 정의 → `RegistryChecker` 인터페이스 + 상태 판정 로직
- [x] 빠른 루프 / 느린 루프 분리 구현 → `FastRun` (manifest+referrer 존재 확인) / `SlowRun` (pull 가능 여부)
- [x] reconcile 결과가 index integrity_health 전이로 반영 → `Store.SetIntegrityHealth` 호출
- [x] `pkg/reconcile` 테스트 11개 통과

**선행 조건**: TODO-08 (완료), TODO-15a

---

#### TODO-15c | Webhook fast path

**완료 기준**
- [ ] webhook 수신 시 reconcile trigger 호출 (integrity_health 갱신 유도)
- [ ] webhook 미수신 시에도 주기 reconcile이 상태 보정

**선행 조건**: TODO-15a, TODO-15b

---

#### TODO-16b | stableRef 재사용 운영 / UI 세부 정책

> TODO-16a(cardinality 최소 결정)는 P0.5에서 닫힘.

**완료 기준**
- [ ] Catalog UI revision 목록 표시 방식 결정
- [ ] active 전환 규칙 세부 정의 (수동 전환 / 자동 전환)
- [ ] TODO-06 index 스키마에 16a + 16b 결정사항 모두 반영 확인

**선행 조건**: TODO-06 (완료)

---

### P5 — 최종 전환

---

#### TODO-17 | NodeKit 연동 경로 전환 ✓

**완료 기준**
- [x] NodeKit AdminToolList/AdminDataList → Catalog REST API 사용 → `HttpCatalogClient`
- [x] NodeKit이 NodeVault 내부 저장 구조를 직접 알지 않음
- [x] 기존 NodeKit 테스트 모두 통과

**선행 조건**: TODO-10 (완료)

---

#### TODO-18 | README / 운영 문서 정리

> **권장**: P1/P4 결정 시마다 병행 작성. P5 끝에만 두면 전환 중 혼선이 길어짐.

**완료 기준**
- [ ] 전체 플랫폼 아키텍처 다이어그램 (write path / read path 분리 표시)
- [ ] authority map (TODO-09a) 포함
- [ ] 이중 축 상태 모델 (TODO-15b) 포함
- [ ] kaniko/NodeForge 과거 흔적 제거

**선행 조건**: TODO-17 이후

---

## 전체 의존성

```
P0:    01 ──┐
       02 ──┼──► 04 ──► 06* ──► 05
       03   │           │
            │           ├──► 08 ──► 07  ← 현재 여기
            └──► 09a ──► 09b
                    └──► 10 ──► 11

P0.5:  02 ──► 16a ──► 06*   ← 16a는 06 진입 전 필수

P3:    06(자리 확보) ──► 12 ──► 13

P4:    08 + 09a ──► 14
       15a ──► 15b ──► 15c
       06 ──► 16b

P5:    10 + 09b ──► 17 ──► 18
```

> `*` 06의 실제 선행 조건: TODO-02 + TODO-16a

---

## 위험 요약

| 항목 | 위험 | 완화 방법 |
|------|------|-----------|
| 07 | pkg/oras 구현 시 Harbor referrer API 호환성 | Harbor 버전 확인 후 oras-go 사용 |
| 15b | lifecycle_phase와 integrity_health를 같은 필드에 섞음 | 이중 축 분리 — reconcile은 integrity_health만 변경 |
| 09b | 09a와 동시 시작 | 인프라 안정화 조건 명시 — 실패 원인 분리 불가 |
| 06, 12 | data를 P3까지 index에서 고려 안 함 | index 스키마 설계 시 data 자리 확보 완료 |
| 15b | 상태 분류만 하고 응답 행동 누락 | 5가지 상태별 NodeVault 행동 반드시 포함 |
| 08 | index lifecycle 테스트 없음 | pkg/index 15개 테스트 통과 |
| 03 | 비목표가 PR에 슬며시 포함 | PR 리뷰 체크포인트 작동 |
