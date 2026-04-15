# Harbor Webhook 이벤트 표면 검증 (TODO-15a)

**버전**: v0.1  
**상태**: Harbor 미설치 — 공식 문서 기반 사전 조사

---

## 1. Harbor 지원 Webhook 이벤트 (v2.x 기준)

Harbor는 프로젝트 단위로 webhook을 구성한다.  
아래 이벤트 타입이 지원된다:

| 이벤트 타입 | 트리거 조건 | GC 이후 변화 감지 |
|------------|------------|-----------------|
| `PUSH_ARTIFACT` | image/chart/artifact push 완료 | 없음 |
| `PULL_ARTIFACT` | image pull | 없음 |
| `DELETE_ARTIFACT` | artifact 명시적 삭제 (`docker rmi`, Harbor UI) | **있음** (수동 삭제 시) |
| `QUOTA_EXCEED` | 프로젝트 스토리지 한도 초과 | 없음 |
| `QUOTA_WARNING` | 한도 임박 | 없음 |
| `REPLICATION` | 복제 작업 완료/실패 | 없음 |
| `SCANNING_FAILED` | CVE 스캔 실패 | 없음 |
| `SCANNING_COMPLETED` | CVE 스캔 완료 | 없음 |
| `TAG_RETENTION` | tag retention policy 실행 완료 | 일부 있음 |

---

## 2. 관찰 가능 이벤트 vs. 관찰 불가능 이벤트

### 관찰 가능 (webhook으로 fast path 가능)

| 상황 | 이벤트 | integrity_health 전이 |
|------|--------|----------------------|
| 새 artifact push | `PUSH_ARTIFACT` | → Healthy (reconcile 확인 후) |
| 명시적 삭제 | `DELETE_ARTIFACT` | → Missing |
| Tag retention 실행 후 삭제 | `TAG_RETENTION` | → Missing (확인 필요) |

### 관찰 불가능 (reconcile loop이 커버해야 하는 범위)

| 상황 | 이유 | reconcile 응답 |
|------|------|----------------|
| Harbor GC로 인한 blob 소실 | GC 완료 이벤트 없음 | fast loop에서 manifest 없음 감지 → Missing |
| 부분 push 실패 (spec referrer 없음) | push 실패 이벤트 없음 | fast loop에서 referrer 없음 감지 → Partial |
| 네트워크 단절로 인한 일시 접근 불가 | pull 실패 이벤트 없음 | slow loop에서 pull 실패 감지 → Unreachable |
| Harbor DB 손상 | 이벤트 없음 | 전체 재스캔으로 감지 |
| registry:2 → Harbor 마이그레이션 중 데이터 손실 | 이벤트 없음 | 마이그레이션 후 전체 reconcile |

---

## 3. Reconcile-first 원칙

> **webhook이 없어도 결국 맞춰지는 구조.**

- reconcile loop는 webhook의 유무와 무관하게 주기적으로 돌아야 한다
- webhook은 reconcile을 즉시 trigger하는 fast path일 뿐
- GC 완료 이벤트가 없으므로 reconcile 없이는 Missing 상태를 감지할 수 없다

---

## 4. Harbor 설치 후 검증 항목

Harbor 설치 (TODO-09b 선행 조건) 완료 후 아래를 실제 확인한다:

- [ ] `PUSH_ARTIFACT` 이벤트 payload 구조 확인 (digest 필드 위치)
- [ ] `DELETE_ARTIFACT` 이벤트 payload 구조 확인
- [ ] GC 완료 후 `DELETE_ARTIFACT` 이벤트가 발생하는지 확인
- [ ] Tag retention 실행 후 이벤트 타입 확인
- [ ] Referrers API (`GET /v2/{name}/referrers/{digest}`) 응답 형식 확인
- [ ] Harbor 버전별 webhook 이벤트 차이 확인 (v2.8 vs v2.9+)

---

## 5. Webhook payload 예시 (PUSH_ARTIFACT)

```json
{
  "type": "PUSH_ARTIFACT",
  "occur_at": 1775000000,
  "operator": "admin",
  "event_data": {
    "resources": [{
      "digest": "sha256:abc123...",
      "tag": "2.2.1",
      "resource_url": "registry.example.com/project/bwa-mem2:2.2.1"
    }],
    "repository": {
      "name": "bwa-mem2",
      "full_name": "project/bwa-mem2",
      "type": "IMAGE"
    }
  }
}
```

NodeVault webhook handler는 `event_data.resources[0].digest`를 추출하여  
`pkg/reconcile.Reconciler.ReconcileOne(casHash)` 를 trigger한다.
