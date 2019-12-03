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
make linux # build the linux binaries
go run cmd/acceptance/main.go prepare # prepare the cluster, install theatre
```

At this point a development cluster has been provisioned. You can access this
cluster using the following, as is suggested to save as an alias in your shell
environment:

```
function e2e() {
  export KUBECONFIG="$(kind get kubeconfig-path --name="e2e")"
}

$ e2e
$ kubectl get pods --all-namespaces
NAMESPACE        NAME                                             READY   STATUS    RESTARTS   AGE
kube-system      coredns-86c58d9df4-6mw6z                         1/1     Running   0          12m
kube-system      coredns-86c58d9df4-fhnwg                         1/1     Running   0          12m
kube-system      etcd-kind-e2e-control-plane                      1/1     Running   0          11m
kube-system      kube-apiserver-kind-e2e-control-plane            1/1     Running   0          11m
kube-system      kube-controller-manager-kind-e2e-control-plane   1/1     Running   0          11m
kube-system      kube-proxy-rs29h                                 1/1     Running   0          12m
kube-system      kube-scheduler-kind-e2e-control-plane            1/1     Running   0          11m
kube-system      weave-net-jzx8c                                  2/2     Running   0          12m
theatre-system   theatre-rbac-manager-0                           1/1     Running   7          12m
theatre-system   theatre-workloads-manager-0                      1/1     Running   0          4m52s
```

To develop against this cluster, either run the managers locally with the
exported Kubernetes config, or re-run `make linux` followed by acceptance
prepare flow.

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
