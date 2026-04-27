# NodeVault — multipass-k8s-lab 클러스터 통합 테스트 가이드

## 배경 및 목적

NodeVault의 통합 테스트는 실제 Kubernetes 클러스터에서 실행됩니다.
기존에는 kind(Kubernetes in Docker)만을 공식 환경으로 지정했으나,
다음과 같은 이유로 multipass-k8s-lab VM 클러스터를 추가 테스트 환경으로 채택합니다.

| 항목 | kind | multipass-k8s-lab |
|------|------|-------------------|
| 설치 위치 | 단일 호스트 프로세스 | Ubuntu 24.04 VM 3대 (1 master + 2 worker) |
| 네트워크 격리 | 호스트 네트워크 공유 | 독립 VM 네트워크 (10.87.127.x) |
| 노드 수 | 1 | 3 |
| 컨테이너 런타임 | containerd | containerd |
| 레지스트리 접근 | ClusterIP 10.96.0.1:5000 | NodePort 10.87.127.18:31500 |
| 멀티노드 스케줄링 | 불가 | 가능 |

---

## 클러스터 정보

| 항목 | 값 |
|------|----|
| 프로젝트 경로 | `/opt/go/src/github.com/HeaInSeo/multipass-k8s-lab` |
| Kubeconfig | `multipass-k8s-lab/kubeconfig` |
| Master IP | `10.87.127.18` |
| Worker IPs | `10.87.127.94`, `10.87.127.150` |
| K8s 버전 | v1.32.13 |
| Pod CIDR | `10.244.0.0/16` |
| Service CIDR | `10.96.0.0/12` |
| 내부 레지스트리 | `10.87.127.18:31500` (NodePort 31500) |

---

## 아키텍처: 로컬 바이너리 + 원격 클러스터

NodeVault는 **클러스터 내부에 배포되지 않고, 호스트에서 바이너리로 실행**됩니다.
kubeconfig를 통해 원격 클러스터 K8s API에 접근합니다.

```
[호스트]                          [multipass VM 클러스터]
  NodeVault binary  ──gRPC─→  (localhost:50051 — 클라이언트가 접속)
  (bin/nodeforge)
       │
       ├── K8s API 접근 ──────→  lab-master-0:6443
       │   (KUBECONFIG 사용)
       │
       └── 레지스트리 접근 ────→  10.87.127.18:31500
           (HTTP, --insecure)         │
                                      └── kaniko Job이 이미지 push
                                          (NodePort 통해 어느 노드에서도 접근 가능)
```

이 방식의 장점:
- NodeVault 배포 이미지 없이 바이너리만으로 테스트 가능
- 클러스터 재시작 없이 바이너리 교체 가능
- 기존 통합 테스트(localhost:50051)를 수정 없이 재사용

---

## 사전 조건

```bash
# 1. multipass-k8s-lab 클러스터 실행 확인
KUBECONFIG=/opt/go/src/github.com/HeaInSeo/multipass-k8s-lab/kubeconfig \
  kubectl get nodes

# 예상 출력:
# NAME           STATUS   ROLES           AGE
# lab-master-0   Ready    control-plane   ...
# lab-worker-0   Ready    <none>          ...
# lab-worker-1   Ready    <none>          ...

# 2. 호스트에서 master 노드 레지스트리 접근 가능 확인 (배포 후)
curl -s http://10.87.127.18:31500/v2/ | python3 -m json.tool
```

---

## 실행 방법

```bash
cd /opt/go/src/github.com/HeaInSeo/NodeVault

# 1. 클러스터 리소스 배포 (최초 1회, 또는 클러스터 재시작 후)
make deploy-multipass

# 2. 통합 테스트 실행
make test-integration-multipass

# 또는 한 번에
make deploy-multipass test-integration-multipass

# 3. 정리 (선택적)
make undeploy-multipass
```

`deploy-multipass` 가 배포하는 리소스:

| 파일 | 내용 |
|------|------|
| `deploy/00-namespaces.yaml` | nodeforge-system, nodeforge-builds, nodeforge-smoke |
| `deploy/01-registry.yaml` | registry:2 Deployment + NodePort 31500 Service |
| `deploy/02-rbac.yaml` | ServiceAccount + ClusterRole + ClusterRoleBinding |

---

## 환경 변수

`test-integration-multipass` Makefile 타겟이 자동으로 설정하는 환경 변수:

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `KUBECONFIG` | `../multipass-k8s-lab/kubeconfig` | 클러스터 인증 |
| `NODEFORGE_REGISTRY_ADDR` | `10.87.127.18:31500` | kaniko push 대상 레지스트리 |

Override 방법:
```bash
MULTIPASS_MASTER_IP=192.168.x.x make test-integration-multipass
```

---

## 레지스트리 설계 결정

- **HTTP 전용 (TLS 없음)**: 개발/테스트 환경 전용. 프로덕션에서는 TLS 레지스트리 사용.
- **emptyDir 스토리지**: Pod 재시작 시 이미지 초기화됨. 테스트 독립성 보장.
- **NodePort 31500**: 호스트와 모든 클러스터 노드에서 동일한 엔드포인트로 접근.
- **kaniko `--insecure --skip-tls-verify`**: 이미 `pkg/build/service.go`에 포함됨.

---

## 알려진 제약 사항 및 리스크

| ID | 내용 | 영향 |
|----|------|------|
| R-MP-01 | emptyDir 레지스트리는 Pod 재시작 시 초기화 | 테스트 재실행 전 kaniko 이미지 캐시 없음 (각 빌드가 full build) |
| R-MP-02 | NodeVault가 호스트 바이너리로 실행 — gRPC 서버 충돌 방지 필요 | 이전 프로세스가 남아있으면 50051 포트 충돌 |
| R-MP-03 | 클러스터 재시작 시 레지스트리 Pod 재생성 필요 | `make deploy-multipass` 재실행으로 해결 |
| R-MP-04 | kaniko 이미지(`gcr.io/kaniko-project/executor:v1.23.2`) 최초 pull 시간 | 클러스터 노드당 최초 실행 시 수 분 소요 가능 |
| R-MP-05 | in-cluster ServiceAccount 인증 미구현 | 현재 kubeconfig 직접 사용. 프로덕션 배포 시 추가 필요 (Roadmap) |

---

## 기존 kind 통합 테스트와의 차이

| 항목 | kind (`test-integration`) | multipass (`test-integration-multipass`) |
|------|--------------------------|------------------------------------------|
| 레지스트리 주소 | `10.96.0.1:5000` (ClusterIP) | `10.87.127.18:31500` (NodePort) |
| NodeVault 실행 위치 | 별도 프로세스 또는 클러스터 내 | 호스트 바이너리 |
| 멀티노드 검증 | 불가 | 가능 |
| 네트워크 현실성 | 낮음 (loopback) | 높음 (실제 VM 네트워크) |
