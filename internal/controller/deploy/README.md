# Deploy Controllers Overview

This directory contains the controllers for the Deploy API group, which manages release resources.

## Controllers

- `release_controller.go` - Manages Release resources
  - `release_analysis.go` - Health analysis reconciliation for Release resources
  - `release_culling.go` - Release culling logic

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
