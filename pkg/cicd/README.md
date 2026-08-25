# pkg/cicd

Core interfaces and types for CI/CD integrations in Theatre's rollback system.

## Overview

This package defines the `Deployer` interface that CI/CD providers implement to trigger and monitor deployments during rollback operations. It provides a provider-agnostic abstraction for deployment orchestration.

## Key Components

- **`Deployer` interface**: Core interface for CI/CD integrations with methods to trigger deployments and check their status
- **`DeploymentRequest`**: Request structure containing rollback metadata and provider-specific options
- **`DeploymentResult`**: Response structure with deployment ID, status, and URL
- **`DeploymentStatus`**: Enumeration of deployment states (Pending, InProgress, Succeeded, Failed, Unknown)
- **`DeployerError`**: Structured error type with retry semantics
- **`NoopDeployer`**: Test implementation that always succeeds
- **`ParseDeploymentOptions`**: Utility for parsing JSONPath expressions in deployment options

## Usage

Implement the `Deployer` interface to integrate a new CI/CD provider:

```go
type MyDeployer struct {}

func (d *MyDeployer) TriggerDeployment(ctx context.Context, req cicd.DeploymentRequest) (*cicd.DeploymentResult, error) {
    // Trigger deployment in your CI/CD system
}

func (d *MyDeployer) GetDeploymentStatus(ctx context.Context, deploymentID string) (*cicd.DeploymentResult, error) {
    // Poll deployment status
}

func (d *MyDeployer) Name() string {
    return "my-provider"
}
```

## Implementations

- [`github`](./github/README.md): GitHub Deployments API integration
