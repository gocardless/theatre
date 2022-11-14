PROG=bin/rbac-manager bin/vault-manager bin/theatre-secrets bin/workloads-manager bin/theatre-consoles
PROJECT=github.com/gocardless/theatre
IMAGE=eu.gcr.io/gc-containers/gocardless/theatre
VERSION=$(shell git rev-parse --short HEAD)-dev
BUILD_COMMAND=go build -ldflags "-s -w -X main.Version=$(VERSION)"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

.PHONY: build build-darwin build-linux build-all test generate manifests deploy clean docker-build docker-pull docker-push docker-tag controller-gen

build: $(PROG)
build-darwin: $(PROG:=.darwin_amd64)
build-linux: $(PROG:=.linux_amd64)
build-all: build-darwin build-linux

# Specific linux build target, making it easy to work with the docker acceptance
# tests on OSX
bin/%.linux_amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(BUILD_COMMAND) -a -o $@ ./cmd/$*/.

bin/%.darwin_amd64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(BUILD_COMMAND) -a -o $@ ./cmd/$*/.

bin/%:
	CGO_ENABLED=0 GOARCH=amd64 $(BUILD_COMMAND) -o $@ ./cmd/$*/.

# go install github.com/onsi/ginkgo/ginkgo@v1.16.5
# go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
# setup-envtest use -p path 1.22.x
# source <(setup-envtest use -i -p env 1.22.x)
test:
	ginkgo -race -r ./...

vet:
	go vet ./cmd/rbac-manager/...
	go vet ./cmd/theatre-consoles/...
	go vet ./cmd/theatre-secrets/...
	go vet ./cmd/vault-manager/...
	go vet ./cmd/workloads-manager/...

generate: controller-gen
	$(CONTROLLER_GEN) object paths="./apis/rbac/..."
	$(CONTROLLER_GEN) object paths="./apis/workloads/..."

manifests: generate
	$(CONTROLLER_GEN) crd paths="./apis/rbac/..." output:crd:artifacts:config=config/base/crds
	$(CONTROLLER_GEN) crd paths="./apis/workloads/..." output:crd:artifacts:config=config/base/crds

deploy:
	kustomize build config/base | kubectl apply -f -

deploy-production:
	kustomize build config/overlays/production | kubectl apply -f -

clean:
	rm -rvf $(PROG) $(PROG:%=%.linux_amd64) $(PROG:%=%.darwin_amd64)

docker-build:
	docker build -t $(IMAGE):latest .

docker-pull:
	docker pull $(IMAGE):$$(git rev-parse HEAD)

docker-push:
	docker push $(IMAGE):latest

docker-tag:
	docker tag $(IMAGE):$$(git rev-parse HEAD) $(IMAGE):latest

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.9.2 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif
