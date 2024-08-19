# Build the manager binary
FROM golang:1.22 AS builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/ cmd/
COPY apis/ apis/
COPY internal/ internal/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o provider cmd/provider/main.go

FROM gcr.io/distroless/static@sha256:49af06135e8bbe8ddc46c1d28b0bd00961aae9c9ed090bbc0237f58e1462dd4b
WORKDIR /
COPY --from=builder /workspace/provider /usr/local/bin/crossplane-unikraft-cloud-provider
COPY package /

USER 65532
EXPOSE 8080
ENTRYPOINT ["crossplane-unikraft-cloud-provider"]
