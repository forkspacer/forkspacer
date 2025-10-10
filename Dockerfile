# Build the manager binary
FROM golang:1.24 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=unknown
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
ARG PV_MIGRATE_VERSION=v2.2.1

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/
COPY pkg/ pkg/

# Build
# the GOARCH has no default value to allow the binary to be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -ldflags "-X github.com/forkspacer/forkspacer/pkg/constants/version.Version=${VERSION} \
    -X github.com/forkspacer/forkspacer/pkg/constants/version.GitCommit=${GIT_COMMIT} \
    -X github.com/forkspacer/forkspacer/pkg/constants/version.BuildDate=${BUILD_DATE}" \
    -a -o manager cmd/main.go

# Install https://github.com/utkuozdemir/pv-migrate
RUN wget https://github.com/utkuozdemir/pv-migrate/releases/download/${PV_MIGRATE_VERSION}/pv-migrate_${PV_MIGRATE_VERSION}_linux_x86_64.tar.gz
RUN tar -xvzf pv-migrate_${PV_MIGRATE_VERSION}_linux_x86_64.tar.gz

RUN mkdir -p /internal-data && \
    chown -R 65532:65532 /internal-data

FROM gcr.io/distroless/base-debian12:nonroot

WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder --chown=65532:65532 /workspace/pv-migrate /usr/local/bin/pv-migrate
COPY --from=builder --chown=65532:65532 /internal-data /internal-data

USER 65532:65532

ENTRYPOINT ["/manager"]
