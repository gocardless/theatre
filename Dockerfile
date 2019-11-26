# Build Go binary without cgo dependencies
FROM golang:1.13.4 as builder
WORKDIR /go/src/github.com/gocardless/theatre
COPY . /go/src/github.com/gocardless/theatre
RUN make VERSION=$(cat VERSION) build

# Use ubuntu as our base package to enable generic system tools
FROM ubuntu:bionic-20190807

# Without these certificates we'll fail to validate TLS connections to Google's
# services.
RUN set -x \
      && apt-get update -y \
      && apt-get install -y --no-install-recommends \
                            ca-certificates \
                            curl \
                            unzip \
      && apt-get clean -y \
      && rm -rf /var/lib/apt/lists/*

# Install envconsul for theatre-envconsul
ENV ENVCONSUL_VERSION=0.9.1 \
    ENVCONSUL_SHA256=b58d032ad61937eca9def17482807124fa1bafac7e7bb5e025ea8a28d9c6ce42

RUN set -x \
    && curl -o /tmp/envconsul.zip -fsL https://releases.hashicorp.com/envconsul/0.9.1/envconsul_${ENVCONSUL_VERSION}_linux_amd64.zip \
    && echo ${ENVCONSUL_SHA256} /tmp/envconsul.zip | sha256sum -c \
    && unzip /tmp/envconsul.zip -d /tmp \
    && mv /tmp/envconsul /usr/local/bin/ \
    && rm /tmp/envconsul.zip

WORKDIR /
COPY --from=builder /go/src/github.com/gocardless/theatre/bin/* /usr/local/bin/
ENTRYPOINT ["/bin/bash"]
