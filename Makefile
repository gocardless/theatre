PROJECT=github.com/lawrencejones/theatre

.PHONY: test codegen

test:
	go test -v ./...

codegen:
	vendor/k8s.io/code-generator/generate-groups.sh all \
		$(PROJECT)/pkg/client \
		$(PROJECT)/pkg/apis \
		rbac:v1alpha1
