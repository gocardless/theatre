PROG=bin/rbac-manager bin/vault-manager bin/theatre-secrets bin/workloads-manager bin/theatre-consoles
PROJECT=github.com/gocardless/theatre
IMAGE=eu.gcr.io/gc-containers/gocardless/theatre
VERSION=$(shell git describe --tags  --dirty --long)
GIT_REVISION=$(shell git rev-parse HEAD)
DATE=$(shell date +"%Y%m%d.%H%M%S")
LDFLAGS=-ldflags "-s -X github.com/gocardless/theatre/v5/cmd.Version=$(VERSION) -X github.com/gocardless/theatre/v5/cmd.Commit=$(GIT_REVISION) -X github.com/gocardless/theatre/v5/cmd.Date=$(DATE)"
BUILD_COMMAND=go build $(LDFLAGS)

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

.PHONY: build build-darwin build-linux build-all clean fmt vet test \
	acceptance-e2e acceptance-run acceptance-prepare acceptance-destory \
	generate manifests install-tools deploy help \
	lint lint-fix lint-config run \
	docker-build docker-push build-installer kustomize controller-gen \
	setup-envtest envtest golangci-lint

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
	
##@ Development

manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	controller-gen rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

hack/boilerplate.go.txt:
	echo "/*" > $@
	cat LICENSE >> $@
	echo "*/" >> $@

generate: controller-gen hack/boilerplate.go.txt ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."

fmt: ## Run go fmt against code.
	go fmt ./...

vet: ## Run go vet against code.
	go vet ./...

test: manifests generate fmt vet setup-envtest setup-ginkgo ## Run tests.
	KUBEBUILDER_ASSETS="$(shell setup-envtest use $(ENVTEST_K8S_VERSION) -p path)" ginkgo -race -randomizeSuites -randomizeAllSpecs -r ./...

acceptance-e2e: install-tools acceptance-prepare acceptance-run acceptance-destroy ## Requires the following binaries: kubectl, kustomize, kind, docker

acceptance-run: install-tools ## Run acceptance tests
	go run cmd/acceptance/main.go run --verbose

acceptance-prepare: install-tools ## Prepare acceptance tests
	go run cmd/acceptance/main.go prepare --verbose

acceptance-destroy: install-tools ## Destroy acceptance tests
	go run cmd/acceptance/main.go destroy

lint: golangci-lint ## Run golangci-lint linter
	golangci-lint run

lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	golangci-lint run --fix

lint-config: golangci-lint ## Verify golangci-lint linter configuration
	golangci-lint config verify

##@ Build

build: $(PROG) ## Build a binary
build-darwin: $(PROG:=.darwin) ## Build a binary for darwin
build-linux: $(PROG:=.linux) ## Build a binary for linux
build-all: build-darwin build-linux ## Build all binaries

# Specific linux build target, making it easy to work with the docker acceptance
# tests on OSX. It uses by default your local arch
bin/%.linux: ## Build a binary for linux
	CGO_ENABLED=0 GOOS=linux $(BUILD_COMMAND) -a -o $@ ./cmd/$*/.

bin/%.darwin: ## Build a binary for darwin
	CGO_ENABLED=0 GOOS=darwin $(BUILD_COMMAND) -a -o $@ ./cmd/$*/.

bin/%: ## Build a binary
	CGO_ENABLED=0 $(BUILD_COMMAND) -o $@ ./cmd/$*/.

clean: ## Clean up the build artifacts
	rm -rvf $(PROG) $(PROG:%=%.linux) $(PROG:%=%.darwin) hack/boilerplate.go.txt

run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMAGE}:latest .

docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMAGE}:latest

build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && kustomize edit set image controller=${IMAGE}
	kustomize build config/default > dist/install.yaml

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Versions
KUSTOMIZE_VERSION ?= v5.7.1
CONTROLLER_TOOLS_VERSION ?= v0.19.0
#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')
GOLANGCI_LINT_VERSION ?= v2.4.0
GINKGO_VERSION ?= v1.16.5

kustomize: ## Download kustomize globally if necessary.
	go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)

controller-gen: ## Download controller-gen globally if necessary.
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

setup-envtest: envtest ## Download the binaries required for ENVTEST.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@setup-envtest use $(ENVTEST_K8S_VERSION) -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

setup-ginkgo: ## Download ginkgo globally if necessary.
	go install github.com/onsi/ginkgo/ginkgo@$(GINKGO_VERSION)

envtest: ## Download setup-envtest globally if necessary.
	go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)

golangci-lint: ## Download golangci-lint globally if necessary.
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

