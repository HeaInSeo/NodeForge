# RegisteredTool v0.1

## 한 줄 결론

`RegisteredTool v0.1`은 지금 이 시점에 "정적 툴 계약(static tool contract)"으로 고정해도 된다.

## 왜 지금 RegisteredTool를 먼저 고정해야 하는가

파이프라인 표현은 이후 여러 단계로 나뉘더라도, 그 모든 단계는 결국 "툴이 무엇을 요구하고 무엇을 생산하는가"라는 기준 위에 서야 한다. 이 기준이 흔들리면 `toolRef`의 의미도 흔들리고, 이후의 바인딩, 검증, 시각화, 실행 구체화도 함께 흔들린다.

따라서 지금은 파이프라인 전체 표현을 넓히는 것보다 먼저 `RegisteredTool`를 정적 계약으로 고정하는 것이 맞다. 이 단계에서 필요한 것은 실행 전략이 아니라 계약 안정성이다. 즉, 입력 포트, 출력 포트, 허용 파라미터, 계보 정보, 확장 인터페이스만 남기고, 저장 방식이나 실행 방식에 관한 세부는 의도적으로 제외해야 한다.

## RegisteredTool v0.1 최종 YAML

```yaml
apiVersion: nodeforge.io/v1alpha1
kind: RegisteredTool
metadata:
  name: bwa-mem

spec:
  immutable:
    identity:
      tool: bwa-mem
      version: "0.7.17"

    runtime:
      image: harbor.internal/tools/bwa-mem@sha256:img-bwa-001
      command: ["/app/run.sh"]

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

        executionPolicy:
          kind: ExecutionPolicy
          required: false

        materializationPolicy:
          kind: MaterializationPolicy
          required: false

status:
  casHash: "sha256:cas-bwa-001"
  phase: Registered
  registeredAt: "2026-04-10T10:00:00Z"
  validation:
    phase: Passed
    lastValidatedAt: "2026-04-10T10:05:00Z"
```

## 필드별 채택 이유

### identity

- `tool`과 `version`만 남긴다.
- 이것이 사람이 이해하는 최소 정체성이다.
- `toolDefinitionId`는 정체성보다 계보와 추적성에 가깝기 때문에 `provenance`로 내린다.
- 이로써 identity는 단순하고 안정적으로 유지된다.

### runtime

- `image`와 `command`만 둔다.
- 툴이 어떤 실행 artifact와 진입 명령을 갖는지 선언하는 최소 계약이다.
- 실행 큐, 재시도, 자원량, 프로파일 같은 운영 정책은 여기 넣지 않는다.
- `runtime`은 실행 방법의 정책이 아니라 툴 artifact의 고정된 기초 정보다.

### ports.inputs

- `name / role / format / shape / required`만 둔다.
- 이것이 "무엇을 요구하는가"를 표현하는 최소 계약이다.
- `role`은 의미적 호환성 검증의 기준이고, `format`은 데이터 형식 검증의 기준이다.
- `shape`는 v0.1에서 `single / pair` 수준만 유지한다.
- 저장소 종류나 바인딩 방식과 무관하게 읽혀야 하므로 storage-agnostic를 유지한다.

### ports.outputs

- `name / role / format / shape / class / constraints`를 둔다.
- 이것이 "무엇을 생산하는가"를 표현하는 최소 계약이다.
- `class`는 `primary / secondary` 구분을 유지해 downstream 해석의 최소 의미를 남긴다.
- `constraints`는 산출물 의미를 좁혀주는 계약이다.
- 경로, 게시 방식, 물질화 정책은 넣지 않는다.

### provenance

- 계보와 재현성 메타데이터를 수용한다.
- `toolDefinitionId`는 사람과 시스템이 원 정의를 추적하는 데 유용하므로 여기 둔다.
- `digests.image / digests.package`는 빌드 artifact의 추적 정보를 제공한다.
- `build`는 계약의 본체라기보다 생성 배경을 담는 영역이므로 `provenance`에 둔다.
- 이 필드는 툴의 정체성을 오염시키지 않고 추적성을 보강한다.

### extensionPoints.parameters

- 값 저장소가 아니라 불변 계약의 일부인 확장 인터페이스다.
- 따라서 `spec.immutable` 아래에 둔다.
- `name / type / default / range / envKey`만 유지한다.
- `envKey`는 이후 실행 시점으로 내려갈 최소 브리지이지만, 현재는 어디까지나 파라미터 계약의 일부로만 둔다.
- 파라미터의 실제 적용 전략이나 CLI 변환 방식은 여기 넣지 않는다.

### extensionPoints.attachments

- 외부 attachable 엔티티의 종류만 선언한다.
- `resourceProfile / executionPolicy / materializationPolicy` 정도만 남긴다.
- 세부 필드는 선언하지 않는다.
- 이 필드의 목적은 "이 툴 계약에 어떤 외부 정책이 결합될 수 있는가"를 고정하는 것이다.

### status

- 등록 후 계산되는 상태 정보를 담는다.
- `casHash`를 content identity의 단일 기준으로 둔다.
- `definitionDigest`를 별도로 두지 않는 이유는 순환 해시 문제를 피하고 식별 기준을 단순화하기 위해서다.
- `phase / registeredAt / validation`은 등록 시스템 관점의 운영 상태이며, 계약 본문과 분리되어야 한다.

## RegisteredTool v0.1에서 의도적으로 제외한 것

- `binding strategy`
- `resolveFrom`
- `materialization strategy`
- `publish.as`
- `mount/env/path concretization`
- `queue / retry / backoff`
- `execution profile`
- `resource tier 값`
- `storage backend 의존 필드`
- `shape`의 `row / list / collection` 확장
- parameter의 CLI arg lowering 규칙
- output의 실제 materialized location
- external policy의 상세 스키마

이 항목들은 모두 정적 툴 계약을 넘어서기 때문에 지금은 넣지 않는다.

## v0.2로 넘기는 것

- `shape` 확장: `row / list / collection`
- 포트와 실제 데이터 표현 사이의 상세 대응 규칙
- parameter의 추가 전달 규약
- attachment 대상(`ExecutionPolicy`, `MaterializationPolicy`)의 구체 스키마
- richer output constraints vocabulary
- provenance의 추가 표준화 필드
- 포트 단위의 더 정교한 semantic typing

## 다음 단계 한 줄 제안

다음 단계는 `toolRef`가 이 `RegisteredTool` 계약을 어떻게 안정적으로 참조할지 고정하는 일이다.

## 구현 시작점 판단

이 YAML을 지금 구현 시작점으로 써도 되는지: Yes
