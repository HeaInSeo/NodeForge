# NodeVault Index Schema (TODO-06)

작성일: 2026-04-14  
상태: **확정**  
선행 조건: TODO-16a (stableRef cardinality), TODO-02 (stableRef/casHash 경계)

---

## 1. 설계 원칙

index는 Harbor 저장 사실이 아니라 **플랫폼의 승인 사실**을 기록하는 원장이다.

- Harbor에 이미지가 있다고 해서 Catalog에 노출되는 것이 아니다.
- `lifecycle_phase = Active`인 항목만 Catalog에 노출된다.
- 모든 index read/write는 `pkg/index`(`Store` 타입)를 경유한다. 직접 파일 접근 금지.

---

## 2. 저장 방식

| 항목 | 값 |
|------|-----|
| 파일 경로 | `assets/index/vault-index.json` (기본) |
| 환경변수 오버라이드 | `INDEX_DIR` |
| 형식 | JSON (들여쓰기 포함, human-readable) |
| 동시성 | `sync.RWMutex`로 in-process 보호 |
| 원자성 | 단일 파일 write (`os.WriteFile`) |

---

## 3. 스키마

### indexFile (최상위)

```json
{
  "schema_version": 1,
  "entries": [ ... ]
}
```

### Entry (항목 단위)

| 필드 | 타입 | 키 종류 | 설명 |
|------|------|---------|------|
| `cas_hash` | string | **Primary key** | SHA256(spec JSON without cas_hash). pipeline toolRef pin. |
| `artifact_kind` | string | — | `"tool"` \| `"data"` |
| `stable_ref` | string | **조회 키** | UI 검색 키. `tool_name@version`. 1:N (여러 casHash 가능). |
| `tool_name` | string | — | stable_ref 구성 요소 |
| `version` | string | — | stable_ref 구성 요소 |
| `image_digest` | string | — | Harbor 이미지 OCI digest |
| `spec_referrer_digest` | string | — | OCI referrer spec digest (TODO-07 이후 채워짐) |
| `lifecycle_phase` | string | — | `Pending` \| `Active` \| `Retracted` \| `Deleted` |
| `integrity_health` | string | — | `Healthy` \| `Partial` \| `Missing` \| `Unreachable` \| `Orphaned` |
| `registered_at` | time | — | 등록 시각 (UTC) |
| `lifecycle_updated_at` | time | — | 마지막 lifecycle_phase 변경 시각 |
| `health_checked_at` | time | — | 마지막 integrity_health 갱신 시각 |

---

## 4. 상태 이중 축

```
lifecycle_phase          integrity_health
────────────────         ────────────────────
Pending                  Healthy
Active           ✗       Partial
Retracted        절대     Missing
Deleted          혼합     Unreachable
                         Orphaned
```

**변경 주체 분리:**

| 축 | 변경 주체 | 변경 메서드 | 목적 |
|----|-----------|------------|------|
| `lifecycle_phase` | NodeVault 명시적 호출 | `SetLifecyclePhase` | Catalog 노출 결정 |
| `integrity_health` | reconcile loop | `SetIntegrityHealth` | 알람/모니터링 |

**Catalog 노출 규칙**: `lifecycle_phase = Active`만. `integrity_health`는 노출에 영향 없음.

교차 상태 예시:

| lifecycle_phase | integrity_health | 의미 | 처리 |
|-----------------|------------------|------|------|
| Active | Healthy | 정상 | Catalog 노출 |
| Active | Partial | 공식 artifact, spec referrer 없음 | 알람, Catalog 노출 유지 |
| Active | Missing | 심각 — image Harbor에서 사라짐 | 알람, Catalog 노출 유지 |
| Active | Unreachable | 일시적 접근 불가 | 모니터링, Catalog 노출 유지 |
| Retracted | * | 의도적 숨김 | Catalog 제외 |
| Deleted | * | 완전 퇴출 | 모든 경로에서 제외 |
| * | Orphaned | spec referrer는 있으나 image 없음 | 알람, 자동 삭제 금지 |

---

## 5. stableRef:casHash 카디널리티 (TODO-16a 결정 반영)

- `stable_ref`는 **unique constraint 없음** — 여러 casHash가 같은 stable_ref를 가질 수 있음
- 동일 stable_ref에 여러 revision 허용
- 동시에 여러 `lifecycle_phase = Active` 허용 (단수 Active 제약 없음)
- `stable_ref → current active` 단수 포인터는 TODO-16b까지 유예

---

## 6. lifecycle_phase 전이 규칙

```
Pending → Active      : L4 통과 후 NodeVault RegisterTool 완료
Active  → Retracted   : 운영자 Retract 요청
Retracted → Active    : 운영자 Restore 요청 (재활성화)
Retracted → Deleted   : 운영자 Delete 확인 + Harbor GC 완료
```

`Pending → Retracted`, `Active → Deleted` 직접 전이는 허용하지 않는다(운영 사고 방지).

---

## 7. integrity_health 전이 규칙 (reconcile 관찰)

```
*   → Healthy      : image O + spec referrer O + Exists + Attached + Complete
*   → Partial      : image O + spec referrer X
*   → Missing      : image X + spec referrer X
*   → Unreachable  : Harbor API 접근 실패 (일시적)
*   → Orphaned     : image X + spec referrer O
```

reconcile 루프 분리:

| 루프 | 주기 | 확인 항목 |
|------|------|----------|
| 빠른 루프 | 주기적 (짧은 간격) | Exists + Attached + Complete → integrity_health 갱신 |
| 느린 루프 | 주기적 (긴 간격) 또는 on-demand | Reachable (실제 pull) 검증 |

---

## 8. data artifact 지원 (P3 예약)

`artifact_kind = "data"` 항목은 현재 스키마에 자리가 확보되어 있다.  
동일한 lifecycle_phase / integrity_health 이중 축이 data artifact에도 적용된다.  
P3(TODO-12) 구현 전까지 `KindData` 상수는 정의되어 있으나 production 경로에서 사용되지 않는다.

---

## 9. CAS와의 관계

| 저장소 | 역할 | 상태 |
|--------|------|------|
| `assets/catalog/{casHash}.tooldefinition` | 전체 spec JSON (현재 사용 중) | 유지 — pkg/index 완성 전까지 |
| `assets/index/vault-index.json` | 승인 원장 (이 문서) | TODO-08 구현 완료 후 정식 운영 |

전환 완료 후 `pkg/catalog` CAS는 `pkg/index`로 대체될 예정이지만 지금 당장 삭제하지 않는다.

---

## 10. pkg/index 공개 API (TODO-08 구현 기준)

```go
// 등록
func (s *Store) Append(e Entry) error

// 조회
func (s *Store) GetByCasHash(casHash string) (Entry, error)
func (s *Store) ListByStableRef(stableRef string) ([]Entry, error)
func (s *Store) ListActive() ([]Entry, error)
func (s *Store) All() ([]Entry, error)

// lifecycle_phase 전이 — NodeVault 명시적 호출만
func (s *Store) SetLifecyclePhase(casHash string, phase LifecyclePhase) error

// integrity_health 전이 — reconcile loop만
func (s *Store) SetIntegrityHealth(casHash string, health IntegrityHealth) error
```

---

## 11. 완료 기준 체크리스트 (TODO-06)

- [x] index 스키마 문서화 (이 파일)
- [x] 이중 축 상태 모델 (`lifecycle_phase` / `integrity_health`) 스키마에 반영
- [x] TODO-16a cardinality 모델 반영 (stableRef:casHash = 1:N, unique 제약 없음)
- [x] lifecycle_phase 전이 규칙 명문화
- [x] integrity_health 전이 규칙 명문화 (reconcile 관찰)
- [x] stableRef 기준 조회 지원 (`ListByStableRef`)
- [x] casHash 기준 역조회 지원 (`GetByCasHash`)
- [x] CAS와의 관계 정의 (병존, 전환 예정)
- [x] data artifact를 위한 자리 확보 (`KindData`, `artifact_kind` 필드)
- [x] pkg/index 구현 완료 (Store + 15개 테스트 통과)
