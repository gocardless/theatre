# Deploy Controllers Overview

This directory contains the controllers for the Deploy API group, which manages release, rollback, and automated rollback policy resources.

## Controllers

- `release_controller.go` - Manages Release resources
  - `release_analysis.go` - Health analysis reconciliation for Release resources
  - `release_culling.go` - Release culling logic
- `rollback_controller.go` - Manages Rollback resources
- `automated_rollback_policy_controller.go` - Manages AutomatedRollbackPolicy resources

## Release Controller

Responsible for reconciling Release resources. In the context of GoCardless, Release CRs are created
only after a deployment has completed. Hence, the `Release` CR is using a `.config`, instead of a
`.spec`, as the controller doesn't have a desired state to get to.

### Annotation-driven status

Release status is driven by annotations set by external tooling (e.g. the CI/CD pipeline):

| Annotation                                     | Effect                                      |
| ---------------------------------------------- | ------------------------------------------- |
| `theatre.gocardless.com/active: "true"`        | Sets the `Active` condition to `True`       |
| `theatre.gocardless.com/deployment-start-time` | Sets `status.deploymentStartTime` (RFC3339) |
| `theatre.gocardless.com/deployment-end-time`   | Sets `status.deploymentEndTime` (RFC3339)   |
| `theatre.gocardless.com/previous-release`      | Sets `status.previousRelease.releaseRef`    |

### Health analysis

Health analysis is opt-in and must be enabled via the `AnalysisEnabled` controller flag. When
disabled, analysis is skipped entirely and the `Healthy` and `RollbackRequired` conditions remain
`Unknown`.

When enabled, the controller creates `AnalysisRun` resources (part of the Argo Rollouts project)
owned by the Release, and updates the Release status based on their results. Templates are matched
three ways:

- **By release labels** — namespaced `AnalysisTemplate` resources whose labels match the Release's labels
- **By custom selector** — namespaced and cluster-scoped templates matching the label selector in the `theatre.gocardless.com/analysis-selector` annotation
- **Global templates** — `ClusterAnalysisTemplate` resources with `global: "true"` label (opt-out per-release with annotation `theatre.gocardless.com/no-global-analysis: "true"`)

The label on an `AnalysisTemplate` controls which Release condition it feeds:

- `health: "true"` → contributes to the `Healthy` condition
- `rollback: "true"` → contributes to the `RollbackRequired` condition
- A single `AnalysisRun` can carry both labels and feed both conditions.

The `pre-release-timestamp` argument, if declared in a template, is automatically populated with a
Unix timestamp of `deploymentStartTime - 5s` to ensure that the analysis for `RollbackRequired` condition is ran against the state before the deployment.

### Release culling

The controller culls old releases to prevent unbounded growth. Culling behaviour:

- Only **inactive** releases are candidates for deletion
- Culling is skipped if there are not enough inactive candidates to safely reach the target limit
- A Kubernetes `Lease` object (named `theatre-release-cull-<hash>`) is used to prevent concurrent culls across multiple reconcile loops
- Default limit is **30** releases per target; configurable via the `theatre.gocardless.com/release-limit` annotation on the namespace
- Oldest releases (by `deploymentEndTime`, falling back to creation time) are deleted first

## Rollback Controller

Responsible for reconciling Rollback resources. Rollback resources are either created manually by a
user or automatically by the automated rollback controller. The Rollback controller is responsible
for initiating the rollback process through a configured CI/CD backend. As of time of writing, the
supported backends are:

- **GitHub Deployments** - implemented using the GitHub REST API (see `pkg/cicd/github`)
- **ArgoCD** - implemented using the ArgoCD REST API (see `pkg/cicd/argocd`)

The configured Rollback `.spec.deploymentOptions` will be passed to the chosen backend.

### Reconcile flow

1. On first reconcile, the controller records the currently active release as `status.fromReleaseRef`.
2. The controller triggers a deployment via the configured backend and sets `InProgress: True`.
3. The controller polls the backend every **15 seconds** until the deployment succeeds or fails.
4. On failure, the controller retries up to **3 attempts** total (trigger + re-polls). Retries only
   occur for errors the backend marks as retryable; non-retryable errors fail immediately.
5. On terminal success or failure, the `Succeeded` condition is set accordingly and reconciliation stops.

### Metrics

The controller registers the following Prometheus metrics:

- `rollbackTerminalTotal` — counter of rollbacks that reached a terminal state, labelled by outcome
- `rollbackCompletionDurationSeconds` — histogram of rollback duration from creation to completion
- `rollbackRetryCount` — histogram of the number of retries per rollback

## Automated Rollback Controller

Responsible for reconciling `AutomatedRollbackPolicy` resources and creating `Rollback` resources
when the configured trigger condition is met on the active release.

The controller watches `Release` objects and maps
them to their policy. A reconciliation is triggered only when:

- An `AutomatedRollbackPolicy` is created, updated, or deleted, **or**
- The policy's trigger condition **transitions** on an active `Release`

This predicate filters out all other Release update events, keeping reconcile load low.

**Note:** the controller expects exactly one `AutomatedRollbackPolicy` per target. If zero or more
than one policies exist for a target, Release update events for that target are silently dropped.

### Reconcile flow

1. Find the active `Release` for the policy's `targetName`.
2. Evaluate policy constraints (e.g. `spec.enabled`) and update the `Automated` status condition.
3. If rollback is allowed, check the trigger condition on the active release and confirm no
   `Rollback` resource already exists for it (deduplication via owner index).
4. If all checks pass, create a `Rollback` resource with `toReleaseRef.name` left empty — the
   Rollback controller resolves it to the latest healthy release.
5. Disable the policy after creating the rollback (`Automated: False`, reason `DisabledByController`).

The policy will be re-enabled when the next Release recovers from the trigger condition (i.e. the configured condition status changes to the opposite of the configured condition status).
