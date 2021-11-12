# Theatre [![CircleCI](https://circleci.com/gh/gocardless/theatre.svg?style=svg)](https://circleci.com/gh/gocardless/theatre)

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
- [Default priority classes](apis/workloads/v1alpha1/README.md): Mutate all pods within a
  namespace to set a default priority class.

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

1. Install Docker Desktop for macOS from https://www.docker.com/get-started

2. Install Go, kubectl, kustomize, and kind via Homebrew
```shell
brew install go kubernetes-cli kustomize kind
```

Note: kubectl is already included with Docker Desktop, so you might get a message
about the command getting installed by Homebrew but not linked.

3. (Optional) Download and install Kubebuilder

This step is not required for running acceptance tests. It is only required
for writing new Kubernetes operators.

We are using an older version of Kubebuilder, which doesn't have pre-built
binaries for Apple silicon. Therefore, if your workstation is an ARM64 Mac
then you can either run the x86 binaries (which will run in emulation mode on
Rosetta) or you can build Kubebuidler from sources.

- Option A: pre-built binaries (for Intel Macs or in emulation mode on M1 Macs)
```shell
sudo mkdir /usr/local/kubebuilder
curl -fLs https://github.com/kubernetes-sigs/kubebuilder/releases/download/v2.3.1/kubebuilder_2.3.1_darwin_amd64.tar.gz | \
  sudo tar x --strip=1 -C /usr/local/kubebuilder
```

- Option B: build form source (suitable for both Intel and M1 Macs)
```shell
git clone https://github.com/kubernetes-sigs/kubebuilder.git
cd kubebuilder
git checkout v2.3.1
make build
sudo mv bin /usr/local/kubebuilder/
```

After the installation, set environment variable for Kubebuilder:
```shell
export KUBEBUILDER_ASSETS=/usr/local/kubebuilder/bin
```

## Local development environment

Running `make` will compile binaries into `bin`
  
```shell
git clone git@github.com:gocardless/theatre.git
cd theatre
make
```

For developing changes, you can make use of the acceptance testing
infrastructure to install the code into a local Kubernetes-in-Docker
([Kind][kind]) cluster.

Ensure `kind` is installed (as per the [getting started
steps][#getting-started]) and then run the following, which
launchs a Kubernetes in Kind and installs Theatre.

```shell
go run cmd/acceptance/main.go prepare
```

At this point a development cluster has been provisioned. Your current local
Kubernetes context will have been changed to point to the test cluster. You
should see the following if you inspect kubernetes:

```console
$ kubectl --context kind-e2e get pods --all-namespaces | grep -v kube-system
NAMESPACE            NAME                                        READY   STATUS    RESTARTS   AGE
cert-manager         cert-manager-9b8969d86-ktp2m                1/1     Running   0          45s
cert-manager         cert-manager-cainjector-8545fdf87c-vt8s7    1/1     Running   0          45s
cert-manager         cert-manager-webhook-8c5db9fb6-74jz2        1/1     Running   0          45s
local-path-storage   local-path-provisioner-5bf465b47d-8ngbh     1/1     Running   0          114s
theatre-system       theatre-rbac-manager-0                      1/1     Running   0          5s
theatre-system       theatre-vault-manager-0                     1/1     Running   0          5s
theatre-system       theatre-workloads-manager-0                 1/1     Running   0          5s
vault                vault-0                                     1/1     Running   0          45s
```

All of the controllers and webhooks, built from the local working copy of the
code, have been installed into the cluster.

As this is a fully-fledged Kubernetes cluster, at this point you are able to
interact with it as you would with any other cluster, but also have the ability
to use the custom resources defined in theatre, e.g. creating a `Console`.

If changes are made to the code, then you must re-run the `prepare` step in
order to update the cluster with images built from the new binaries.

After you are done, you can tear down the cluster with:
```shell
go run cmd/acceptance/main.go destroy
```

[kind]: https://github.com/kubernetes-sigs/kind

## Tests

Theatre has test suites at several different levels, each of which play a
specific role. All of these suites are written using the [Ginkgo][ginkgo]
framework.

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
