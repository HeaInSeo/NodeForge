module github.com/HeaInSeo/NodeForge

go 1.25.5

require google.golang.org/grpc v1.72.0

require (
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250218202821-56aae31c358a // indirect
	google.golang.org/protobuf v1.36.6 // indirect
	k8s.io/api v0.33.0
	k8s.io/apimachinery v0.33.0
	k8s.io/client-go v0.33.0
)

require (
	github.com/HeaInSeo/podbridge5 v0.0.0-00010101000000-000000000000
	github.com/containers/storage v1.55.0
)

require (
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/seoyhaein/sori v0.0.1 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	golang.org/x/sync v0.14.0 // indirect
	oras.land/oras-go/v2 v2.6.0 // indirect
)

replace github.com/HeaInSeo/podbridge5 => /opt/go/src/github.com/HeaInSeo/podbridge5
