# RegisteredTool v0.2 설계

## v0.1 이후 무엇을 바꾸는가

v0.1은 정적 툴 계약의 골격을 잡았다. v0.2는 두 가지만 추가한다.

1. **`display` 섹션** — 사용자 UI 팔레트가 spec을 파싱하지 않고 툴을 표시하기 위한 최소 메타데이터
2. **`identity.stableRef`** — UI 검색·카탈로그 탐색 전용 식별자. 파이프라인 pinning에는 사용하지 않는다.

나머지(lifecycle 확장, supersedes, dockerfileContent, resourceProfile.defaultHint,
ValidatePortConnection RPC)는 이 단계에서 넣지 않는다. 이유는 아래 "의도적 제외" 절에 기록한다.

---

## 설계 원칙 — 변경 없음

### 재현성

파이프라인의 `toolRef`는 **`casHash`로 고정**한다.

`stableRef`는 UI 검색·카탈로그 탐색에만 쓴다. NodeForge는 `stableRef`로 라우팅하거나
최신 revision을 자동 반환하지 않는다. "같은 파이프라인 + 같은 데이터 = 같은 결과"를
보장하는 유일한 방법은 실행 시점에 어떤 이미지가 쓰였는지 casHash로 추적하는 것이다.

### 관심사 분리

리소스 값(CPU/메모리/GPU)은 RegisteredTool 밖에 있다. `ResourceProfile`은 외부
attachable 엔티티이고, 실제 값은 툴 계약의 변화 주기와 독립적으로 관리된다.
"기본 힌트"라는 이름으로도 툴 안에 리소스 값을 넣지 않는다.

---

## v0.2 YAML

```yaml
apiVersion: nodeforge.io/v1alpha1
kind: RegisteredTool
metadata:
  name: bwa-mem-0.7.17   # {tool}-{version}. revision은 name에 넣지 않는다.

spec:
  immutable:

    # ── 1. 정체성 ──────────────────────────────────────────────────────────────
    identity:
      tool: bwa-mem
      version: "0.7.17"
      stableRef: "bwa-mem@0.7.17"
      # stableRef 용도: 카탈로그 탐색, 팔레트 검색, 관리자 UI 조회
      # stableRef 금지: 파이프라인 toolRef. 파이프라인은 반드시 casHash를 참조한다.

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

    # ── 4. 계보 ───────────────────────────────────────────────────────────────
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
          # 리소스 기본값은 여기 없다. 값은 ResourceProfile 엔티티 안에 있다.

        executionPolicy:
          kind: ExecutionPolicy
          required: false

        materializationPolicy:
          kind: MaterializationPolicy
          required: false

    # ── 6. 표시 정보 (v0.2 추가) ─────────────────────────────────────────────
    display:
      label: "BWA-MEM 0.7.17"
      description: "Burrows-Wheeler Aligner — paired-end FASTQ → coordinate-sorted BAM."
      category: "Alignment"
      tags: ["dna", "alignment", "bwa", "short-read"]
      # iconUrl은 넣지 않는다. CDN/업로드 인프라가 없는 상태에서 필드만 앞서 나올 이유가 없다.

status:
  casHash: "sha256:cas-bwa-001"   # 파이프라인 toolRef의 유일한 고정 기준
  phase: Active                    # v0.2 허용 상태: Active | Retracted
  registeredAt: "2026-04-10T10:00:00Z"
  validation:
    phase: Passed                  # Pending | Running | Passed | Failed
    lastValidatedAt: "2026-04-10T10:05:00Z"
```

---

## 현재 코드와의 갭

v0.2 YAML 기준으로 현재 proto (`nodeforge.proto`)와 `catalog.go`에 없는 것을 기록한다.
이 갭이 다음 구현 단계의 입력이다.

### proto — `RegisteredToolDefinition` 메시지

| 필드 | 현재 상태 | 필요한 변경 |
|------|-----------|-------------|
| `input_names []string` | 있음 | `repeated PortSpec inputs`로 교체 |
| `output_names []string` | 있음 | `repeated PortSpec outputs`로 교체 |
| `image_uri` | 있음 (digest 포함) | 유지 |
| `digest` | 있음 | `provenance.digests.image`에 해당. 유지 |
| `environment_spec` | 있음 | `provenance.build.spec`에 해당. 유지 |
| `command` | **없음** | `runtime.command` 추가 필요 |
| `stable_ref` | **없음** | `identity.stable_ref` 추가 필요 |
| `display.*` | **없음** | `DisplaySpec` 메시지 추가 필요 |
| `phase` | **없음** | enum 또는 string 추가 필요 |
| `validation` | **없음** | `ValidationStatus` 서브메시지 추가 필요 |
| `parameters[]` | **없음** | `extensionPoints.parameters` 추가 필요 |

새로 필요한 메시지:

```protobuf
message PortSpec {
  string name     = 1;
  string role     = 2;
  string format   = 3;
  string shape    = 4;  // "single" | "pair"
  bool   required = 5;
  string class    = 6;  // "primary" | "secondary" (output 전용)
  map<string, string> constraints = 7;
}

message DisplaySpec {
  string label       = 1;
  string description = 2;
  string category    = 3;
  repeated string tags = 4;
}

message ValidationStatus {
  string phase             = 1;  // "Pending" | "Running" | "Passed" | "Failed"
  int64  last_validated_at = 2;
  repeated string failures = 3;
}

message ParameterSpec {
  string name     = 1;
  string type     = 2;
  string default  = 3;  // JSON 직렬화 값
  repeated int64 range = 4;  // [min, max], integer 전용
  string env_key  = 5;
}
```

### proto — `RegisterToolRequest` 메시지

| 필드 | 현재 상태 | 필요한 변경 |
|------|-----------|-------------|
| `input_names` / `output_names` | 있음 | `PortSpec`으로 교체 |
| `stable_ref` | **없음** | 추가 필요 |
| `display` | **없음** | `DisplaySpec` 추가 필요 |
| `command` | **없음** | 추가 필요 |
| `parameters` | **없음** | `repeated ParameterSpec` 추가 필요 |

### catalog.go

| 기능 | 현재 상태 | 필요한 변경 |
|------|-----------|-------------|
| casHash로 단건 조회 | 있음 (`GetTool`) | 유지 |
| stableRef로 목록 조회 | **없음** | `ListByStableRef(stableRef string)` 추가 |
| phase 필터 조회 | **없음** | `ListActive()` 추가 (UI 팔레트용) |
| 전체 목록 조회 | 있음 (`List`) | 유지 |

`ListByStableRef`는 casHash 라우팅과 독립적으로 동작한다.
파이프라인 실행 경로는 여전히 `GetTool(casHash)`만 사용한다.

---

## stableRef 사용 범위 명시

모호함을 없애기 위해 stableRef를 쓸 수 있는 곳과 없는 곳을 명시한다.

| 위치 | stableRef 허용 여부 |
|------|-------------------|
| 관리자 카탈로그 탐색 (NodeKit AdminToolList) | 허용 |
| 사용자 UI 팔레트 검색 | 허용 |
| 팔레트에서 툴 드래그 후 파이프라인 저장 | **금지** — casHash로 저장 |
| 파이프라인 실행 시 툴 조회 | **금지** — casHash로 조회 |
| 빌드 로그·감사 추적 | **금지** — casHash로 추적 |

팔레트에서 사용자가 툴을 드래그하면 DagEdit는 해당 툴의 casHash를 파이프라인 노드에 기록한다.
사용자는 "BWA-MEM 0.7.17"을 선택했다고 생각하지만, 저장되는 것은 그 시점의 casHash다.

---

## 의도적 제외 — 이유 포함

### stableRef → 최신 Active revision 자동 라우팅

**제외 이유**: 재현성 원칙과 정면 충돌한다.
파이프라인이 stableRef를 참조하고 NodeForge가 최신 Active revision을 반환하면,
관리자가 같은 tool+version을 리빌드하는 순간 기존 파이프라인이 암묵적으로 다른 툴을 실행한다.
"같은 파이프라인 + 같은 데이터"인데 실행 결과가 달라진다.

### resourceProfile.defaultHint

**제외 이유**: 리소스 정책과 툴 계약을 다시 결합한다.
ResourceProfile은 외부 attachable 엔티티이며 툴 계약과 독립적인 변화 주기를 갖는다.
"힌트"라는 포장이어도 값이 툴 안에 들어오는 순간 두 주기가 섞인다.

### provenance.build.dockerfileContent

**제외 이유**: 문제의식(관리자가 오류를 고치려면 빌드 입력이 필요하다)은 맞지만
해결 방식이 불완전하다. 실제 빌드 입력은 Dockerfile 하나가 아니다.
run.sh, conda spec, COPY 대상 파일, 보조 스크립트가 빠지면
"원문 복원 가능"이라는 환상을 줄 뿐 실제 재빌드는 실패할 수 있다.
올바른 해결은 `buildContextRef` 또는 `sourceDraftRef` 형태의 참조이며,
그것은 아티팩트 스토리지 설계가 선행되어야 하므로 이 단계에서 넣지 않는다.

### 6단계 lifecycle / supersedes / RetractTool RPC

**제외 이유**: 지금 목표는 "정적 툴 계약 고정"이다.
lifecycle 확장과 supersedes는 카탈로그 운영 모델이며,
그 설계는 툴 계약이 안정된 후에 별도로 다룬다.
v0.2 status.phase는 `Active`와 `Retracted` 두 값만 허용한다.

### ValidatePortConnection RPC

**제외 이유**: 유용한 기능이지만 Tool Schema 문서의 범위가 아니다.
이것은 catalog service / validation service 기능이고,
포트 역할(role) 온톨로지 설계와 함께 서비스 API 설계 단계에서 다룬다.

---

## v0.3으로 이월

- lifecycle 확장: Deprecated / Superseded / Retracted 전이, RetractTool RPC
- buildContextRef / sourceDraftRef — 빌드 입력 전체 보존
- stableRef 조회 시 revision 지정 접근 (감사 재현용)
- role 온톨로지 등록 (`RegisterPortRole`)
- ValidatePortConnection RPC
- `shape` 확장: row / list / collection
- iconUrl CDN 통합
