# Theatre [![CircleCI](https://circleci.com/gh/gocardless/theatre.svg?style=svg)](https://circleci.com/gh/gocardless/theatre)

This project contains GoCardless Kubernetes extensions, mostly in the form of
operators and admission controller webhooks. The aim of this project is to
provide a space to write Kubernetes extensions where:

1. Doing the right thing is easy; it is difficult to make mistakes!
2. Each category of Kubernetes extension has a well defined implementation pattern
3. Writing meaningful tests is easy, with minimal boilerplate

## TODO

Theatre is under active development and we're still working out what works best
for this type of software development. Below is an on-going list of TODOs that
would help us move toward our three primary goals:

- [x] Verify all API groups are registered
- [x] Integrate Kubernetes events with logging
- [x] Investigate installing webhooks: can we test admission webhooks with the
      integration test suite?
- [ ] Use caching informers to power controllers
- [x] Unit/integration testing
- [ ] Acceptance testing with real kubernetes
- [ ] Auto-install CRDs into the cluster
- [x] Log changes to Kubernetes resources as events
- [ ] Auto-generate:
  - [ ] RBAC roles for the manager
  - [ ] Stateful set for the deployment
- [ ] Decide on deployment strategy
- [ ] Document installation procedure

## Getting Started

Theatre assumes developers have several tools installed to ensure their
environment can run sanely. The following will configure an OSX user with all
the necessary dependencies:

```shell
brew cask install docker
brew install go@1.11 kubernetes-cli kustomize
go get -u sigs.k8s.io/kind 
curl -fsL -o /usr/local/bin/kustomize https://github.com/kubernetes-sigs/kustomize/releases/download/v1.0.11/kustomize_1.0.11_darwin_amd64
mkdir /usr/local/kubebuilder
curl -fsL https://github.com/kubernetes-sigs/kubebuilder/releases/download/v1.0.7/kubebuilder_1.0.7_linux_amd64.tar.gz \
  | tar -xvz --strip=1 -C /usr/local/kubebuilder
```

Running `make` should now compile binaries into `bin`.

## Deploying to development environments

The Kustomize deployment that can be triggered with a `make deploy` will
configure managers that point at the latest docker image tag. While this project
is still in alpha, it may be useful for people to deploy a development image.
You can do so by tagging your current docker image as latest, after the
container has been built by CI:

```shell
git push # wait for CI to build container
make docker-pull docker-tag docker-push # assigns latest tag to current SHA
make deploy # deploys to the currently active cluster
kubectl delete pod -l app=theatre # optionally restart pods
```

## CRDs

### `rbac.crd.gocardless.com`

Collection of utilities to extend the default Kubernetes RBAC resources. These
CRDs are motivated by real-world use cases when using Kubernetes with an
organisation that using GSuite, and which frequently onboards new developers.

#### `DirectoryRoleBinding`

Kubernetes- and even GKE- lacks support for integrating RBAC with Google groups.
Often organisations make use of Google groups as a directory system, and this
CRD extends the native Kubernetes RoleBinding resource to provide the
`GoogleGroup` subject.

```yaml
---
apiVersion: rbac.crd.gocardless.com/v1alpha1
kind: DirectoryRoleBinding
metadata:
  name: platform-superuser
spec:
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: ClusterRole
    name: superuser
  subjects:
    - kind: GoogleGroup
      name: platform@gocardless.com
    - kind: User
      name: hmac@gocardless.com
```
