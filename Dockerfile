# ── Stage 1: Go 빌드 ──────────────────────────────────────────────────────────
# podbridge5가 사용하는 컨테이너 빌드 라이브러리는 CGO를 사용하므로
# CGO_ENABLED=1 + 관련 C 헤더가 필요하다.
#
# go.mod의 replace directive(podbridge5)가 로컬 경로를 가리키므로
# 컨테이너 빌드 전에 반드시 `make vendor` (= go work vendor) 를 실행해야 한다.
# vendor/ 디렉토리가 없으면 빌드가 실패한다.
FROM quay.io/buildah/stable:v1.37.1 AS builder

ENV GO_VERSION=1.25.5
RUN dnf install -y gcc && \
    curl -fsSL https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz \
    | tar -C /usr/local -xz

ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH=/opt/go
ENV CGO_ENABLED=1

WORKDIR /src

# go.mod / go.work 먼저 복사 (vendor와 함께 캐시 활용)
COPY go.mod go.work go.work.sum ./
# vendor/ 가 있어야 -mod=vendor 빌드 가능 (`make vendor` 선행 필요)
COPY vendor/ ./vendor/
# api-protos gen/go 는 workspace 로컬 참조 — 빌드 컨텍스트에 함께 포함
COPY api-protos/ ./api-protos/
COPY . .

# go.work 경로 정비: api-protos → 상대 경로, sori → 빌드 컨텍스트 밖이므로 제거 (미사용)
RUN sed -i \
    -e 's|/opt/go/src/github.com/HeaInSeo/api-protos/gen/go/nodeforge/v1|./api-protos/gen/go/nodeforge/v1|g' \
    -e '/HeaInSeo\/sori/d' \
    go.work

RUN go build \
    -mod=vendor \
    -tags "exclude_graphdriver_btrfs containers_image_openpgp exclude_graphdriver_devicemapper" \
    -ldflags="-s -w" \
    -o /bin/nodeforge \
    ./cmd/controlplane/...

# ── Stage 2: 런타임 이미지 ────────────────────────────────────────────────────
# podbridge5 기반 이미지 빌드가 Pod 내에서 동작하려면 overlay + runc 런타임이 필요하다.
FROM quay.io/buildah/stable:v1.37.1

COPY --from=builder /bin/nodeforge /usr/local/bin/nodeforge

# 컨테이너 스토리지 디렉토리 (K8s Deployment에서 emptyDir 볼륨으로 마운트)
VOLUME ["/var/lib/containers"]

# containers/storage 가 root 실행 시에도 runroot를 찾을 수 있도록 명시
RUN mkdir -p /etc/containers && \
    printf '[storage]\ndriver = "overlay"\nrunroot = "/run/containers/storage"\ngraphRoot = "/var/lib/containers/storage"\n[storage.options.overlay]\nmount_program = "/usr/bin/fuse-overlayfs"\n' \
    > /etc/containers/storage.conf

EXPOSE 50051

ENTRYPOINT ["/usr/local/bin/nodeforge"]
