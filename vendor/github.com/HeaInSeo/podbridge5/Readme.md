# podbridge5

English follows the Korean section.

## 개요

`podbridge5`는 컨테이너 런타임, 이미지 빌드, 이미지 push 흐름을 다루는 Go 라이브러리입니다.
이 저장소는 Kubernetes 오케스트레이션 계층 아래에 있으므로, `NodeForge` 같은 상위 프로젝트와는 다른 방식으로 검증하는 편이 맞습니다.

## 검증 방향

검증은 두 경로로 나눕니다.

### 1. 기본 개발 경로

현재 호스트에서 빠르게 확인하는 경로입니다.

- `make test`
- `make test-runtime`
- `make test-runtime-integration`
- `make runtime-env-check`

이 경로는 코드 수정, 경량 검증, 로컬 정리에 적합합니다. 다만 `buildah`, `containers/storage`, `containers/image` 의존성 때문에 호스트 패키지가 없으면 전체 테스트가 바로 되지 않을 수 있습니다.

### 2. 원격 런타임 VM 경로

실제 런타임 의존성 검증은 `100.123.80.48` 장비의 Multipass에서 ephemeral VM을 만들어 수행합니다.

- 목적 장비: `100.123.80.48`
- 사용자: `seoy`
- VM 이름 기본값: `podbridge5-dev`
- 권장 OS: Ubuntu 24.04
- 검증 대상:
  - `buildah`
  - `fuse-overlayfs`
  - `pkg-config` / `gpgme`
  - `btrfs` headers
  - system `podman` socket
  - storage 초기화
  - 이미지 build / push 흐름

이 경로는 `multipass-k8s-lab` 클러스터와 분리합니다.

- `multipass-k8s-lab`: `NodeForge` Kubernetes end-to-end 검증
- `podbridge5-dev`: `podbridge5` 런타임 검증

## 원격 VM 자동화

원격 VM 테스트는 Makefile이 자동으로 처리합니다.

`make vm-test-runtime` 실행 순서:

1. 원격 장비에서 테스트용 VM 생성
2. 필요한 패키지와 system `podman` socket 준비
3. 현재 로컬 `podbridge5` 워크트리를 tar.gz로 묶어 원격 호스트로 업로드
4. 원격 호스트에서 `multipass transfer`로 fresh VM에 동기화
5. VM 안에서 `go test ./...` 실행
6. 테스트 종료 후 VM 삭제

`make vm-test-runtime-integration`도 같은 흐름으로 동작하며, integration 태그 테스트를 수행합니다.

중요한 점은 이 경로가 항상 **clean VM 기준 재현성**을 목표로 한다는 것입니다.
환경 찌꺼기 때문에 생기는 문제라면 VM을 새로 만들고 다시 돌렸을 때 사라져야 합니다.
반대로 fresh VM에서도 계속 재현되면, 그 문제는 호스트 찌꺼기가 아니라 코드나 런타임 초기화 경로에 있을 가능성이 큽니다.

## 로그 수집

원격 VM 테스트 출력은 콘솔에 표시되는 동시에 로컬 로그 파일로 저장됩니다.

- `artifacts/vm-test-runtime.log`
- `artifacts/vm-test-runtime-integration.log`

로그에는 VM lifecycle, 원격 준비 단계, worktree sync, `go test` stdout/stderr가 함께 들어갑니다.

## 사용 방법

필수 환경 변수:

- `REMOTE_PASS`: 원격 장비 SSH 비밀번호

자주 쓰는 타깃:

- `make vm-test-runtime REMOTE_PASS=...`
- `make vm-test-runtime-integration REMOTE_PASS=...`
- `make vm-create-runtime REMOTE_PASS=...`
- `make vm-prepare-runtime REMOTE_PASS=...`
- `make vm-sync-runtime REMOTE_PASS=...`
- `make vm-delete-runtime REMOTE_PASS=...`

기본값으로 현재 로컬 워크트리(`$(CURDIR)`)가 VM에 동기화됩니다.
필요하면 `PODBRIDGE5_LOCAL_REPO=/path/to/repo`로 명시적으로 바꿀 수 있습니다.

## 참고

이 저장소의 런타임 검증은 GitHub Actions를 기준으로 설계하지 않습니다.
중첩 가상화와 런타임 의존성 때문에, 현재 기준으로는 원격 장비의 Multipass VM을 사용하는 방식이 더 단순하고 안정적입니다.

기존 README 초안은 [backup/Readme.legacy.md](backup/Readme.legacy.md)에 백업해 두었습니다.

---

# podbridge5

## Overview

`podbridge5` is a Go library for container runtime behavior, image builds, and image pushes.
Because it sits below the Kubernetes orchestration layer, it should be validated differently from upper-layer projects such as `NodeForge`.

## Validation Model

Validation is split into two paths.

### 1. Base development path

This is the fast path on the current host.

- `make test`
- `make test-runtime`
- `make test-runtime-integration`
- `make runtime-env-check`

This path is useful for code changes, lightweight checks, and local maintenance. Since the repository still depends directly on `buildah`, `containers/storage`, and `containers/image`, a full test run may still require host packages to be installed.

### 2. Remote runtime VM path

Runtime-dependent validation runs on an ephemeral Multipass VM created on `100.123.80.48`.

- target machine: `100.123.80.48`
- user: `seoy`
- default VM name: `podbridge5-dev`
- recommended OS: Ubuntu 24.04
- validated components:
  - `buildah`
  - `fuse-overlayfs`
  - `pkg-config` / `gpgme`
  - `btrfs` headers
  - system `podman` socket
  - storage initialization
  - image build / push flows

This stays separate from the `multipass-k8s-lab` cluster.

- `multipass-k8s-lab`: Kubernetes end-to-end validation for `NodeForge`
- `podbridge5-dev`: runtime validation for `podbridge5`

## Remote VM Automation

The Makefile handles the remote runtime VM lifecycle automatically.

`make vm-test-runtime` does the following:

1. creates a fresh test VM on the remote machine
2. installs required packages and prepares the system `podman` socket
3. archives the current local `podbridge5` worktree and uploads it to the remote host
4. syncs that archive into the fresh VM with `multipass transfer`
5. runs `go test ./...` inside the VM
6. deletes the VM after the test finishes

`make vm-test-runtime-integration` uses the same lifecycle and runs the integration-tag path.

The key property is **clean-VM reproducibility**.
If a failure only exists because of environment residue, deleting the runtime VM and running again should make it disappear.
If it still reproduces on a fresh VM, the problem is more likely in code or runtime initialization rather than stale host state.

## Log Collection

Remote VM test output is shown in the console and also stored in local log files.

- `artifacts/vm-test-runtime.log`
- `artifacts/vm-test-runtime-integration.log`

The logs include the VM lifecycle, remote preparation steps, worktree sync, and `go test` stdout/stderr.

## Usage

Required environment variable:

- `REMOTE_PASS`: SSH password for the remote machine

Common targets:

- `make vm-test-runtime REMOTE_PASS=...`
- `make vm-test-runtime-integration REMOTE_PASS=...`
- `make vm-create-runtime REMOTE_PASS=...`
- `make vm-prepare-runtime REMOTE_PASS=...`
- `make vm-sync-runtime REMOTE_PASS=...`
- `make vm-delete-runtime REMOTE_PASS=...`

By default, the current local worktree (`$(CURDIR)`) is synced into the VM.
You can override it explicitly with `PODBRIDGE5_LOCAL_REPO=/path/to/repo`.

## Note

This runtime validation path is not designed around GitHub Actions.
Given nested virtualization and runtime package dependencies, the current preferred path is a remote Multipass VM on the lab machine.

The previous README draft has been backed up to [backup/Readme.legacy.md](backup/Readme.legacy.md).
