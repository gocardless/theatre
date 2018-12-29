PROJECT=github.com/lawrencejones/theatre

.PHONY: test codegen deploy docker-build docker-push

test:
	go test -v ./...

codegen:
	vendor/k8s.io/code-generator/generate-groups.sh all \
		$(PROJECT)/pkg/client \
		$(PROJECT)/pkg/apis \
		rbac:v1alpha1

deploy:
	kustomize build config | kubectl apply -f -

docker-build:
	docker build -t gcr.io/lawrjone/theatre:latest .

docker-push:
	docker push gcr.io/lawrjone/theatre:latest
