PROG=bin/rbac-manager bin/vault-manager bin/theatre-secrets bin/workloads-manager bin/theatre-consoles
PROJECT=github.com/gocardless/theatre
IMAGE=eu.gcr.io/gc-containers/gocardless/theatre
VERSION=$(shell git describe --tags  --dirty --long)
GIT_REVISION=$(shell git rev-parse HEAD)
DATE=$(shell date +"%Y%m%d.%H%M%S")
LDFLAGS=-ldflags "-s -X github.com/gocardless/theatre/v3/cmd.Version=$(VERSION) -X github.com/gocardless/theatre/v3/cmd.Commit=$(GIT_REVISION) -X github.com/gocardless/theatre/v3/cmd.Date=$(DATE)"
BUILD_COMMAND=go build $(LDFLAGS)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

.PHONY: build build-darwin build-linux build-all clean fmt vet test acceptance-e2e acceptance-run acceptance-prepare acceptance-destory generate manifests install-tools deploy

build: $(PROG)
build-darwin: $(PROG:=.darwin)
build-linux: $(PROG:=.linux)
build-all: build-darwin build-linux

# Specific linux build target, making it easy to work with the docker acceptance
# tests on OSX. It uses by default your local arch
bin/%.linux:
	CGO_ENABLED=0 GOOS=linux $(BUILD_COMMAND) -a -o $@ ./cmd/$*/.

bin/%.darwin:
	CGO_ENABLED=0 GOOS=darwin $(BUILD_COMMAND) -a -o $@ ./cmd/$*/.

bin/%:
	CGO_ENABLED=0 $(BUILD_COMMAND) -o $@ ./cmd/$*/.

clean:
	rm -rvf $(PROG) $(PROG:%=%.linux) $(PROG:%=%.darwin)

fmt:
	go fmt ./...

vet:
	go vet -tests -unreachable ./...

test: install-tools
	KUBEBUILDER_ASSETS="$(shell setup-envtest use -p path 1.24.x!)" ginkgo -race -randomizeSuites -randomizeAllSpecs -r ./...

# Requires the following binaries: kubectl, kustomize, kind, docker
acceptance-e2e: install-tools acceptance-prepare acceptance-run acceptance-destroy

acceptance-run: install-tools
	go run cmd/acceptance/main.go run --verbose

acceptance-prepare: install-tools
	go run cmd/acceptance/main.go prepare --verbose

acceptance-destroy: install-tools
	go run cmd/acceptance/main.go destroy

generate: install-tools
	controller-gen object paths="./apis/rbac/..."
	controller-gen object paths="./apis/workloads/..."

manifests: generate
	controller-gen crd paths="./apis/rbac/..." output:crd:artifacts:config=config/base/crds
	controller-gen crd paths="./apis/workloads/..." output:crd:artifacts:config=config/base/crds

# See https://github.com/kubernetes-sigs/controller-runtime/tree/main/tools/setup-envtest
install-tools:
	go install github.com/onsi/ginkgo/ginkgo@v1.16.5
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.10.0
	go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.17

install-tools-homebrew:
	brew install kubernetes-cli kustomize kind
	echo "you also need docker and go in your developer environment"

# Deprecated
deploy:
	kustomize build config/base | kubectl apply -f -

deploy-production:
	kustomize build config/overlays/production | kubectl apply -f -

docker-build:
	docker build -t $(IMAGE):latest .

docker-pull:
	docker pull $(IMAGE):$$(git rev-parse HEAD)

docker-push:
	docker push $(IMAGE):latest

docker-tag:
	docker tag $(IMAGE):$$(git rev-parse HEAD) $(IMAGE):latest

