# ArgoCD CI/CD Backend

This package implements the `cicd.Deployer` interface using ArgoCD's REST API, enabling the rollback controller to perform faster rollbacks by bypassing the normal CI pipeline and directly updating an ArgoCD application's target revision and syncing it.

## How It Works

1. **Resolve application name** — derives the ArgoCD application name from the `argocd_app_name` deployment option, or by rendering the `--argocd-app-name-template` with the rollback's `Namespace` and `Target`.
2. **Update application** — patches the ArgoCD application via `PATCH /api/v1/applications/{name}`, setting:
   - `spec.source.targetRevision` to the value of `target_revision`
   - `spec.source.plugin.env[REVISION]` to the value of `app_revision` (if provided)
3. **Sync application** — triggers a sync via `POST /api/v1/applications/{name}/sync`.
4. **Poll status** — `GetDeploymentStatus` fetches the application via `GET /api/v1/applications/{name}` and maps `status.operationState.phase` to a `cicd.DeploymentStatus`.

## Configuration

To use the ArgoCD backend, pass the following flags to the rollback controller:

| Flag                         | Required | Description                                                                                                                                                                                                                   |
| ---------------------------- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--cicd-backend=argocd`      | Yes      | Selects the ArgoCD backend                                                                                                                                                                                                    |
| `--argocd-server-url`        | Yes      | Base URL of your ArgoCD server (e.g. `https://argocd.yourcompany.dev`)                                                                                                                                                        |
| `--argocd-auth-token`        | Yes      | Authentication token for a robot account defined in `argocd-cm` (e.g. `accounts.theatre: apiKey`)                                                                                                                             |
| `--argocd-app-name-template` | Yes\*    | Go template for deriving the application name (e.g. `compute-lab-{{.Namespace}}-{{.Target}}`). Available fields: `.Namespace`, `.Target`. \*Not required if every Rollback provides `argocd_app_name` as a deployment option. |

## Deployment Options

These are set per Rollback resource under `deploymentOptions`.

### Required

| Key               | Description                                                                                                                                                                     |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `target_revision` | The infrastructure revision to deploy. Can be a literal revision string or a JSONPath expression into the Release (e.g. `{.config.revisions[?(@.name=="infrastructure")].id}`). |

### Optional

| Key               | Description                                                                                                                                                                                |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `app_revision`    | The application revision. Same format as `target_revision` (e.g. `{.config.revisions[?(@.name=="application")].id}`). Set as the `REVISION` plugin env variable on the ArgoCD application. |
| `argocd_app_name` | Override the application name derived from `--argocd-app-name-template`. Useful when a specific application does not follow the standard naming convention.                                |

## Status Mapping

The deployer maps ArgoCD `status.operationState.phase` to `cicd.DeploymentStatus` as follows:

| ArgoCD Phase            | Deployment Status |
| ----------------------- | ----------------- |
| `Running`               | `InProgress`      |
| `Succeeded`             | `Succeeded`       |
| `Error` / `Failed`      | `Failed`          |
| _(no active operation)_ | `Pending`         |
