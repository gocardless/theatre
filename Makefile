PROG=bin/rbac-manager
IMAGE=eu.gcr.io/gc-containers/gocardless/theatre
VERSION=$(shell git rev-parse --short HEAD)-dev
BUILD_COMMAND=go build -ldflags "-s -w -X main.Version=$(VERSION)"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

.PHONY: build build-darwin build-linux build-all test generate manifests controller-gen deploy clean docker-build

build: $(PROG)
build-darwin: $(PROG:=.darwin_amd64)
build-linux: $(PROG:=.linux_amd64)
build-all: build-darwin build-linux

# Specific linux build target, making it easy to work with the docker acceptance
# tests on OSX
bin/%.linux_amd64: vet
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(BUILD_COMMAND) -a -o $@ ./cmd/$*/.

bin/%.darwin_amd64: vet
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(BUILD_COMMAND) -a -o $@ ./cmd/$*/.

bin/%: vet
	CGO_ENABLED=0 GOARCH=amd64 $(BUILD_COMMAND) -o $@ ./cmd/$*/.

# go get -u github.com/onsi/ginkgo/ginkgo
test:
	ginkgo -r

fmt:
	go fmt ./...

vet:
	go vet ./...

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object paths="./apis/..."

manifests: generate
	$(CONTROLLER_GEN) crd paths="./apis/..." output:crd:artifacts:config=config/base/crds

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.2.5 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

clean:
	rm -rvf $(PROG) $(PROG:%=%.linux_amd64) $(PROG:%=%.darwin_amd64)
