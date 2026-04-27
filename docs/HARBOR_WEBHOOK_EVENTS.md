# Harbor Webhook 이벤트 표면 문서 (TODO-15a)

**버전**: v1.0  
**상태**: Harbor 운영 중 (harbor.10.113.24.96.nip.io) — 문서 기반 완료, live 페이로드 검증은 §4 참조

---

## 1. Harbor 지원 Webhook 이벤트 (v2.x 기준)

Harbor는 프로젝트 단위로 webhook을 구성한다.

| 이벤트 타입 | 트리거 조건 | NodeVault 처리 |
|------------|------------|----------------|
| `PUSH_ARTIFACT` | image/artifact push 완료 | integrity_health → Partial |
| `PULL_ARTIFACT` | image pull | 무시 |
| `DELETE_ARTIFACT` | artifact 명시적 삭제 | integrity_health → Missing |
| `QUOTA_EXCEED` | 스토리지 한도 초과 | 무시 |
| `QUOTA_WARNING` | 한도 임박 | 무시 |
| `REPLICATION` | 복제 작업 완료/실패 | 무시 |
| `SCANNING_FAILED` | CVE 스캔 실패 | 무시 |
| `SCANNING_COMPLETED` | CVE 스캔 완료 | 무시 |
| `TAG_RETENTION` | tag retention policy 실행 | 무시 (reconcile 루프가 감지) |

---

## 2. GC 완료 이벤트 — 없음

**Harbor 2.x는 GC(Garbage Collection) 완료 이벤트를 webhook으로 제공하지 않는다.**

GC는 Harbor 내부 백그라운드 작업으로, 완료 시 외부로 이벤트를 발송하지 않는다.  
GC로 인해 물리 삭제된 이미지는 webhook으로 감지 불가능하다.

**NodeVault 대응**: 주기적 reconcile loop(`FastRun` 5분 주기)이 GC 이후 상태를 감지한다.

---

## 3. 관찰 가능 vs. 관찰 불가능 이벤트

### 관찰 가능 (webhook fast path 처리)

| 상황 | 이벤트 | integrity_health 전이 |
|------|--------|----------------------|
| 새 artifact push | `PUSH_ARTIFACT` | → Partial (spec referrer 미첨부 초기화) |
| 명시적 삭제 | `DELETE_ARTIFACT` | → Missing |

### 관찰 불가능 (reconcile loop이 커버해야 하는 범위)

| 상황 | 이유 | reconcile 응답 |
|------|------|----------------|
| Harbor GC로 인한 blob 소실 | **GC 완료 이벤트 없음** | FastRun: manifest 없음 → Missing |
| 부분 push 실패 (spec referrer 없음) | push 실패 이벤트 없음 | FastRun: referrer 없음 → Partial |
| 네트워크 단절 일시 접근 불가 | pull 실패 이벤트 없음 | SlowRun: pull 실패 → Unreachable |
| webhook 전송 실패 (네트워크 단절) | event 소실 | FastRun 5분 주기 보정 |

---

## 4. Reconcile-first 원칙

> **webhook이 없어도 결국 맞춰지는 구조.**

- reconcile loop는 webhook 유무와 무관하게 주기적으로 동작 (FastRun 5분 / SlowRun 30분)
- webhook은 reconcile을 즉시 trigger하는 fast path일 뿐
- GC 완료 이벤트가 없으므로 reconcile 없이는 GC 결과를 감지할 수 없다

---

## 5. NodeVault Webhook 엔드포인트

| 항목 | 값 |
|------|-----|
| 엔드포인트 | `POST http://100.123.80.48:8082/webhook/harbor` |
| 포트 | `:8082` (기본값, `NODEVAULT_WEBHOOK_ADDR` env로 변경 가능) |
| 구현 | `pkg/webhook/handler.go` → `webhook.NewHandler(indexStore)` |

**Harbor 설정 방법**:
1. Harbor 관리자 콘솔 → Projects → library → Webhooks
2. Notify Type: HTTP
3. Endpoint URL: `http://100.123.80.48:8082/webhook/harbor`
4. Event Types: `Artifact pushed`, `Artifact deleted` 선택

---

## 6. Webhook payload 예시 (DELETE_ARTIFACT)

```json
{
  "type": "DELETE_ARTIFACT",
  "occur_at": 1713513600,
  "operator": "system",
  "event_data": {
    "resources": [{
      "digest": "sha256:abc123...",
      "tag": "latest",
      "resource_url": "harbor.10.113.24.96.nip.io/library/bwa:latest"
    }],
    "repository": {
      "name": "bwa",
      "namespace": "library",
      "full_name": "library/bwa",
      "type": "IMAGE"
    }
  }
}
```

NodeVault webhook handler는 `event_data.resources[*].digest`를 index 조회 키로 사용한다.

---

## 7. Harbor 설치 후 live 검증 항목

| 항목 | 상태 |
|------|------|
| `PUSH_ARTIFACT` 페이로드 구조 확인 (digest 필드 위치) | 미완료 (live 테스트 필요) |
| `DELETE_ARTIFACT` 페이로드 구조 확인 | 미완료 (live 테스트 필요) |
| GC 완료 후 `DELETE_ARTIFACT` 이벤트 발생 여부 | 미완료 — GC webhook 없음 확인 필요 |
| Referrers API (`GET /v2/{name}/referrers/{digest}`) 응답 형식 확인 | 미완료 |
| Harbor 버전 확인 | 미완료 (Helm 설치 채널 기준 최신) |

live 검증은 seoy 호스트에서 NodeVault + Harbor 동시 실행 후 수행한다.
