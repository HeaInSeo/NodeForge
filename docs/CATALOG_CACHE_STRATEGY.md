# Catalog Cache Strategy (TODO-11)

작성일: 2026-04-15  
상태: **확정**  
선행 조건: TODO-10

---

## 1. 현재 구조 (단일 프로세스)

```
NodeVault 프로세스
├── gRPC ToolRegistryService  → index.Store (write)
├── Catalog REST Server       → index.Store (read-only)
└── index.Store               → vault-index.json (OS 파일 I/O)
```

`index.Store`는 `sync.RWMutex`로 in-process 보호된다. 같은 프로세스 내에서는 항상 일관된 상태를 읽는다.

**현재 단계에서 별도 캐시 레이어는 불필요하다.** 파일 I/O가 병목이 될 경우 in-memory index를 Store가 이미 유지하므로 실질적 지연은 없다.

---

## 2. Catalog 노출 일관성 보장

### 2.1 lifecycle_phase 변경 후 반영

| 상황 | 반영 지연 | 설명 |
|------|-----------|------|
| 같은 프로세스 내 요청 | **즉시** | `SetLifecyclePhase` 호출 후 다음 `ListActive` 호출에 즉시 반영 |
| 프로세스 재시작 후 | **즉시** | `load()` 시 파일에서 최신 상태 읽음 |
| 다중 프로세스 (미래) | TTL 또는 invalidation 필요 | TODO-09b 이후 별도 결정 |

### 2.2 integrity_health 변경 후 반영 (reconcile loop)

`integrity_health` 변경은 Catalog 노출에 영향을 주지 않는다.
따라서 reconcile loop의 `SetIntegrityHealth` 호출은 ListActive 결과를 변경하지 않는다.

**이 규칙은 구현 수준에서 보장된다**: `ListActive()`는 `lifecycle_phase == Active` 조건만 검사하며 `integrity_health`를 확인하지 않는다.

---

## 3. TTL / Invalidation 정책

### 현재 단계 (단일 프로세스)

TTL 불필요. 인메모리 index가 곧 캐시이며 파일 기반 store와 항상 동기화된다.

### TODO-09b 이후 (Catalog가 별도 서비스로 분리될 경우)

| 이벤트 | 권장 invalidation |
|--------|------------------|
| RegisterTool (Active 등록) | 즉시 push-invalidation |
| Retract / Delete | 즉시 push-invalidation |
| integrity_health 변경 | **캐시 갱신 불필요** (노출 기준과 무관) |
| Harbor GC 이후 | integrity_health reconcile이 처리 — 노출에 영향 없음 |

TTL 방어선: 30초 이하. 운영자 Retract 후 30초 이내 Catalog에서 제외됨을 보장.

---

## 4. 핵심 불변 규칙

> **integrity_health 변화는 Catalog 노출에 영향을 주지 않는다.**

이 규칙은 캐시 설계와 무관하게 항상 성립한다:
- `ListActive()`는 `lifecycle_phase = Active` 기준만 사용
- integrity_health가 Partial / Missing / Unreachable이어도 Active 툴은 Catalog에 노출된다
- Catalog 노출 제거는 오직 NodeVault의 명시적 Retract / Delete 호출로만 가능

---

## 5. 완료 기준 체크리스트 (TODO-11)

- [x] 캐시 TTL 또는 invalidation 정책 문서화
- [x] lifecycle_phase 변경(Retract 등) 후 Catalog 반영 지연 허용 범위 명시 (단일 프로세스: 즉시)
- [x] **integrity_health 변화는 Catalog 노출에 영향을 주지 않음을 구현 수준에서 확인** (`ListActive` 조건 = lifecycle_phase only)
