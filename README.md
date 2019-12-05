# Theatre [![CircleCI](https://circleci.com/gh/gocardless/theatre.svg?style=svg)](https://circleci.com/gh/gocardless/theatre)

This project contains GoCardless Kubernetes extensions, mostly in the form of
operators and admission controller webhooks. The aim of this project is to
provide a space to write Kubernetes extensions where:

1. Doing the right thing is easy; it is difficult to make mistakes!
2. Each category of Kubernetes extension has a well defined implementation pattern
3. Writing meaningful tests is easy, with minimal boilerplate

## Getting Started

Theatre assumes developers have several tools installed to ensure their
environment can run sanely. The following will configure an OSX user with all
the necessary dependencies:

```shell
brew cask install docker
brew install go@1.13.4 kubernetes-cli
curl -fsL -o /usr/local/bin/kind https://github.com/kubernetes-sigs/kind/releases/download/v0.6.0/kind-darwin-amd64 \
  && chmod a+x /usr/local/bin/kind
curl -fsL https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv3.4.0/kustomize_v3.4.0_darwin_amd64.tar.gz \
  | tar -xvz -C /usr/local/bin
mkdir /usr/local/kubebuilder
curl -fsL https://github.com/kubernetes-sigs/kubebuilder/releases/download/v1.0.7/kubebuilder_1.0.7_darwin_amd64.tar.gz \
  | tar -xvz --strip=1 -C /usr/local/kubebuilder
```

Running `make` should now compile binaries into `bin`.

## Deploying locally

For testing development changes, you can make use of the acceptance testing
infrastructure to install the code into a local kubernetes-in-docker cluster.
Ensure `kind` is installed (as per getting started steps) and then run the
following:

```
go run cmd/acceptance/main.go prepare # prepare the cluster, install theatre
```

At this point a development cluster has been provisioned. Your local kubernetes
context will have been adapted to point at the test cluster. You should see the
following if you inspect kubernetes:

```shell
kubectl get pods --namespace theatre-system | grep -v kube-system
NAMESPACE        NAME                                        READY   STATUS    RESTARTS   AGE
theatre-system   theatre-rbac-manager-0                      1/1     Running   0          25h
theatre-system   theatre-vault-manager-0                     1/1     Running   0          25h
theatre-system   theatre-workloads-manager-0                 1/1     Running   0          25h
vault            vault-0                                     1/1     Running   0          4h24m
```

## API Groups

### RBAC

Collection of utilities to extend the default Kubernetes RBAC resources. These
CRDs are motivated by real-world use cases when using Kubernetes with an
organisation that using GSuite, and which frequently onboards new developers.

- `DirectoryRoleBinding` supports Google groups in `RoleBinding`s

### Workloads

Extends core workload resources with new CRDs. This functionality can be
expected to create pods, deployments, etc.

- `Console` a one-shot job created by a human operator from a `ConsoleTemplate`
- `ConsoleTemplate` specifies how `Console` pods should be created, and who has
  access to create them

### Vault

Utilities for interacting with Vault. Primarily used to inject secret material
into pods by use of annotations.

- `envconsul-injector.vault.crd.gocardless.com` webhook for injecting theatre
  utilities that pull secrets from Vault
