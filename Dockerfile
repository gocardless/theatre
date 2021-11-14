# Build Go binary without cgo dependencies
FROM golang:1.17.3 as builder
WORKDIR /go/src/github.com/gocardless/theatre

COPY . /go/src/github.com/gocardless/theatre
RUN make VERSION=$(cat VERSION) build

# Use ubuntu as our base package to enable generic system tools
FROM ubuntu:focal-20211006

# Without these certificates we'll fail to validate TLS connections to Google's
# services.
RUN set -x \
      && apt-get update -y \
      && apt-get install -y --no-install-recommends \
                            ca-certificates \
      && apt-get clean -y \
      && rm -rf /var/lib/apt/lists/*

WORKDIR /
COPY --from=builder /go/src/github.com/gocardless/theatre/bin/* /usr/local/bin/
ENTRYPOINT ["/bin/bash"]
