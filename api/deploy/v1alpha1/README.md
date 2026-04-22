# Deploy CRDs

The deployment CRDs are a set of API definitions that are used to provide release,
release health analysis, and rollback management. All of the CRDs define a `target`
field that specifies the resource to which the CRD applies. Target is a GoCardless
specific field that is used to identify the resource to which the CRD applies.

- Release - records release information like a set of revisions
- Rollback - records rollback information, like from/to which release, deployment options
- AutomatedRollbackPolicy - controls automated rollback behavior (trigger, rollback deployment options)

## Release

**Short name:** `rel`

Records a deployment event for a given target. Each `Release` captures a set of
revisions (e.g. git commit SHAs, container image digests, Helm chart versions) that
were deployed together, and tracks the lifecycle of that deployment through status
conditions.

### Spec (`config`)

| Field        | Required | Description                                                                                                                         |
| ------------ | -------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `targetName` | Yes      | Namespace-unique identifier for the release target                                                                                  |
| `revisions`  | Yes      | List of revisions (1–10 items). Each revision has `name`, `id`, `source`, `type`, and optional `metadata` (author, branch, message) |

### Status

| Field                 | Description                                                  |
| --------------------- | ------------------------------------------------------------ |
| `conditions`          | Observed conditions: `Active`, `Healthy`, `RollbackRequired` |
| `message`             | Human-readable state description                             |
| `deploymentStartTime` | When the deployment started                                  |
| `deploymentEndTime`   | When the deployment completed                                |
| `previousRelease`     | Reference to the release that was superseded                 |
| `signature`           | Deterministic hash of the revision names and IDs             |

### Conditions

| Condition          | Status=True                         | Status=False                   |
| ------------------ | ----------------------------------- | ------------------------------ |
| `Active`           | Release is actively serving traffic | Release has been superseded    |
| `Healthy`          | Release passed health analysis      | Release failed health analysis |
| `RollbackRequired` | Release should be rolled back       | Release does not need rollback |

---

## Rollback

**Short name:** `rb`

Represents a historical record of a rollback operation. A `Rollback` resource is
created (manually or automatically) to roll a target back to a previously healthy
`Release`. The controller carries out the rollback via the CI/CD system and tracks
progress through status conditions.

### Spec

| Field                   | Required | Description                                                                                              |
| ----------------------- | -------- | -------------------------------------------------------------------------------------------------------- |
| `toReleaseRef.target`   | Yes      | Target name identifying which release target to roll back (immutable)                                    |
| `toReleaseRef.name`     | No       | Name of the specific `Release` to roll back to; if empty the controller picks the latest healthy release |
| `reason`                | Yes      | Human-readable explanation for why the rollback was initiated (1–512 chars)                              |
| `initiatedBy.principal` | No       | Identifier of the person or system that triggered the rollback                                           |
| `initiatedBy.type`      | No       | Type of initiator, e.g. `user` or `system`                                                               |
| `deploymentOptions`     | No       | Provider-specific options passed to the CI/CD system                                                     |

### Status

| Field            | Description                                                           |
| ---------------- | --------------------------------------------------------------------- |
| `conditions`     | Observed conditions: `InProgress`, `Succeeded`                        |
| `message`        | Human-readable state description                                      |
| `fromReleaseRef` | The release being rolled back from                                    |
| `automatic`      | Whether this rollback was triggered automatically                     |
| `startTime`      | When the rollback operation started                                   |
| `completionTime` | When the rollback operation completed                                 |
| `deploymentID`   | Unique identifier for the CI/CD deployment job                        |
| `deploymentURL`  | URL to the CI job performing the rollback                             |
| `attemptCount`   | Number of times the controller has attempted to initiate the rollback |

### Conditions

| Condition    | Status=True                                        | Status=False                              |
| ------------ | -------------------------------------------------- | ----------------------------------------- |
| `InProgress` | Rollback is in progress (e.g. ArgoCD sync running) | Rollback has not started or has completed |
| `Succeeded`  | Rollback completed successfully                    | Rollback has not yet succeeded            |

---

## AutomatedRollbackPolicy

**Short name:** `arbp`

Controls whether the operator should automatically create a `Rollback` resource when
a `Release` for a given target enters a trigger condition. The policy can be enabled
or disabled, and the controller will disable it automatically after performing one
automated rollback to prevent rollback loops.

### Spec

| Field                                     | Required | Description                                                                                        |
| ----------------------------------------- | -------- | -------------------------------------------------------------------------------------------------- |
| `targetName`                              | Yes      | Identifies which releases this policy applies to, matching `Release.config.targetName` (immutable) |
| `enabled`                                 | Yes      | Whether automated rollbacks are active (default: `false`)                                          |
| `trigger.conditionType`                   | No       | The `Release` condition type to watch (default: `RollbackRequired`)                                |
| `trigger.conditionStatus`                 | No       | The condition status value that triggers a rollback (`True` or `False`, default: `True`)           |
| `rollbackTemplate.metadata.labels`        | No       | Labels to apply to the created `Rollback` resource                                                 |
| `rollbackTemplate.metadata.annotations`   | No       | Annotations to apply to the created `Rollback` resource                                            |
| `rollbackTemplate.spec.deploymentOptions` | No       | Provider-specific options passed to the created `Rollback` spec                                    |

### Status

| Field                       | Description                                                     |
| --------------------------- | --------------------------------------------------------------- |
| `conditions`                | Observed conditions: `Automated`                                |
| `lastAutomatedRollbackTime` | Timestamp of the last automated rollback created by this policy |

### Conditions

| Condition   | Status=True                     | Status=False                     |
| ----------- | ------------------------------- | -------------------------------- |
| `Automated` | Automated rollbacks are enabled | Automated rollbacks are disabled |

Reason values: `SetByUser` (user explicitly configured it) or `DisabledByController`
(controller disabled it after performing an automated rollback).
