# pkg/cicd/github

GitHub Deployments API implementation of the Theatre CI/CD deployer interface.

## Overview

This package implements `cicd.Deployer` using the GitHub Deployments API. It creates GitHub deployment events that can be consumed by any CI/CD system watching for them (e.g., GitHub Actions, external deployment tools).

## How It Works

1. **Trigger**: Creates a GitHub deployment event on a specified repository/revision
2. **Status**: Polls GitHub deployment statuses to track progress
3. **Metadata**: Includes rollback context (target, creator, reason) in the deployment payload

## Configuration

The deployer requires:
- **`deployment_revision_name`** (required): Name of the revision in the release config where the deployment should be created
- **`environment`** (optional): GitHub environment name for the deployment

Additional options in `DeploymentRequest.Options` are merged into the deployment payload.

## Revision Requirements

The target release must contain a revision with:
- `Type: "github"`
- `Source: "owner/repo"` format
- `ID`: Git ref (commit SHA, branch, or tag)

## Deployment Payload

The GitHub deployment payload includes:

```json
{
  "version": 3,
  "target": "production",
  "creator": "user@example.com",
  "is_rollback": true,
  "rollback_from": "release-v2.0",
  "rollback_to": "release-v1.9",
  "reason": "Critical bug in v2.0",
  // ... additional user options
}
```

## Status Mapping

GitHub deployment states are mapped to Theatre deployment statuses:
- `success` → `Succeeded`
- `failure`, `error` → `Failed`
- `pending`, `queued` → `Pending`
- `in_progress` → `InProgress`
