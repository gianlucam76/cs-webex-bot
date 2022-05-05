# Build the manager binary
FROM aci-docker-reg.cisco.com/c3/godev:1.0.0-beta_u39 as builder


ARG ARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY webex_utils/ webex_utils/

# Build
RUN GOOS=linux GOARCH=$ARCH go build --trimpath -a -o webex_bot main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM aci-docker-reg.cisco.com/c3/minbase:1.0.0-beta_u41
RUN useradd  nonroot
WORKDIR /
COPY --from=builder /workspace/webex_bot .
USER nonroot:nonroot

ENTRYPOINT ["/webex_bot"]
