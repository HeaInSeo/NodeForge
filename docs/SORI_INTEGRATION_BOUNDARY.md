# sori 통합 경계 문서 (TODO-13)

**버전**: v0.1  
**상태**: 확정

---

## 1. 현재 상황

sori는 원래 로컬 OCI 스토어 관리와 볼륨 인덱스를 담당하는 독립 패키지다.  
NodeVault가 OCI referrer push를 필요로 하면서, sori에 `referrer.go`가 추가됐다.

```
sori (GitHub: HeaInSeo/sori)
├── volume-index.go     ← sori 본연의 역할 (볼륨 메타 인덱스)
├── config.go           ← Local/Remote 스토어 설정
├── referrer.go         ← NodeVault 요청으로 추가된 OCI referrer push
└── ...
```

---

## 2. 경계 규칙

### sori가 소유하는 것

| 기능 | 설명 |
|------|------|
| OCI 로컬 스토어 생성/관리 | `NewReferrerLocalStore(path)` |
| OCI 원격 레포지터리 연결 | `NewReferrerRemoteRepository(repoRef, ...)` |
| spec referrer push | `PushToolSpecReferrer()`, `PushDataSpecReferrer()` |
| spec JSON 직렬화 | `MarshalSpec(v)` |
| MediaType 상수 | `MediaTypeToolSpec`, `MediaTypeDataSpec` |
| 볼륨 인덱스 관리 | `volume-index.go` (NodeVault와 무관) |

### NodeVault(NodeVault)가 소유하는 것

| 기능 | 설명 |
|------|------|
| artifact index 관리 | `pkg/index` — lifecycle_phase / integrity_health 이중 축 |
| CAS 저장소 | `pkg/catalog` — `.tooldefinition`, `.datadefinition` 파일 |
| 등록 흐름 오케스트레이션 | 빌드 완료 → CAS 저장 → index append → sori referrer push |
| lifecycle_phase 전이 | Retract, Delete — NodeVault only |
| integrity_health 갱신 | reconcile loop — NodeVault 내부 |
| Catalog REST 서비스 | `pkg/catalogrest` |

### sori가 알지 못하는 것 (의존성 역방향 금지)

- `index.Store`, `index.Entry` — NodeVault 내부 구조
- `lifecycle_phase`, `integrity_health` — NodeVault 상태 모델
- Catalog 노출 규칙 — NodeVault 정책

---

## 3. 호출 방향

```
NodeVault (NodeVault)
    │
    │  빌드 완료 후
    ├─► pkg/catalog.SaveWithCasHash()     ← CAS 저장
    ├─► pkg/index.Store.Append()          ← index append
    └─► sori.PushToolSpecReferrer()       ← OCI referrer push (sori 호출)
            │
            └─► Harbor / registry:2
```

NodeVault → sori 단방향.  
sori → NodeVault 호출 없음.

---

## 4. NodeVault–sori API 계약

```go
// sori 패키지가 NodeVault에 제공하는 인터페이스

// ReferrerTarget은 referrer push 대상 스토어/레포지터리다.
type ReferrerTarget interface { oras.Target }

// PushToolSpecReferrer는 toolSpec JSON을 subjectDigest 이미지의 OCI referrer로 push한다.
func PushToolSpecReferrer(
    ctx           context.Context,
    target        ReferrerTarget,
    subjectDigest string,   // "sha256:..." — 이미지 digest
    specJSON      []byte,   // json.Marshal(RegisteredToolDefinition)
) (SpecReferrerResult, error)

// PushDataSpecReferrer는 dataSpec JSON을 subjectDigest의 OCI referrer로 push한다.
func PushDataSpecReferrer(
    ctx           context.Context,
    target        ReferrerTarget,
    subjectDigest string,
    specJSON      []byte,   // json.Marshal(RegisteredDataDefinition)
) (SpecReferrerResult, error)

type SpecReferrerResult struct {
    ReferrerDigest string  // 생성된 referrer manifest의 digest
    SubjectDigest  string  // 연결된 subject image의 digest (입력값 그대로)
    MediaType      string  // MediaTypeToolSpec 또는 MediaTypeDataSpec
}

// MediaType 상수
const (
    MediaTypeToolSpec = "application/vnd.nodevault.toolspec.v1+json"
    MediaTypeDataSpec = "application/vnd.nodevault.dataspec.v1+json"
)
```

---

## 5. NodeVault가 흡수하지 않는 것 (비목표)

- sori의 볼륨 인덱스 로직 (`volume-index.go`) — NodeVault 범위 밖
- sori 설정 파일 파싱 (`config.go`) — sori 내부
- sori의 파이프라인 인덱스 (`pipeline-index.go`) — NodeVault 범위 밖

---

## 6. 향후 계획

| 시점 | 항목 |
|------|------|
| TODO-07 구현 시 | NodeVault build 완료 이벤트 후 `sori.PushToolSpecReferrer()` 호출 연결 |
| Harbor 전환 후 | `NewReferrerRemoteRepository()` 대상을 Harbor로 교체 |
| data artifact (TODO-12) | `sori.PushDataSpecReferrer()` 활용 — 동일 패턴 |
