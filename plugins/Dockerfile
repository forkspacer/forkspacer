# Build the plugin using the same Go version and environment as the controller
FROM golang:1.24 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG PLUGIN

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Download dependencies
RUN go mod download

# Copy required source files for dependencies
COPY plugins/${PLUGIN}/main.go plugins/${PLUGIN}/main.go
COPY api/ api/
COPY internal/ internal/
COPY pkg/ pkg/


# Build the plugin with the same settings as the controller
RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -buildmode=plugin -a -o plugin.so plugins/${PLUGIN}/main.go

# Use a minimal base image to extract the plugin
FROM alpine:latest
WORKDIR /output
COPY --from=builder /workspace/plugin.so .
CMD ["cp", "plugin.so", "/output/plugin.so"]
