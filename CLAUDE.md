# Theatre — CLAUDE.md

## Project Overview

Theatre is GoCardless' Kubernetes extensions project, providing operators, admission controller webhooks, and supporting CLIs. The Go module is `github.com/gocardless/theatre/v5`.

## Repository Structure

- `api/` — CRD API types (RBAC, Vault, Workloads)
- `internal/` — Controllers and webhooks (unexported)
- `pkg/` — Shared/exported packages
- `cmd/` — CLI entry points (`rbac-manager`, `vault-manager`, `workloads-manager`, `theatre-consoles`, `theatre-secrets`, `acceptance`)
- `config/` — Kustomize manifests, CRDs, base configs

## Key API Groups

- **RBAC** (`rbac.crd.gocardless.com`) — `DirectoryRoleBinding`: provisions `RoleBinding`s from Google group members
- **Workloads** (`workloads.crd.gocardless.com`) — `Console`: temporary dedicated pods for operational tasks
- **Vault** (`vault.crd.gocardless.com`) — `secrets-injector` webhook for injecting Vault secrets into pods

## Build & Development Commands

```shell
make build           # Build all binaries
make test            # Run unit + integration tests (requires setup-envtest)
make lint            # Run golangci-lint
make lint-fix        # Run golangci-lint with auto-fix
make fmt             # go fmt ./...
make vet             # go vet ./...
make generate        # Generate DeepCopy methods via controller-gen
make manifests       # Generate CRDs/RBAC/Webhook configs via controller-gen
make acceptance-e2e  # Full E2E: prepare Kind cluster + run + destroy
make acceptance-run  # Run acceptance tests against existing cluster
make install-tools   # Download all dev tool binaries into ./bin/
```

## Testing

Tests use [Ginkgo](https://onsi.github.io/ginkgo) and run with `-race -randomizeSuites -randomizeAllSpecs`.

**Setup before running tests:**

```shell
make install-tools
eval $(setup-envtest use -i -p env 1.24.x)
```

Three test levels:

- **Unit** — `make test` (fast, no cluster needed)
- **Integration** — `make test` (uses `envtest` with a temporary API server, no nodes)
- **Acceptance** — `make acceptance-e2e` (full Kind cluster, slow, used sparingly)

## Local Development Cluster (Kind)

```shell
make build
make test
make acceptance-e2e
# or step-by-step:
make prepare  # provisions Kind cluster
make acceptance-run --verbose
make acceptance-destroy
```

Re-run `make acceptance-prepare` after any code changes to rebuild and redeploy images.

## Toolchain

- **Go**
- **controller-gen** — CRD/webhook/RBAC manifest generation
- **kustomize**
- **golangci-lint**
- **ginkgo**
- **setup-envtest** — manages `etcd`/`kube-apiserver` binaries for integration tests
- **kind** — Kubernetes-in-Docker for acceptance tests

All tool binaries are installed locally into `./bin/` via `make install-tools`.
