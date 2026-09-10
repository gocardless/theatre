# Theatre

Kubernetes operators, admission webhooks, and CLIs. Module path is
`github.com/gocardless/theatre/v5` — internal imports need the `/v5`.

Read `README.md` for architecture and CRD semantics. Run `make help` for the target list.

## Conventions

- Editing `api/**` types: run `make manifests generate`, then commit the regenerated
  `config/crd/bases/**` and `api/*/v1alpha1/zz_generated.deepcopy.go`. Never hand-edit them.
- `make test` provisions envtest binaries itself and derives the Kubernetes version from
  `k8s.io/api` — no manual `setup-envtest` step, no pinned version.
- New integration suites must set `Metrics: metricsserver.Options{BindAddress: "0"}`.
  The controller-runtime default binds `:8080`, and suites running concurrently collide.
- Tests run with `-randomize-suites -randomize-all`; specs must not depend on ordering.
- Acceptance tests (`make acceptance-e2e`) spin up a Kind cluster — slow, use sparingly.
  After code changes, re-run `make acceptance-prepare` to rebuild and redeploy images.
