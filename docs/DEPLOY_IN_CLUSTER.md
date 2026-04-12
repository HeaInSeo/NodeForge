# NodeForge in-cluster 배포 가이드

이 문서는 NodeForge를 multipass-k8s-lab 클러스터에 배포할 때 반드시 확인해야 할 사항을 기록한다.

---

## 전제 조건

- multipass-k8s-lab 클러스터가 실행 중 (`seoy` 장비: 100.123.80.48)
- Cilium Gateway + Harbor HTTPRoute 적용 완료 (Sprint I-2)
- Harbor 접근 가능: `http://harbor.10.113.24.96.nip.io`

---

## 주의사항

### 1. 이미지 빌드 전 `make vendor` 필수

`go.mod`의 `replace` 디렉티브가 podbridge5 로컬 경로(`/opt/go/src/github.com/HeaInSeo/podbridge5`)를 가리키기 때문에, Docker 빌드 컨텍스트 내에서는 해당 경로에 접근할 수 없다.  
`make vendor`(= `go work vendor`)를 먼저 실행해 모든 의존성을 `vendor/` 디렉토리에 복사한 뒤 이미지를 빌드해야 한다.

```bash
# seoy 장비에서 실행
cd /opt/go/src/github.com/HeaInSeo/NodeForge

make vendor       # go work vendor → vendor/ 생성
make push-image   # podman build -mod=vendor + podman push
```

`vendor/` 없이 `make push-image`를 실행하면 Dockerfile 내 `go build` 단계에서 오류가 발생한다.

---

### 2. Harbor 인증 Secret 주입 필요

`deploy/03-nodeforge.yaml`에는 Secret 구조 템플릿만 포함돼 있으며 실제 인증 값은 없다.  
배포 전에 아래 명령으로 실제 Harbor 관리자 자격증명을 주입해야 한다.

```bash
kubectl create secret generic nodeforge-harbor-auth \
  --from-literal=username=admin \
  --from-literal=password=<harbor-admin-password> \
  -n nodeforge-system
```

> **주의**: `deploy/03-nodeforge.yaml`의 Secret 오브젝트(`stringData: REPLACE_ME`)는 git에 커밋된 플레이스홀더다. 실제 값을 이 파일에 직접 입력해서 커밋하지 말 것.

---

### 3. Gateway 이름 확인 후 GRPCRoute 수정

`deploy/04-grpcroute.yaml`의 `parentRefs.name`은 실제 클러스터의 Gateway 이름과 일치해야 한다.  
배포 전에 아래 명령으로 실제 Gateway 이름과 네임스페이스를 확인하고 필요하면 수정한다.

```bash
KUBECONFIG=../multipass-k8s-lab/kubeconfig kubectl get gateway -A
# NAME              CLASS    ADDRESS         PROGRAMMED   AGE
# cilium-gateway    cilium   10.113.24.96    True         ...
```

`parentRefs`에 `name`과 `namespace` 둘 다 실제 값으로 맞춰야 GRPCRoute가 `Accepted` 상태가 된다.

```yaml
# deploy/04-grpcroute.yaml
spec:
  parentRefs:
    - name: cilium-gateway        # ← kubectl get gateway -A 결과와 일치시킬 것
      namespace: nodeforge-system # ← Gateway가 있는 실제 네임스페이스
```

GRPCRoute 상태 확인:

```bash
kubectl get grpcroute -n nodeforge-system
# NAME              HOSTNAMES                              ...  STATUS
# nodeforge-grpc    ["nodeforge.10.113.24.96.nip.io"]    ...  Accepted
```

---

### 4. Privileged Pod — buildah in-cluster 실행을 위한 필수 설정

NodeForge Pod는 buildah 라이브러리를 사용해 컨테이너 이미지를 직접 빌드한다.  
Pod 내에서 overlay 파일시스템을 마운트하고 runc를 실행하려면 `privileged: true`가 필요하다.

```yaml
# deploy/03-nodeforge.yaml
securityContext:
  privileged: true
  allowPrivilegeEscalation: true
```

이 설정이 없으면 buildah의 `imagebuildah.BuildDockerfiles()` 호출이 `operation not permitted` 오류로 실패한다.

클러스터 보안 정책(PodSecurityAdmission 등)이 `privileged: true`를 차단할 경우:
- `nodeforge-system` 네임스페이스에 `pod-security.kubernetes.io/enforce: privileged` 레이블 추가  
- 또는 PSA를 `baseline`/`restricted`에서 제외하는 admission exception 구성

```bash
kubectl label namespace nodeforge-system \
  pod-security.kubernetes.io/enforce=privileged \
  pod-security.kubernetes.io/warn=privileged
```

---

## 배포 순서 요약

```bash
# 1. vendor 생성 (로컬 podbridge5 의존성 해결)
make vendor

# 2. Harbor에 이미지 push (seoy 장비 또는 빌드 환경)
podman login harbor.10.113.24.96.nip.io
make push-image

# 3. Harbor 인증 Secret 생성
kubectl create secret generic nodeforge-harbor-auth \
  --from-literal=username=admin \
  --from-literal=password=<password> \
  -n nodeforge-system

# 4. Gateway 이름 확인 후 04-grpcroute.yaml parentRefs 수정 (필요 시)
KUBECONFIG=../multipass-k8s-lab/kubeconfig kubectl get gateway -A

# 5. 클러스터 리소스 배포
make deploy-multipass

# 6. 확인
kubectl get pod,svc,grpcroute -n nodeforge-system
```

---

## 관련 파일

| 파일 | 설명 |
|------|------|
| `deploy/00-namespaces.yaml` | 네임스페이스 (nodeforge-system, nodeforge-smoke) |
| `deploy/02-rbac.yaml` | ServiceAccount + ClusterRole (L3/L4 smoke 권한) |
| `deploy/03-nodeforge.yaml` | Deployment + Service + Secret 템플릿 |
| `deploy/04-grpcroute.yaml` | Cilium GRPCRoute |
| `Dockerfile` | NodeForge 이미지 빌드 (make vendor 선행 필수) |
| `../multipass-k8s-lab/k8s/` | Gateway/HTTPRoute/GRPCRoute YAML (클러스터 재구성 시 재적용) |
