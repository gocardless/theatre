PROG=bin/manager bin/acceptance
PROJECT=github.com/lawrencejones/theatre
VERSION := $(shell git rev-parse --short HEAD)-dev
BUILD_COMMAND := go build -ldflags "-X github.com/gocardless/theatre/cmd/manager.Version=$(VERSION)"

.PHONY: all test codegen deploy clean docker-build docker-push

all: $(PROG)

# Specific linux build target, making it easy to work with the docker acceptance
# tests on OSX
bin/%.linux_amd64:
	GOOS=linux GOARCH=amd64 $(BUILD_COMMAND) -a -o $@ cmd/$*/$*.go

bin/%:
	$(BUILD_COMMAND) -o $@ cmd/$*/$*.go

test:
	ginkgo -v ./...

codegen:
	vendor/k8s.io/code-generator/generate-groups.sh all \
		$(PROJECT)/pkg/client \
		$(PROJECT)/pkg/apis \
		rbac:v1alpha1

deploy:
	kustomize build config/base | kubectl apply -f -

clean:
	rm -rvf dist $(PROG) $(PROG:%=%.linux_amd64)

docker-build:
	docker build -t gcr.io/lawrjone/theatre:latest .

docker-push: docker-build
	docker push gcr.io/lawrjone/theatre:latest
