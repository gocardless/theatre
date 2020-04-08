# Build Go binary without cgo dependencies
FROM golang:1.13.5 as builder
WORKDIR /go/src/github.com/gocardless/theatre

# Clone our fork of envconsul and build it
RUN set -x \
      && git clone https://github.com/gocardless/envconsul.git \
      && cd envconsul \
      && git checkout 2eb7fdc4dd1a13464e9a529e324ffd9b8d12ce25 \
      && make linux/amd64 \
      && mkdir ../bin \
      && mv pkg/linux_amd64/envconsul ../bin

COPY . /go/src/github.com/gocardless/theatre
RUN make VERSION=$(cat VERSION) build

# Use ubuntu as our base package to enable generic system tools
FROM ubuntu:bionic-20191202

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
