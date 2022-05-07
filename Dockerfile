# Build the manager binary
FROM golang:1.17 as builder

ARG ARCH

ENV http_proxy http://proxy.esl.cisco.com:8080/
ENV HTTP_PROXY http://proxy.esl.cisco.com:8080/
ENV https_proxy=http://proxy.esl.cisco.com:8080/
ENV HTTPS_PROXY=http://proxy.esl.cisco.com:8080/
ENV NO_PROXY .cisco.com,.insieme.local,localhost
ENV no_proxy .cisco.com,.insieme.local,localhost

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
COPY utils/ utils/
COPY analyze/ analyze/

# Build
RUN GOOS=linux GOARCH=$ARCH go build -a -o webex_bot main.go

FROM aci-docker-reg.cisco.com/demo_images/centos:8  
RUN useradd  nonroot
WORKDIR /
COPY --from=builder /workspace/webex_bot .
USER nonroot:nonroot

ENTRYPOINT ["/webex_bot"]
