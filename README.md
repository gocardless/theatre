# Theatre

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

To help me personally, my next steps are:

- Modify sudorolebindings to use new controller Add interface
- Review small packages, consolidate where necessary, add unit tests
- Document installation procedure
- Investigate installing webhooks: can we test admission webhooks with the
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

## CRDs

### `rbac.lawrjone.xyz`

Collection of utilities to extend the default Kubernetes RBAC resources. These
CRDs are motivated by problems I've seen using Kubernetes with an organisation
that works with GSuite, and frequently onboards new developers.

#### `DirectoryRoleBinding`

Kubernetes- and even GKE- lacks support for integrating RBAC with Google groups.
Often organisations make use of Google groups as a directory system, and this
CRD extends the native Kubernetes RoleBinding resource to provide the
`GoogleGroup` subject.

- [x] Can manage permissions using GSuite groups
- [ ] Has unit tests
- [ ] Has acceptance tests
- [x] Refactor to support a more standard CRD interface

```yaml
---
apiVersion: rbac.lawrjone.xyz/v1alpha1
kind: DirectoryRoleBinding
metadata:
  name: platform-superuser
spec:
  roleBinding:
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

#### `SudoRoleBinding`

In normal cluster usage, you don't want to be using a superadmin account to do
your work. Doing so runs the disk of causing irrevocable damage to the cluster
if any developer accidentally targets the wrong resource: I've personally
witnessed a developer accidentally destroy every persistent disk in the cluster
through a scripting error.

This CRD would permit developers to temporarily add themselves into a
RoleBinding that provides the capabilities they need, then remove them once
their grant expires. It functions like `sudo` in a normal linux box, caching
your permissions for a small period of time.

- [x] Manages membership into a RoleBinding
- [ ] Exposes mechanism to trigger permission elevation
