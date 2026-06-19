# Dockerfile has specific requirement to put this ARG at the beginning:
# https://docs.docker.com/engine/reference/builder/#understand-how-arg-and-from-interact
ARG GOLANG_VERSION=1.25

ARG BUILDPLATFORM
ARG TARGETPLATFORM

## Multistage build
FROM --platform=$BUILDPLATFORM registry.access.redhat.com/ubi9/go-toolset:$GOLANG_VERSION AS builder
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
FROM --platform=$TARGETPLATFORM registry.access.redhat.com/ubi9/ubi-minimal:9.8@sha256:1bc3c5c15720506a0cf48adfdf8b623dfe704377e007d7bbae8d14876392ca6a

WORKDIR /
COPY --from=builder /bbr /bbr

USER 1001

ENTRYPOINT ["/bbr"]
