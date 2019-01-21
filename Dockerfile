# Build Go binary without cgo dependencies
FROM golang:1.11 as builder
WORKDIR /go/src/github.com/gocardless/theatre
COPY . /go/src/github.com/gocardless/theatre
RUN CGO_ENABLED=0 make VERSION=$(cat VERSION) all

# Use ubuntu as our base package to enable generic system tools
FROM ubuntu:latest

# Without these certificates we'll fail to validate TLS connections to Google's
# services.
RUN set -x \
      && apt-get update \
      && apt-get install --no-install-recommends -y ca-certificates \
      && apt-get clean -y \
      && rm -rf /var/lib/apt/lists/*

WORKDIR /
COPY --from=builder /go/src/github.com/gocardless/theatre/bin/* /
ENTRYPOINT ["/bin/bash"]
