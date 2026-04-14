# stableRef Cardinality 결정 (TODO-16a)

작성일: 2026-04-14  
상태: **확정** — TODO-06 index 설계 전 필수 선행 결정

---

## 질문 1: stableRef : casHash 카디널리티는 1:1인가 1:N인가?

**결정: 1:N**

같은 `stableRef` (`bwa-mem2@2.2.1`) 아래에 여러 `casHash`가 존재할 수 있다.
예: 동일 버전을 다른 베이스 이미지로 두 번 빌드하면 각각 다른 `casHash`를 갖는다.

index 스키마 영향: `stable_ref` 컬럼은 unique key가 아니다. `cas_hash`만 unique key.

---

## 질문 2: 같은 stableRef 아래 여러 revision 허용 여부

**결정: 허용**

같은 `stableRef` 아래 여러 revision을 기록할 수 있다.
revision 목록 조회는 `ListTools(stable_ref="bwa-mem2@2.2.1")` 로 가능.

`casHash`는 항상 특정 revision을 고정 pin한다.
파이프라인은 `casHash` pin을 사용하므로 revision 다수 존재가 실행 재현성을 해치지 않는다.

---

## 질문 3: active가 하나만 가능한가, 여러 개 가능한가?

**결정: 여러 개 가능 (동시 active 허용)**

같은 `stableRef`에 속한 여러 `casHash`가 동시에 `lifecycle_phase = Active` 상태를 가질 수 있다.
"어느 revision이 현재 추천 버전인가"는 별도 정책(TODO-16b)에서 다룬다.

index 스키마 영향: `lifecycle_phase = Active` 제약을 `stable_ref` 단위로 하나만 허용하는 unique 제약을 걸지 않는다.

---

## 질문 4: index에 `stableRef → current active` 매핑 레코드가 필요한가?

**결정: 현재 불필요. TODO-16b에서 재검토.**

현재는 `ListTools(stable_ref=...)` + `lifecycle_phase = Active` 필터 조합으로 충분.
"current active" 단수 포인터가 필요한 시점은 UI revision 표시 정책(TODO-16b)이 결정된 이후.

---

## index 스키마 영향 요약

| 설계 결정 | index 스키마 영향 |
|-----------|-----------------|
| stableRef:casHash = 1:N | `stable_ref`는 unique constraint 없음 |
| revision 다수 허용 | 동일 `stable_ref`에 여러 row 가능 |
| 동시 active 허용 | `(stable_ref, lifecycle_phase='Active')` unique 제약 없음 |
| current active 매핑 불필요 | 별도 pointer 컬럼/테이블 없음 (TODO-16b까지) |

---

## 완료 기준 체크리스트 (TODO-16a)

- [x] stableRef:casHash 카디널리티 결정 (1:N)
- [x] revision 허용 여부 결정 (허용)
- [x] active 단수/복수 결정 (복수 허용)
- [x] stableRef→current active 매핑 레코드 필요 여부 결정 (불필요, TODO-16b까지 유예)
- [x] index 스키마 설계에 반영될 수 있는 수준으로 결정 닫힘
