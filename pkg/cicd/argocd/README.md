# ArgoCD CI/CD Backend

This package implements the `cicd.Deployer` interface using ArgoCD's REST API, enabling the rollback controller to perform faster rollbacks by bypassing the normal CI pipeline and directly updating an ArgoCD application's target revision and syncing it.

## How It Works

1. **Resolve application name** — derives the ArgoCD application name from the `argocd_app_name` deployment option, or by rendering the `--argocd-app-name-template` with the rollback's `Namespace` and `Target`.
2. **Update application** — patches the ArgoCD application via `PATCH /api/v1/applications/{name}`, setting:
   - `spec.source.targetRevision` to the value of `target_revision`
   - `spec.source.plugin.env[REVISION]` to the value of `app_revision` (if provided)
3. **Sync application** — triggers a sync via `POST /api/v1/applications/{name}/sync`.
4. **Poll status** — `GetDeploymentStatus` fetches the application via `GET /api/v1/applications/{name}` and checks whether the application has synced to the target revision; falls back to `status.operationState.phase` to determine in-progress, failed, pending, or superseded states.

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

| Key               | Description                                                                                                                                 |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `target_revision` | The infrastructure revision to deploy. A JSONPath expression into the Release (e.g. `{.config.revisions[?(@.name=="infrastructure")].id}`). |

### Optional

| Key                      | Description                                                                                                                                                                                |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `app_revision`           | The application revision. Same format as `target_revision` (e.g. `{.config.revisions[?(@.name=="application")].id}`). Set as the `REVISION` plugin env variable on the ArgoCD application. |
| `argocd_app_name`        | Override the application name derived from `--argocd-app-name-template`. Useful when a specific application does not follow the standard naming convention.                                |
| `argocd_add_sync_window` | Add a sync window to the project to prevent automatic syncs after the deployment.                                                                                                          |

## Status Mapping

`GetDeploymentStatus` first checks whether the application has reached the desired revision, then falls back to `status.operationState.phase`:

| Condition                                                                             | Deployment Status |
| ------------------------------------------------------------------------------------- | ----------------- |
| Application is already synced to `targetRevision`                                     | `Succeeded`       |
| `operationState.phase` is `Running`                                                   | `InProgress`      |
| `operationState.phase` is `Error` or `Failed`                                         | `Failed`          |
| `operationState.phase` is `Succeeded` and `spec.source.targetRevision` matches        | `Pending`         |
| `operationState.phase` is `Succeeded` and `spec.source.targetRevision` does not match | `Superseded`      |
| No active operation and `spec.source.targetRevision` matches                          | `Pending`         |
| No active operation and `spec.source.targetRevision` does not match                   | `Superseded`      |
