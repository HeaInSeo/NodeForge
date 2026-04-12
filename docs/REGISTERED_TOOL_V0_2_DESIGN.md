# RegisteredTool v0.2 설계

## 배경 — v0.1이 부족한 이유

v0.1은 정적 툴 계약(static tool contract)의 기반을 잘 잡았다. 그러나 관리자→사용자 UI 전달 경로를 실제로 지탱하기 위해 필요한 세 가지 축이 빠져 있다.

1. **관리자가 오류를 수정할 수 없다.** Dockerfile 원문이 없으므로 빌드가 실패했을 때 무엇을 고쳐야 하는지 알 수 없다.
2. **사용자 UI가 툴을 표시할 수 없다.** 레이블, 설명, 카테고리, 태그가 없어 팔레트 렌더링이 불가능하다.
3. **`toolRef`가 불안정하다.** `casHash`는 리빌드 시 변경되므로 파이프라인이 이것을 참조하면 저장된 파이프라인이 리빌드 즉시 망가진다.

v0.2는 이 세 가지 축을 보완하고, 관리자 라이프사이클 워크플로를 명시한다.

---

## 핵심 결정

### 결정 1 — stableRef / revision 분리

```
identity.stableRef = tool + version          ← 사람이 참조하는 불변 이름
identity.revision   = 1, 2, 3, …            ← 같은 tool+version 내 리빌드 카운터
casHash             = SHA256(spec 직렬화)     ← 콘텐츠 식별자, 리빌드마다 바뀜
```

파이프라인의 `toolRef`는 `stableRef`를 사용한다(`casHash`를 사용하지 않는다).  
NodeForge는 `stableRef`로 조회 시 `phase: Active`인 revision 중 가장 최신을 반환한다.

### 결정 2 — Dockerfile 원문 보존

`provenance.build.dockerfileContent`에 원문을 저장한다.  
관리자가 NodeKit에서 툴을 열면 이 필드의 내용이 에디터에 복원되어 수정 후 재빌드가 가능하다.

### 결정 3 — display 섹션

사용자 UI가 spec을 파싱하지 않고도 팔레트를 렌더링하기 위한 최소 표시 정보를 별도 섹션에 둔다.

### 결정 4 — lifecycle 상태 머신

```
Registered → Validating → Active
                        → Retracted   (관리자 강제 회수)
Active     → Deprecated               (새 revision 등록 시 이전 revision에 자동 적용)
           → Superseded               (동일 stableRef의 더 높은 revision이 Active가 될 때)
           → Retracted               (관리자 강제 회수)
```

### 결정 5 — 포트 호환성 검증 책임

NodeForge가 `ValidatePortConnection` RPC를 제공한다.  
DagEdit가 파이프라인을 저장하기 전에 이 RPC를 호출하여 upstream.output → downstream.input 연결이 유효한지 확인한다.  
NodeKit(admin UI)은 이 RPC를 사용하지 않는다 — NodeKit은 단일 툴 계약만 본다.

---

## v0.2 전체 YAML

```yaml
apiVersion: nodeforge.io/v1alpha2
kind: RegisteredTool
metadata:
  name: bwa-mem-0.7.17-r1   # {tool}-{version}-r{revision}

spec:
  immutable:

    # ── 1. 정체성 ──────────────────────────────────────────────────────────────
    identity:
      tool: bwa-mem
      version: "0.7.17"
      revision: 1              # 같은 tool+version 내 리빌드 카운터 (1부터 시작)
      stableRef: "bwa-mem@0.7.17"   # 파이프라인 toolRef의 기준값

    # ── 2. 런타임 ─────────────────────────────────────────────────────────────
    runtime:
      image: harbor.internal/tools/bwa-mem@sha256:img-bwa-001
      command: ["/app/run.sh"]

    # ── 3. 포트 ───────────────────────────────────────────────────────────────
    ports:
      inputs:
        - name: reads
          role: sample-fastq
          format: fastq
          shape: pair
          required: true

        - name: reference
          role: reference-fasta
          format: fasta
          shape: single
          required: true

      outputs:
        - name: aligned_bam
          role: aligned-bam
          format: bam
          shape: single
          class: primary
          constraints:
            sorted: coordinate
            indexRecommended: true

        - name: run_log
          role: execution-log
          format: text
          shape: single
          class: secondary

    # ── 4. 계보 (Dockerfile 원문 포함) ───────────────────────────────────────
    provenance:
      toolDefinitionId: "tdef-bwa-mem-001"
      digests:
        image: sha256:img-bwa-001
        package: sha256:pkg-bwa-001
      build:
        recipeType: conda
        spec: |
          name: bwa
          channels: [bioconda, conda-forge]
          dependencies:
            - bwa=0.7.17=h5bf99c6_8
        dockerfileContent: |
          FROM ubuntu:22.04 AS builder
          RUN apt-get update && apt-get install -y wget bwa=0.7.17 && apt-get clean
          COPY run.sh /app/run.sh
          RUN chmod +x /app/run.sh
          FROM ubuntu:22.04
          COPY --from=builder /usr/bin/bwa /usr/bin/bwa
          COPY --from=builder /app/run.sh /app/run.sh
          ENTRYPOINT ["/app/run.sh"]
      supersedes: null  # 이전 revision의 stableRef + revision. 첫 revision이면 null.
      # 예시 (revision 2라면): "bwa-mem@0.7.17#1"

    # ── 5. 확장 포인트 ────────────────────────────────────────────────────────
    extensionPoints:
      parameters:
        - name: threads
          type: integer
          default: 4
          range: [1, 64]
          envKey: BWA_THREADS

      attachments:
        resourceProfile:
          kind: ResourceProfile
          required: false
          defaultHint:           # K8s Job 제출 시 요청값 기본값 (override 가능)
            cpu: "2"
            memory: "4Gi"
            gpu: "0"

        executionPolicy:
          kind: ExecutionPolicy
          required: false

        materializationPolicy:
          kind: MaterializationPolicy
          required: false

    # ── 6. 표시 정보 (UI 팔레트용) ──────────────────────────────────────────
    display:
      label: "BWA-MEM 0.7.17"
      description: "Burrows-Wheeler Aligner — short-read DNA 정렬. paired-end FASTQ → BAM."
      category: "Alignment"
      tags: ["dna", "alignment", "bwa", "short-read"]
      iconUrl: null               # 선택 사항. 없으면 UI가 category 기본 아이콘 사용.

status:
  casHash: "sha256:cas-bwa-001"
  phase: Active           # Registered | Validating | Active | Deprecated | Superseded | Retracted
  registeredAt: "2026-04-10T10:00:00Z"
  activeAt: "2026-04-10T10:07:00Z"    # phase가 Active로 전환된 시각
  deprecatedAt: null
  retractedAt: null
  retractReason: null     # Retracted 시 관리자가 입력한 사유
  validation:
    phase: Passed         # Pending | Running | Passed | Failed
    lastValidatedAt: "2026-04-10T10:05:00Z"
    failures: []          # ValidationFailure 목록 (phase: Failed일 때 채워짐)
```

---

## 필드별 채택 이유 (v0.1 대비 변경/추가 항목)

### identity.revision + stableRef

`casHash`는 콘텐츠 식별자다. 리빌드하면 바뀐다.  
파이프라인이 `casHash`를 `toolRef`로 사용하면 관리자가 Dockerfile을 한 줄만 고쳐도 파이프라인 전체가 깨진다.  
`stableRef`(`tool@version`)는 리빌드 전후로 동일하다. NodeForge는 `stableRef`로 조회 시 Active revision 최신을 반환한다.  
`revision`은 같은 `stableRef` 내에서 몇 번째 빌드인지 추적한다. 관리자 감사 로그에도 쓰인다.

### provenance.build.dockerfileContent

빌드 성공 후 NodeForge가 이 필드에 Dockerfile 원문을 저장한다.  
관리자가 NodeKit에서 툴을 열면 이 필드가 Dockerfile 에디터에 복원된다.  
Dockerfile 없이는 관리자가 오류를 수정하고 재빌드할 방법이 없다.

### provenance.supersedes

revision N을 등록하면 NodeForge가 revision N-1의 `supersedes` 포인터를 자동으로 채운다.  
이로써 "이 버전이 무엇을 대체했는가"라는 계보가 RegisteredTool 안에서 자기 기술(self-describing)된다.

### display

사용자 UI(DagEdit의 팔레트)가 spec을 파싱하지 않고도 툴을 표시할 수 있어야 한다.  
`label`은 팔레트 카드 제목, `description`은 툴팁, `category`는 그룹핑, `tags`는 검색에 쓰인다.  
이 필드들은 `spec.immutable` 안에 있으므로 등록 후 변경되지 않는다.

### extensionPoints.attachments.resourceProfile.defaultHint

K8s Job을 제출할 때 ResourceProfile이 첨부되지 않으면 기본값으로 사용된다.  
cpu/memory/gpu 세 값만 둔다. 이것이 없으면 Job이 노드에 스케줄되지 못하거나 과다 요청된다.  
실제 ResourceProfile이 첨부되면 이 힌트는 무시된다.

### status.phase 상태 머신

`phase`가 없으면 관리자가 "이 툴을 써도 되는가?"를 판단할 수 없다.  
v0.2의 6개 상태는 최소한이다. 각 상태 전환은 NodeForge가 처리하며, 관리자가 직접 변경하는 상태는 `Retracted`뿐이다.

---

## 관리자 재빌드 워크플로

```
1. 관리자가 NodeKit에서 기존 툴 선택
   → NodeKit이 GetTool(casHash) 호출
   → RegisteredTool.provenance.build.dockerfileContent를 에디터에 복원

2. 관리자가 Dockerfile 수정 후 재빌드 요청
   → NodeKit이 BuildRequest 전송 (dockerfileContent 포함, stableRef 포함)
   → NodeForge가 이미지 빌드 + L3/L4 통과 후 RegisteredTool 생성
     - revision = 이전 revision + 1
     - supersedes = "bwa-mem@0.7.17#N-1"
   → 이전 revision status.phase → Superseded (자동)
   → 새 revision status.phase → Active

3. 파이프라인이 stableRef = "bwa-mem@0.7.17"을 참조하고 있으면
   → NodeForge는 Active revision 최신(새 revision)을 반환
   → 파이프라인 YAML 변경 없이 자동으로 새 이미지로 실행됨
```

```
Retract 워크플로:
1. 관리자가 특정 revision을 회수 결정 (보안 취약점 발견 등)
2. NodeKit이 RetractTool(casHash, reason) 호출
3. NodeForge: 해당 revision phase → Retracted, retractReason 기록
4. 해당 revision이 Active였다면 NodeForge는 이전 revision 중 Active 상태인 것으로 자동 fallback
   → 없으면 stableRef 전체가 일시 중단 상태가 됨 (NodeForge가 경고 반환)
```

---

## 포트 호환성 검증 — ValidatePortConnection RPC

포트 연결 검증은 두 툴의 스키마를 모두 알아야 한다. 이 책임은 NodeForge가 진다.

```protobuf
rpc ValidatePortConnection(ValidatePortConnectionRequest)
    returns (ValidatePortConnectionResponse);

message ValidatePortConnectionRequest {
  string upstream_cas_hash = 1;    // 또는 upstream_stable_ref
  string upstream_output_name = 2;
  string downstream_cas_hash = 3;  // 또는 downstream_stable_ref
  string downstream_input_name = 4;
}

message ValidatePortConnectionResponse {
  bool compatible = 1;
  repeated string violations = 2;  // 불호환 사유 목록
}
```

검증 규칙 (최소):
- upstream output.role == downstream input.role
- upstream output.format == downstream input.format
- shape 호환: single→single (O), pair→single (X), single→pair (X), pair→pair (O)

DagEdit는 파이프라인 엣지 추가 시 이 RPC를 호출한다. NodeKit은 이 RPC를 사용하지 않는다.

---

## v0.1에서 v0.2로 달라진 것 요약

| 항목 | v0.1 | v0.2 |
|------|------|------|
| 파이프라인 참조 기준 | casHash (불안정) | stableRef + revision (안정) |
| Dockerfile 보존 | 없음 | provenance.build.dockerfileContent |
| 관리자 재빌드 후 계보 | 없음 | provenance.supersedes |
| UI 표시 정보 | 없음 | display (label/description/category/tags) |
| 리소스 기본값 | 없음 | resourceProfile.defaultHint |
| 라이프사이클 상태 | phase: Registered only | 6개 상태 + 전환 시각 기록 |
| 포트 검증 책임 | 미정 | NodeForge ValidatePortConnection RPC |
| 회수(Retract) 워크플로 | 없음 | RetractTool RPC + retractReason |

---

## v0.2에서 의도적으로 제외한 것 (v0.1 제외 목록 유지)

v0.1에서 제외했던 항목은 그대로 유지한다. 추가로 v0.2에서도 제외:

- `ValidatePortConnection`의 semantic typing 확장 (role 온톨로지 체계)
- stableRef 조회 시 revision 지정 접근 (항상 최신 Active 반환)
- `display.iconUrl`의 실제 CDN 업로드 워크플로
- Retract 후 파이프라인 자동 실행 차단 메커니즘 (DagEdit 책임)
- 포트의 `shape: row / list / collection` 확장 (v0.1 미완으로 이월)

---

## v0.3으로 넘기는 것

- stableRef @ revision 지정 접근 (audit replay 시 특정 revision 재현)
- 포트 role 온톨로지 등록 RPC (`RegisterPortRole`)
- `shape` 확장: row / list / collection
- attachment 대상(ExecutionPolicy, MaterializationPolicy)의 구체 스키마
- `display.iconUrl` 업로드 및 CDN 통합
- 파이프라인 실행 시 Retracted 툴 자동 차단 (DagEdit 연동)

---

## 구현 순서 제안

1. **proto 변경**: `RegisteredToolDefinition`에 revision, stableRef, display, dockerfileContent, supersedes, defaultHint, activeAt/deprecatedAt/retractedAt/retractReason 필드 추가
2. **catalog.go 변경**: stableRef 인덱스 추가, ListByStableRef + GetActiveByStableRef 메서드
3. **service.go 변경**: 빌드 성공 후 revision 자동 증가, 이전 revision → Superseded 처리
4. **ValidatePortConnection RPC 추가**
5. **RetractTool RPC 추가**
6. **NodeKit**: GetTool 응답에서 dockerfileContent를 에디터에 복원하는 "툴 불러오기" 기능
