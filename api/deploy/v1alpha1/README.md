# Deploy CRDs

The deployment CRDs are a set of API definitions that are used to provide release,
release health analysis, and rollback management. All of the CRDs define a `target`
field that specifies the resource to which the CRD applies. Target is a GoCardless
specific field that is used to identify the resource to which the CRD applies.

- Release - records release information like a set of revisions

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

