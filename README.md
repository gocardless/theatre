# Theatre

This project contains GoCardless' Kubernetes extensions, in the form of
operators, admission controller webhooks and associated CLIs. The aim of this
project is to provide a space to write Kubernetes extensions where:

1. Doing the right thing is easy; it is difficult to make mistakes!
2. Each category of Kubernetes extension has a well defined implementation pattern
3. Writing meaningful tests is easy, with minimal boilerplate

## API Groups

Theatre provides various extensions to vanilla Kubernetes. These extensions are
grouped under separate API groups, all of which exist under the
`*.crd.gocardless.com` namespace.

### RBAC

Utilities to extend the default Kubernetes role-based access control (RBAC)
resources.
These CRDs are motivated by real-world use cases when using
Kubernetes with an organisation that uses GSuite, and which frequently onboards
new developers.

- [`DirectoryRoleBinding`][sample-drb] is a resource that provisions standard
  `RoleBinding`s, which contain the subjects defined in a  Google group.

> Note: In a GKE Kubernetes cluster this may soon be superseded by the [Google
> Groups for GKE][gke-groups] functionality.

[sample-drb]: config/samples/rbac_v1alpha1_directoryrolebinding.yaml
[gke-groups]: https://cloud.google.com/kubernetes-engine/docs/how-to/role-based-access-control#google-groups-for-gke

### Workloads

Extends core workload resources with new CRDs. Extensions within this group can be
expected to create or mutate pods, deployments, etc.

- [Consoles](controllers/workloads/console/README.md): Provide engineers with a temporary
  dedicated pod to perform operational tasks, avoiding the need to provide
  `pods/exec` permissions on production workloads.

### [Vault](apis/vault/v1alpha1/README.md)

Utilities for interacting with Vault. Primarily used to inject secret material
into pods by use of annotations.

- `secrets-injector.vault.crd.gocardless.com` webhook for injecting the
  `theatre-secrets` tool to populate a container's environment with secrets
  from Vault before executing.

## Command line interfaces

As well as Kubernetes controllers this project also contains supporting CLI
utilities.

### theatre-consoles

`theatre-consoles` is a suite of commands that provides the ability to create,
list, attach to and authorise [consoles](#workloads).

Run: `go run cmd/theatre-consoles/main.go`

### theatre-secrets

See the [command README](cmd/theatre-secrets/README.md) for further details.

Run: `go run cmd/theatre-secrets/main.go`

## Getting Started

Theatre assumes developers have several tools installed to provide development
and testing capabilities. The following will configure a macOS environment with
all the necessary dependencies:

```shell
brew cask install docker
brew install go kubernetes-cli kustomize kind
sudo mkdir /usr/local/kubebuilder
curl -fsL "https://github.com/kubernetes-sigs/kubebuilder/releases/download/v2.3.1/kubebuilder_2.3.1_$(go env GOOS)_$(go env GOARCH).tar.gz" | sudo tar -xvz --strip=1 -C /usr/local/kubebuilder
export KUBEBUILDER_ASSETS=/usr/local/kubebuilder/bin
```

Running `make` should now compile binaries into `bin`.

## Local development environment

For developing changes, you can make use of the acceptance testing
infrastructure to install the code into a local Kubernetes-in-Docker
([Kind][kind]) cluster.
Ensure `kind` is installed (as per the [getting started
steps][#getting-started]) and then run the following:


```
go run cmd/acceptance/main.go prepare # prepare the cluster, install theatre
```

At this point a development cluster has been provisioned. Your current local
Kubernetes context will have been changed to point to the test cluster. You
should see the following if you inspect kubernetes:

```console
$ kubectl get pods --all-namespaces | grep -v kube-system
NAMESPACE        NAME                                        READY   STATUS    RESTARTS   AGE
theatre-system   theatre-rbac-manager-0                      1/1     Running   0          5m
theatre-system   theatre-vault-manager-0                     1/1     Running   0          5m
theatre-system   theatre-workloads-manager-0                 1/1     Running   0          5m
vault            vault-0                                     1/1     Running   0          5m
```

All of the controllers and webhooks, built from the local working copy of the
code, have been installed into the cluster.

As this is a fully-fledged Kubernetes cluster, at this point you are able to
interact with it as you would with any other cluster, but also have the ability
to use the custom resources defined in theatre, e.g. creating a `Console`.

If changes are made to the code, then you must re-run the `prepare` step in
order to update the cluster with images built from the new binaries.

[kind]: https://github.com/kubernetes-sigs/kind

## Tests

Theatre has test suites at several different levels, each of which play a
specific role. All of these suites are written using the [Ginkgo][ginkgo]
framework.

In order to setup your local testing environment for unit and integration tests do the following:

```bash
$ # install setup-envtest which configures etcd and kube-apiserver binaries for envtest
$ # https://book.kubebuilder.io/reference/envtest.html#configuring-envtest-for-integration-tests
$ # https://github.com/kubernetes-sigs/controller-runtime/tree/master/tools/setup-envtest#envtest-binaries-manager
$ go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
$ # configure envtest to use k8s 1.24.x binaries
$ setup-envtest use -p path 1.24.x
$ source <(setup-envtest use -i -p env 1.24.x)
```

- **Unit**: Standard unit tests, used to exhaustively specify the functionality of
  functions or objects.

  Invoked with the `ginkgo` CLI.

  [Example unit test](apis/workloads/v1alpha1/helpers_test.go).

- **Integration**: Integration tests run the custom controller code and
  integrates this with a temporary Kubernetes API server, therefore providing an
  environment where the Kubernetes API can be used to manipulate custom objects.

  This environment has no Kubernetes nodes, and does not run any other
  controllers such as `kube-controller-manager`, therefore it will not run pods.

  These suites provide a good balance between runtime and realism, and are
  therefore useful for rapid iteration when developing changes.

  Invoked with the `ginkgo` CLI.

  [Example integration test](apis/workloads/v1alpha1/integration/priority_integration_test.go).

- **Acceptance**: Acceptance is used for full end-to-end (E2E) tests,
  provisioning a fully functional Kubernetes cluster with all custom controllers
  and webhooks installed.

  The acceptance tests are much slower to set up, and so are typically used
  sparingly compared to the other suites, but provide essential validation in CI
  and at the end of development cycles that the code correctly interacts with
  the other components of a Kubernetes cluster.

  Invoked with: `go run cmd/acceptance/main.go run`

  [Example acceptance test](cmd/workloads-manager/acceptance/acceptance.go).

[ginkgo]: https://onsi.github.io/ginkgo
