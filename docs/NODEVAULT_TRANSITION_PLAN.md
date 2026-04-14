# NodeVault 아키텍처 전환 계획

버전: 1.0
작성일: 2026-04-14

관련 문서:
- 아키텍처 개요: [NODEVAULT_DESIGN.md](NODEVAULT_DESIGN.md)
- v0.2 스펙: [REGISTERED_TOOL_V0_2_DESIGN.md](REGISTERED_TOOL_V0_2_DESIGN.md)

이 문서는 NodeForge → NodeVault 전환의 전체 TODO 목록, 우선순위, 의존성,
완료 기준을 기록한다. 개발 시작 전 이 문서를 먼저 확인할 것.

---

## 현재 상태

### 플랫폼 구성

| 컴포넌트 | 위치 | 상태 |
|----------|------|------|
| NodeKit (C#/Avalonia) | NodeKit/ | L1 검증 + BuildRequest gRPC 전송 완성 |
| NodeForge (Go) | NodeForge/ | BuildService/PolicyService/ValidateService/ToolRegistryService 완성, 62개 테스트 통과 |
| api-protos | api-protos/ | RegisteredTool v0.2 proto 부분 반영 |
| DockGuard (OPA/Rego) | DockGuard/ | 9개 규칙(DFM/DSF/DGF), .wasm 번들 완성 |
| 레지스트리 | registry:2 (NodePort 31500) | Harbor 미설치, 내부 레지스트리만 사용 중 |

### 아직 존재하지 않는 것

- Harbor (registry:2만 있음)
- ORAS referrer push (`pkg/oras` 없음 — 스텁만 존재)
- NodeVault index (`pkg/index` 없음 — 스텁만 존재, CAS 파일 저장만 존재)
- Catalog 서비스 분리 (현재 ToolRegistryService가 NodeForge 내부)
- DataDefinition / DataRegisterRequest (NodeKit에 미구현)
- stableRef 재사용 정책 (proto에 stable_ref 필드는 있으나 정책 미정)
- 삭제/철회 lifecycle
- Harbor-Index 정합성 보정
- 이중 상태 축 (lifecycle_phase / integrity_health 미분리)

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

#### TODO-01 | RegisteredTool v0.2 계약 최종 고정

**현재 상태**
- proto에 `PortSpec`, `DisplaySpec`, `ValidationStatus`, `BuildEvent`, `RegisteredToolDefinition` 정의됨
- NodeKit C# 모델(`ToolDefinition`, `BuildRequest`)과 proto 사이 갭 존재
- `cas_hash`, `stable_ref`, `phase` 필드는 proto에 있으나 각 계층 의미 통일 미검증

**해야 할 것**
tool의 identity / port spec / display metadata / validation phase를 하나의 계약으로 확정.
문서 → proto → API → DTO → 저장 구조 모든 계층이 같은 v0.2 의미를 말하는지 갭 검토.

**완료 기준**
- [ ] v0.2 계약 문서 (한 페이지) 작성
- [ ] 문서 내 모든 필드가 proto에 존재
- [ ] proto 필드가 NodeKit C# 모델에 반영
- [ ] 저장 구조(현재 CAS JSON)가 v0.2 필드를 보존
- [ ] ListTools / GetTool 응답이 v0.2 전체 필드 반환

**선행 조건**: 없음

---

#### TODO-02 | stableRef / casHash 경계 규칙 확정

**현재 상태**
- proto `RegisteredToolDefinition`에 `stable_ref`, `cas_hash` 필드 모두 존재
- 어느 계층에서 어느 것을 쓰는지 명문화 없음

**해야 할 것**
- stableRef: 사람이 탐색에 사용하는 이름 (UI 검색, Catalog 목록)
- casHash: 시스템이 실행에 사용하는 불변 pin (pipeline 저장, 실제 실행)

**완료 기준**
- [ ] 규칙 문서화: UI 탐색 = stableRef / pipeline 저장 = casHash / 실행 = casHash
- [ ] ListTools API가 stableRef 기준 필터 지원
- [ ] pipeline 저장 모델 설계 시 casHash pin 강제 명시

**선행 조건**: 없음

---

#### TODO-03 | 비목표 목록 고정

**해야 할 것**
이번 버전에서 의도적으로 하지 않는 항목 목록 작성 및 PR 리뷰 기준으로 작동시키기.

**완료 기준**
- [ ] 비목표 항목 목록 문서화
- [ ] PR 리뷰 체크포인트에 비목표 항목 포함 (문서화만으로 완료 아님)

**선행 조건**: 없음

---

### P0.5 — TODO-06 설계에 필요한 최소 선행 결정

> 원래 16번의 일부였으나, 06(index 스키마 설계)에 직접 영향을 주는 최소 결정만 분리.
> 06 진입 전에 반드시 닫아야 한다. 06을 설계하면 이 질문들을 암묵적으로 가정하고 들어가게 된다.

---

#### TODO-16a | stableRef cardinality / reuse 최소 정책 결정

**왜 06 전에 필요한가**
index 스키마의 컬럼 구조와 제약 조건이 이 질문들의 답에 따라 달라진다.

아래 4가지 질문이 닫혀야 index 스키마를 설계할 수 있다:
- stableRef : casHash 카디널리티가 1:1인가 1:N인가
- 같은 stableRef 아래 여러 revision 허용 여부
- active가 하나만 가능한가, 여러 개 가능한가
- index에 `stableRef → current active` 매핑 레코드가 필요한가

**완료 기준**
- [ ] 위 4가지 질문에 대한 답 문서화
- [ ] cardinality 모델이 index 스키마 설계에 반영 가능한 수준으로 닫힘

**선행 조건**: TODO-02

---

### P1 — 기반 구현

> **P1 내부 실행 순서**: TODO-04 → TODO-06 → TODO-05 → TODO-08 → TODO-07
>
> TODO-05(Catalog 모델)는 TODO-06(index 설계) 완료 후 진입. **병렬 불가.**

---

#### TODO-04 | proto / API 계약 갭 메우기

**현재 상태**
- v0.2 필드가 부분적으로만 각 계층에 반영됨
- `BuildRequest.cs`(NodeKit)와 proto `BuildRequest` 일치 여부 미검증
- `catalog.go`의 CAS 저장 구조가 v0.2 전체 필드를 보존하는지 미검증

**해야 할 것**
v0.2 계약 기준으로 각 계층 갭 목록 작성.
NodeKit C# DTO → proto → NodeForge DTO → CAS JSON 전체 라운드트립 검증.

**완료 기준**
- [ ] v0.2 전체 필드 라운드트립 검증
- [ ] NodeKit C# 모델이 proto 필드를 빠짐없이 매핑
- [ ] NodeForge CAS 저장 JSON이 v0.2 전체 필드 보존
- [ ] ListTools / GetTool 응답에 v0.2 전체 필드 포함
- [ ] `dotnet build` 경고 증가 없음 (NodeKit CLAUDE.md §8)

**선행 조건**: TODO-01

---

#### TODO-06 | NodeVault 인덱스 구조 설계 확정

**현재 상태**
- `assets/catalog/{SHA256}` 파일 기반 CAS만 존재
- index 개념 없음, Harbor 정합성 보정 개념 없음, phase 전이 로직 없음

**해야 할 것**
index는 Harbor 저장 사실이 아니라 **플랫폼의 승인 사실을 기록하는 원장**.

이중 축 상태 모델을 index 스키마에 반영:
- `lifecycle_phase`: Pending / Active / Retracted / Deleted
- `integrity_health`: Healthy / Partial / Missing / Unreachable / Orphaned

stableRef cardinality 모델(TODO-16a) 기반으로 컬럼 설계.

> **중요**: data artifact(TODO-12)는 P3에 구현하지만,
> **index 스키마는 지금 data 항목도 수용할 자리를 잡아야 한다**.
> 나중에 retrofit하면 index 구조를 뜯어야 한다.

**완료 기준**
- [ ] index 스키마 문서화 (tool + data 모두 수용 가능한 구조)
- [ ] `lifecycle_phase` / `integrity_health` 이중 축 스키마에 반영
- [ ] TODO-16a cardinality 모델 반영 (stableRef : casHash 관계)
- [ ] lifecycle_phase 전이 규칙 명문화 (운영 의도 기반)
- [ ] integrity_health 전이 규칙 명문화 (reconcile 관찰 기반)
- [ ] stableRef 기준 조회, casHash 기준 역조회 지원 구조
- [ ] CAS와의 관계 정의

**선행 조건**: TODO-02, **TODO-16a** (cardinality 결정 선행 필수)

---

#### TODO-05 | Catalog 저장소 / 조회 모델 재정의

**현재 상태**
- `ToolRegistryService` (NodeForge 내부 gRPC): RegisterTool, GetTool, ListTools
- stableRef 기준 필터, kind 기준 필터 없음

**해야 할 것**
TODO-06 index 설계 기반으로 읽기 모델 재정의.
Catalog 노출 규칙: `lifecycle_phase = Active` 기준. `integrity_health`는 모니터링 전용.

**완료 기준**
- [ ] stableRef 기준 필터 지원
- [ ] kind(tool/data) 기준 필터 지원
- [ ] casHash 기준 단건 조회 지원
- [ ] Catalog 노출이 `lifecycle_phase = Active` 기준으로만 동작 확인
- [ ] NodeKit AdminToolList / AdminDataList 표시에 충분한 응답 필드

**선행 조건**: TODO-06

---

#### TODO-08 | `pkg/index` 추가 — 인덱스 관리 모듈

**현재 상태**
없음. `pkg/catalog`가 CAS 파일 저장만 담당. `pkg/index/doc.go` 스텁만 존재.

**해야 할 것**
index를 읽고/쓰고/전이하는 **단일 제어 계층** 구현.
`lifecycle_phase` 전이와 `integrity_health` 전이를 분리된 경로로 처리.
NodeVault 내 다른 패키지는 이 모듈을 통해서만 index에 접근.

**완료 기준**
- [ ] 등록 시 index append
- [ ] stableRef 기준 조회
- [ ] casHash 기준 역조회
- [ ] lifecycle_phase 전이 (NodeVault 명시적 호출)
- [ ] integrity_health 전이 (reconcile loop 호출)
- [ ] active 목록 조회 (lifecycle_phase = Active 기준)
- [ ] **테스트 설계 포함**: 두 축 각각의 전이, stableRef 조회, casHash 역조회, 교차 상태 케이스

**선행 조건**: TODO-06

---

#### TODO-07 | `pkg/oras` 추가 — referrer push 경로

**현재 상태**
없음. `pkg/registry/registry.go`에 `GetDigest()` 구현만 있음. `pkg/oras/doc.go` 스텁만 존재.

**해야 할 것**
image manifest와 spec(ToolDefinition JSON)을 OCI referrer artifact로 연결.

**완료 기준**
- [ ] subject image digest에 spec referrer push 성공
- [ ] mediaType 명시 (tool: `application/vnd.nodevault.toolspec.v1+json` / data: `application/vnd.nodevault.dataspec.v1+json`)
- [ ] tool / data 모두 같은 패턴으로 referrer 연결 가능
- [ ] Harbor(또는 registry:2)에서 referrer 조회 확인

**선행 조건**: TODO-06, TODO-08

---

### P2 — NodeVault 본체와 읽기 서비스 분리

---

#### TODO-09a | NodeForge → NodeVault 역할 재구성 **설계**

**현재 상태**
- NodeForge가 build / policy / validate / catalog 모든 책임을 한 곳에서 담당
- write authority 분리 개념 없음

**해야 할 것**
설계 리스크와 구현 리스크를 분리. 이 항목은 **설계만**, 구현은 TODO-09b.
아래 authority map을 **표 형식, 한 페이지**로 작성.

| 작업 | 소유자 |
|------|--------|
| Build 실행 | NodeForge (위임) |
| Build 완료 이벤트 수신 | NodeForge triggers |
| Index commit 판단/실행 | NodeVault only |
| Register / index append | NodeVault only |
| lifecycle_phase 변경 | NodeVault only |
| Delete / Retract | NodeVault only |
| integrity_health 변경 | reconcile loop (NodeVault 내부) |
| Catalog 노출 결정 | lifecycle_phase 기준 only |
| index write | NodeVault only |

> **핵심**: NodeForge build 완료는 trigger. index commit 판단과 실행은 NodeVault only.
> 이 핸드오프 경계가 authority map에 없으면 "build 성공했는데 index에 없다" 혼선 발생.

**완료 기준**
- [ ] write authority 범위 문서화
- [ ] NodeForge 하위 책임 경계 명시
- [ ] lifecycle_phase 변경 authority = NodeVault only 명시
- [ ] integrity_health 변경 authority = reconcile loop 명시
- [ ] Delete / Retract authority = NodeVault only 명시
- [ ] index mutation authority = NodeVault only 명시
- [ ] NodeForge build 완료 → NodeVault index commit 핸드오프 프로토콜 명시
- [ ] 결과물: authority map 표 (구두 합의 아님)

**선행 조건**: TODO-01, TODO-02 | **지금 시작 가능** (인프라 무관)

---

#### TODO-09b | NodeForge runtime / deployment 전환 **구현**

**진입 조건**: **Cilium + Harbor 기반 최소 안정화 후**

현재 대기 중인 병행 작업 (닫히기 전 09b 진입 금지):
- Flannel → Cilium 마이그레이션
- Harbor Helm 설치
- kaniko → buildah(podbridge5) 전환
- NodeForge K8s Deployment화

> **주의**: 09a와 동시 시작 시 장애 원인 분리 불가.
> semantic 실패 / 네트워크 / Harbor / 배포 wiring 실패가 한 번에 섞임.

**완료 기준**
- [ ] NodeVault가 authority map대로 단일 write authority로 동작
- [ ] lifecycle_phase 변경 경로 = NodeVault only
- [ ] integrity_health 변경 경로 = reconcile loop only
- [ ] NodeForge-NodeVault 핸드오프 경계 구현
- [ ] 기존 62개 unit + integration 테스트 통과

**선행 조건**: TODO-09a + Cilium + Harbor 안정화

---

#### TODO-10 | Catalog 서비스 별도 구현

**현재 상태**
`ToolRegistryService`가 NodeForge 내부 gRPC. read/write 미분리.

**해야 할 것**
write path(NodeVault)와 read path(Catalog)를 공식 분리.
Catalog는 read-only REST 서비스. 노출 기준: `lifecycle_phase = Active`.

**완료 기준**
- [ ] Catalog REST API (tool 목록 / data 목록 / 단건 조회)
- [ ] NodeKit이 Catalog REST API로 AdminToolList/AdminDataList 표시
- [ ] NodeKit이 NodeVault 내부 저장 구조를 직접 알지 않음
- [ ] Catalog가 `lifecycle_phase = Active`만 노출하는지 확인

**선행 조건**: TODO-05, TODO-09a

---

#### TODO-11 | Catalog 캐시 전략 결정

**노출 기본 규칙 (불변)**
Catalog 노출 여부는 **lifecycle_phase만으로 결정**한다.
`integrity_health`는 Catalog 노출에 영향을 주지 않는다 — 알람/모니터링 전용.
`lifecycle_phase = Active`이면 integrity_health가 Partial이나 Unreachable이어도 Catalog에 노출된다.
이 규칙은 캐시 전략과 무관하게 항상 성립한다.

**완료 기준**
- [ ] 캐시 TTL 또는 invalidation 정책 문서화
- [ ] lifecycle_phase 변경(Retract 등) 후 Catalog 반영 지연 허용 범위 명시
- [ ] integrity_health 변화가 Catalog 노출에 영향을 주지 않음을 구현 수준에서 확인

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

> **주의**: 구현은 P3이지만 TODO-06 설계 시 data 자리를 잡아야 retrofit 불필요.

**완료 기준**
- [ ] DataDefinition 모델 (NodeKit)
- [ ] DataRegisterRequest (NodeKit → NodeVault gRPC)
- [ ] data artifact의 stableRef / casHash 지원
- [ ] data artifact가 TODO-06 index 스키마에 정상 등록/조회
- [ ] lifecycle_phase / integrity_health 이중 축이 data artifact에도 적용

**선행 조건**: TODO-06 (index 스키마에 data 자리 확보), TODO-08

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

**해야 할 것**
`lifecycle_phase` 전이로 처리: Active → Retracted → Deleted.
사용자 노출 상태와 물리 저장소 정리를 분리.
권장 순서: Retracted → Catalog 숨김 → Harbor 정리 → Deleted.

**완료 기준**
- [ ] Retract API (NodeVault, lifecycle_phase 전이)
- [ ] lifecycle_phase = Retracted 상태에서 Catalog 조회 결과 제외
- [ ] 물리 삭제 경로 (Harbor blob 삭제 또는 GC)
- [ ] TODO-09a authority map에서 Retract/Delete = NodeVault only 반영
- [ ] lifecycle_phase 변경과 integrity_health 변경이 분리된 경로로 처리됨 확인

**선행 조건**: TODO-08, TODO-09a

---

#### TODO-15a | Harbor 이벤트 표면 검증

**현재 상태**: Harbor 미설치.

**완료 기준**
- [ ] Harbor 버전에서 지원하는 webhook 이벤트 목록 문서화
- [ ] GC 완료 이벤트 포함 여부 확인
- [ ] 관찰 불가능한 이벤트 목록 명시 (reconcile이 커버해야 하는 범위)

**선행 조건**: Harbor Helm 설치 (TODO-09b 선행 조건과 겹침)

---

#### TODO-15b | Reconcile loop 설계 — Harbor artifact 상태 판정 모델

**원칙**: reconcile-first. **webhook이 없어도 결국 맞춰지는 구조.**

핵심 규칙:
- reconcile은 Harbor 현실과 index를 대조하여 **`integrity_health`만 변경**
- `lifecycle_phase`는 reconcile이 **절대 변경하지 않음** (운영 의도 축)

상태 판정 4계층:

| 계층 | 의미 |
|------|------|
| Exists | subject image manifest 존재 |
| Attached | required spec referrer 존재 |
| Complete | platform 최소 요구 세트 충족 |
| Reachable | 실제 pull/read 검증 성공 |

5가지 대조 상태와 NodeVault 응답 행동:

| Harbor 상태 | integrity_health | NodeVault 행동 |
|-------------|------------------|----------------|
| image O / spec O | Healthy | 무처리 |
| image O / spec X | Partial | integrity_health → Partial, 알람 |
| image X / spec O | Orphaned | **기본값: 수동 대기** — 알람 후 운영자 판단, 자동 삭제 금지 |
| image X / spec X | Missing | integrity_health → Missing, 알람 |
| image O / spec O / read fail | Unreachable | integrity_health → Unreachable, 모니터링 |

reconcile 루프 분리:

```
빠른 루프 (주기적):              Exists + Attached + Complete → integrity_health 갱신
느린 루프 (주기 길거나 on-demand): Reachable 검증 → integrity_health 갱신
```

**완료 기준**
- [ ] reconcile이 integrity_health만 변경하고 lifecycle_phase는 건드리지 않음
- [ ] 5가지 상태별 NodeVault 응답 행동 정의
- [ ] 빠른 루프 / 느린 루프 분리 구현
- [ ] reconcile 결과가 index integrity_health 전이로 반영
- [ ] `pkg/index` 테스트에 reconcile 상태 전이 케이스 포함

**선행 조건**: TODO-08, TODO-15a

---

#### TODO-15c | Webhook fast path

**완료 기준**
- [ ] webhook 수신 시 reconcile trigger 호출 (integrity_health 갱신 유도)
- [ ] webhook 미수신 시에도 주기 reconcile이 상태 보정

**선행 조건**: TODO-15a, TODO-15b

---

#### TODO-16b | stableRef 재사용 운영 / UI 세부 정책

> TODO-16a(cardinality 최소 결정)는 P0.5에서 닫힘.
> 이 항목은 그 결정 위에서 UI 표시와 운영 정책을 구체화하는 작업.

**완료 기준**
- [ ] Catalog UI revision 목록 표시 방식 결정
- [ ] active 전환 규칙 세부 정의 (수동 전환 / 자동 전환)
- [ ] TODO-06 index 스키마에 16a + 16b 결정사항 모두 반영 확인

**선행 조건**: TODO-06

---

### P5 — 최종 전환

---

#### TODO-17 | NodeKit 연동 경로 전환

**현재 상태**
`GrpcToolRegistryClient`가 NodeForge `ToolRegistryService` 직접 연결. Catalog REST API 미구현.

**완료 기준**
- [ ] NodeKit AdminToolList/AdminDataList → Catalog REST API 사용
- [ ] NodeKit이 NodeVault 내부 저장 구조를 직접 알지 않음
- [ ] 기존 NodeKit 테스트 모두 통과

**선행 조건**: TODO-10, TODO-09b

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
            │           ├──► 08 ──► 07
            └──► 09a ──► 09b (인프라 안정화 후)
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
| 06 | 16a 없이 index 스키마 설계 → cardinality 가정이 암묵적으로 들어감 | 16a를 06 진입 전 필수로 강제 |
| 15b | lifecycle_phase와 integrity_health를 같은 필드에 섞음 | 이중 축 분리 — reconcile은 integrity_health만 변경 |
| 09a | 구두 합의로 완료 선언 | authority map 표 형식 산출물 필수 |
| 09b | 09a와 동시 시작 | 인프라 안정화 조건 명시 — 실패 원인 분리 불가 |
| 06, 12 | data를 P3까지 index에서 고려 안 함 | index 스키마 설계 시 data 자리 확보 |
| 15b | 상태 분류만 하고 응답 행동 누락 | 5가지 상태별 NodeVault 행동 반드시 포함 |
| 15b | Reachable을 빠른 루프에 포함 | 루프 분리 강제 |
| 08 | index lifecycle 테스트 없음 | pkg/index 구현에 테스트 설계 명시 |
| 05, 06 | 두 항목 병렬 시작 | 06 완료 후 05 진입 강제 |
| 03 | 비목표가 PR에 슬며시 포함 | PR 리뷰 체크포인트 작동 |
