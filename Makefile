PROG=bin/rbac-manager bin/workloads-manager bin/vault-manager bin/theatre-envconsul
PROJECT=github.com/gocardless/theatre
IMAGE=eu.gcr.io/gc-containers/gocardless/theatre
VERSION=$(shell git rev-parse --short HEAD)-dev
BUILD_COMMAND=go build -ldflags "-s -w -X main.Version=$(VERSION)"

.PHONY: build build-darwin build-linux build-all test codegen deploy clean docker-build docker-pull docker-push docker-tag manifests

build: $(PROG)
build-darwin: $(PROG:=.darwin_amd64)
build-linux: $(PROG:=.linux_amd64)
build-all: build-darwin build-linux

# Specific linux build target, making it easy to work with the docker acceptance
# tests on OSX
bin/%.linux_amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(BUILD_COMMAND) -a -o $@ cmd/$*/main.go

bin/%.darwin_amd64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(BUILD_COMMAND) -a -o $@ cmd/$*/main.go

bin/%:
	$(BUILD_COMMAND) -o $@ cmd/$*/main.go

# go get -u github.com/onsi/ginkgo/ginkgo
test:
	ginkgo -v -r

codegen:
	vendor/k8s.io/code-generator/generate-groups.sh all \
		$(PROJECT)/pkg/client \
		$(PROJECT)/pkg/apis \
		"rbac:v1alpha1 workloads:v1alpha1"

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

# We place manifests in a non-standard location, config/base/crds, rather than
# config/crds. This means we can provide a more idiomatic kustomize structure,
# but requires us to move the files after generation.
#
# npm install -g prettier
manifests:
	go run vendor/sigs.k8s.io/controller-tools/cmd/controller-gen/main.go all \
		&& rm -rfv config/base/crds \
		&& mv -v config/crds config/base/crds \
		&& prettier --parser yaml --write config/base/crds/*
