package cicd

import (
	"context"
	"fmt"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
)

// DeploymentRequest represents a request to trigger a deployment via a CICD system.
type DeploymentRequest struct {
	// Rollback is the Rollback resource being actioned
	Rollback *deployv1alpha1.Rollback

	// ToRelease is the Release being rolled back to
	ToRelease *deployv1alpha1.Release

	// Options contains provider-specific options to include in the deployment
	// payload
	Options map[string]string
}

// DeploymentStatus represents the status of a deployment in the CICD system.
type DeploymentStatus string

const (
	// DeploymentStatusPending indicates the deployment has been queued
	DeploymentStatusPending DeploymentStatus = "Pending"

	// DeploymentStatusInProgress indicates the deployment is running
	DeploymentStatusInProgress DeploymentStatus = "InProgress"

	// DeploymentStatusSucceeded indicates the deployment completed successfully
	DeploymentStatusSucceeded DeploymentStatus = "Succeeded"

	// DeploymentStatusFailed indicates the deployment failed
	DeploymentStatusFailed DeploymentStatus = "Failed"

	// DeploymentStatusUnknown indicates the status could not be determined
	DeploymentStatusUnknown DeploymentStatus = "Unknown"
)

// DeploymentResult represents the response from a CICD system, used both
// when triggering a deployment and when polling for status.
type DeploymentResult struct {
	// ID is a unique identifier for the deployment in the CICD system
	ID string

	// Status is the current status of the deployment
	Status DeploymentStatus

	// Message is a human-readable status message
	Message string

	// URL is a link to the deployment job/pipeline in the CICD system's UI
	URL string
}

// Deployer is the interface that CICD providers must implement to integrate
// with the rollback controller.
type Deployer interface {
	// TriggerDeployment initiates a deployment in the CICD system.
	// Returns a DeploymentResult containing the deployment ID and initial status.
	// The deployment ID can be used to check status via GetDeploymentStatus.
	TriggerDeployment(ctx context.Context, req DeploymentRequest) (*DeploymentResult, error)

	// GetDeploymentStatus retrieves the current status of a deployment.
	// The deploymentID should be the ID returned from TriggerDeployment.
	GetDeploymentStatus(ctx context.Context, deploymentID string) (*DeploymentResult, error)

	// Name returns a human-readable name for this deployer (e.g., "github").
	// Used for logging and metrics.
	Name() string
}

// DeployerError represents an error from a CICD deployer with additional context.
type DeployerError struct {
	// Deployer is the name of the deployer that produced the error
	Deployer string

	// Operation is the operation that failed (e.g., "TriggerDeployment", "GetDeploymentStatus")
	Operation string

	// Retryable indicates whether the operation can be retried
	Retryable bool

	// Err is the underlying error
	Err error
}

func (e *DeployerError) Error() string {
	return fmt.Sprintf("%s: %s failed: %v", e.Deployer, e.Operation, e.Err)
}

func (e *DeployerError) Unwrap() error {
	return e.Err
}

// NewDeployerError creates a new DeployerError.
func NewDeployerError(deployer, operation string, retryable bool, err error) *DeployerError {
	return &DeployerError{
		Deployer:  deployer,
		Operation: operation,
		Retryable: retryable,
		Err:       err,
	}
}

// NoopDeployer is a Deployer implementation that does nothing.
// Useful for testing or when no CICD integration is configured.
type NoopDeployer struct{}

var _ Deployer = &NoopDeployer{}

func (n *NoopDeployer) TriggerDeployment(ctx context.Context, req DeploymentRequest) (*DeploymentResult, error) {
	return &DeploymentResult{
		ID:      fmt.Sprintf("noop-%s-%s", req.Rollback.Namespace, req.Rollback.Name),
		Status:  DeploymentStatusSucceeded,
		Message: "noop deployer always succeeds",
	}, nil
}

func (n *NoopDeployer) GetDeploymentStatus(ctx context.Context, deploymentID string) (*DeploymentResult, error) {
	return &DeploymentResult{
		ID:      deploymentID,
		Status:  DeploymentStatusSucceeded,
		Message: "noop deployer always succeeds",
	}, nil
}

func (n *NoopDeployer) Name() string {
	return "noop"
}
