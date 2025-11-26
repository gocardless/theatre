PROG=bin/rbac-manager bin/vault-manager bin/theatre-secrets bin/workloads-manager bin/theatre-consoles bin/release-manager
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
	generate manifests deploy help \
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
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object paths="./..."

fmt: ## Run go fmt against code.
	go fmt ./...

vet: ## Run go vet against code.
	go vet ./...

test: manifests generate fmt vet setup-envtest setup-ginkgo ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" ginkgo -race -randomizeSuites -randomizeAllSpecs -r ./...

acceptance-e2e: install-tools acceptance-prepare acceptance-run acceptance-destroy ## Requires the following binaries: kubectl, kustomize, kind, docker

acceptance-run: install-tools ## Run acceptance tests
	go run cmd/acceptance/main.go run --verbose

acceptance-prepare: install-tools ## Prepare acceptance tests
	go run cmd/acceptance/main.go prepare --verbose

acceptance-destroy: install-tools ## Destroy acceptance tests
	go run cmd/acceptance/main.go destroy

lint: golangci-lint ## Run golangci-lint linter
	$(GOLANGCI_LINT) run

lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

lint-config: golangci-lint ## Verify golangci-lint linter configuration
	$(GOLANGCI_LINT) config verify

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
	rm -rvf $(PROG) $(PROG:%=%.linux) $(PROG:%=%.darwin)

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMAGE}:latest .

docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMAGE}:latest

build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMAGE}
	$(KUSTOMIZE) build config/default > dist/install.yaml

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
GINKGO = $(LOCALBIN)/ginkgo

## Tool Versions
KUSTOMIZE_VERSION ?= v5.7.1
CONTROLLER_TOOLS_VERSION ?= v0.19.0
#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')
GOLANGCI_LINT_VERSION ?= v2.4.0
GINKGO_VERSION ?= v1.16.5

install-tools: kustomize controller-gen setup-envtest golangci-lint setup-ginkgo

kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

setup-ginkgo: $(GINKGO) ## Download ginkgo locally if necessary.
$(GINKGO): $(LOCALBIN)
	go install github.com/onsi/ginkgo/ginkgo@$(GINKGO_VERSION)

envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $$(realpath $(1)-$(3)) $(1)
endef
