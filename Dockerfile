# Build Go binary without cgo dependencies
FROM golang:1.11 as builder
WORKDIR /go/src/github.com/lawrencejones/theatre
COPY . /go/src/github.com/lawrencejones/theatre
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager github.com/lawrencejones/theatre/cmd/manager

# Use ubuntu as our base package to enable generic system tools
FROM ubuntu:latest
WORKDIR /
COPY --from=builder /go/src/github.com/lawrencejones/theatre/manager .
ENTRYPOINT ["/manager"]
