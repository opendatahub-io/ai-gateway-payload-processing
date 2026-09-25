ARG BUILDPLATFORM
ARG TARGETPLATFORM

## Multistage build
FROM --platform=$BUILDPLATFORM registry.access.redhat.com/ubi9/go-toolset@sha256:0a4666f7a4eb0644c97a73cba198eb268691b270d97831822689e7a2088f87be AS builder
ARG CGO_ENABLED=1
ARG TARGETOS
ARG TARGETARCH
ARG COMMIT_SHA=unknown
ARG BUILD_REF

USER root

# Dependencies
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

# Sources
COPY api/ api/
COPY cmd/ cmd/
COPY pkg/ pkg/

# -X needs the exact import path of the dependency's version package (matches go.mod / module graph).
RUN VERSION_PKG="$(go list -f '{{.ImportPath}}' github.com/llm-d/llm-d-inference-payload-processor/version)" && \
	CGO_ENABLED=${CGO_ENABLED} GOEXPERIMENT=strictfipsruntime GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
	go build -a -trimpath -ldflags="-s -w -X ${VERSION_PKG}.CommitSHA=${COMMIT_SHA} -X ${VERSION_PKG}.BuildRef=${BUILD_REF}" -o /bbr ./cmd

USER 1001

# Multistage deploy
FROM --platform=$TARGETPLATFORM registry.access.redhat.com/ubi9/ubi-minimal@sha256:8ebe2ad8fdf3cab3e5a53c1edc69194c98209cfadab24b884f4ad9ebcf7bbbfc

WORKDIR /
COPY --from=builder /bbr /bbr

USER 1001

ENTRYPOINT ["/bbr"]
