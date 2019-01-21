# Theatre [![CircleCI](https://circleci.com/gh/gocardless/theatre.svg?style=svg)](https://circleci.com/gh/gocardless/theatre)

This project contains my personal experiments with Kubernetes operators. The aim
is to trial several methods of implementing operators and arrive at a
development environment that is welcoming to developers who are unfamiliar with
this type of development.

The following checklist details my aims for exploring operator development
generally, separate from the features I intend to provide via the CRDs in this
package:

- [x] Verify all API groups are registered
- [x] Integrate Kubernetes events with logging
- [ ] Use caching informers to power controllers
- [x] Unit/integration testing
- [ ] Acceptance testing with real kubernetes
- [ ] Auto-install CRDs into the cluster
- [x] Log changes to Kubernetes resources as events
- [ ] Auto-generate:
  - [ ] RBAC roles for the manager
  - [ ] Stateful set for the deployment
- [ ] Decide on deployment strategy

To help me personally, my next steps are:

- [x] Cache directory lookups
- [x] Support pagination for Google directory lookups
- [x] Restructure DirectoryRoleBinding to use Spec
- [ ] Review small packages, consolidate where necessary, add unit tests
- [ ] Document installation procedure
- [ ] Investigate installing webhooks: can we test admission webhooks with the
      integration test suite?

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
CRDs are motivated by problems I've seen using Kubernetes with an organisation
that works with GSuite, and frequently onboards new developers.

#### `DirectoryRoleBinding`

Kubernetes- and even GKE- lacks support for integrating RBAC with Google groups.
Often organisations make use of Google groups as a directory system, and this
CRD extends the native Kubernetes RoleBinding resource to provide the
`GoogleGroup` subject.

- [x] Can manage permissions using GSuite groups
- [x] Has unit tests
- [x] Has integration tests
- [ ] Has acceptance tests
- [x] Refactor to support a more standard CRD interface

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
