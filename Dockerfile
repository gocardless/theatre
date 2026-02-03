# Build Go binary without cgo dependencies
FROM golang:1.25.5 as builder
WORKDIR /go/src/github.com/gocardless/theatre

COPY . /go/src/github.com/gocardless/theatre
ARG git_revision=unset
RUN echo $git_revision > REVISION
RUN set -x \
  && make VERSION="$(cat VERSION)" GIT_REVISION="$(cat REVISION)" build

# Use ubuntu as our base package to enable generic system tools
FROM ubuntu:jammy-20251001

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
ARG git_revision=unset
RUN echo $git_revision > REVISION
ENTRYPOINT ["/bin/bash"]
