package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/cicd"
	"github.com/google/go-github/v34/github"
)

// Deployer implements cicd.Deployer using the GitHub Deployments API.
// This creates GitHub deployment events that can be consumed by any CICD
// system that watches for them.
//
// The deployer extracts owner/repo from the target release's revision with
// Type="github" and Source in "owner/repo" format. Environment is optionally
// taken from Options["environment"].
type Deployer struct {
	client *github.Client
	logger logr.Logger
}

// Ensure Deployer implements the interface.
var _ cicd.Deployer = &Deployer{}

// NewDeployer creates a new GitHub Deployments deployer.
func NewDeployer(client *github.Client, logger logr.Logger) *Deployer {
	return &Deployer{
		client: client,
		logger: logger.WithValues("deployer", "github"),
	}
}

func (d *Deployer) Name() string {
	return "github"
}

// TriggerDeployment creates a GitHub deployment event.
func (d *Deployer) TriggerDeployment(ctx context.Context, req cicd.DeploymentRequest) (*cicd.DeploymentResult, error) {
	// Extract owner/repo from the github revision in the target release
	applicationRevision, err := d.findGitHubRevision(req.ToRelease.ReleaseConfig.Revisions, req.Options["application_repository"])
	if err != nil {
		return nil, cicd.NewDeployerError(d.Name(), "TriggerDeployment", false, err)
	}

	owner, repo, err := d.parseOwnerRepo(applicationRevision.Source)
	if err != nil {
		return nil, cicd.NewDeployerError(d.Name(), "TriggerDeployment", false, err)
	}

	applicationRevisionId := applicationRevision.ID
	if applicationRevisionId == "" {
		return nil, cicd.NewDeployerError(d.Name(), "TriggerDeployment", false,
			fmt.Errorf("github revision has no ID"))
	}

	// The error here is intentionally not handled, as we might decide that
	// we don't need to fail the deployment if the infrastructure revision is not found.
	infrastructureRevision, _ := d.findGitHubRevision(req.ToRelease.ReleaseConfig.Revisions, req.Options["infrastructure_repository"])

	// Build the deployment payload with rollback metadata and user options
	payload := d.buildPayload(req, infrastructureRevision.ID)

	description := fmt.Sprintf("Rollback to %s: %s", req.Rollback.Spec.ToReleaseRef, req.Rollback.Spec.Reason)
	if len(description) > 140 {
		description = description[:137] + "..."
	}

	deploymentReq := &github.DeploymentRequest{
		Ref:              github.String(applicationRevisionId),
		Description:      github.String(description),
		AutoMerge:        github.Bool(false),
		RequiredContexts: &[]string{}, // Bypass status checks for rollbacks
		Payload:          payload,
	}

	// Set environment from Options if provided
	if env, ok := req.Options["environment"]; ok && env != "" {
		deploymentReq.Environment = github.String(env)
	}

	d.logger.Info("creating GitHub deployment",
		"owner", owner,
		"repo", repo,
		"ref", applicationRevisionId,
		"infrastructure_revision", infrastructureRevision.ID,
		"rollback", req.Rollback.Name,
	)

	deployment, resp, err := d.client.Repositories.CreateDeployment(
		ctx,
		owner,
		repo,
		deploymentReq,
	)
	if err != nil {
		// Check if this is a retryable error (rate limiting, server errors)
		retryable := resp != nil && (resp.StatusCode >= 500 || resp.StatusCode == 429)
		return nil, cicd.NewDeployerError(d.Name(), "TriggerDeployment", retryable,
			fmt.Errorf("failed to create deployment: %w", err))
	}

	deploymentURL := fmt.Sprintf("https://github.com/%s/%s/deployments/%d",
		owner, repo, deployment.GetID())

	return &cicd.DeploymentResult{
		// Here we use the deployment URL as the ID since the deployments API additionally
		// requires the owner and repo when querying status, which are encoded in the URL.
		ID:      deploymentURL,
		URL:     deploymentURL,
		Status:  cicd.DeploymentStatusPending,
		Message: "Deployment created",
	}, nil
}

// GetDeploymentStatus retrieves the current status of a GitHub deployment.
// The deploymentID should be the URL returned from TriggerDeployment
// (e.g., "https://github.com/owner/repo/deployments/123").
func (d *Deployer) GetDeploymentStatus(ctx context.Context, deploymentID string) (*cicd.DeploymentResult, error) {
	// Parse owner, repo, and deployment ID from the URL
	owner, repo, id, err := d.parseDeploymentURL(deploymentID)
	if err != nil {
		return nil, cicd.NewDeployerError(d.Name(), "GetDeploymentStatus", false, err)
	}

	statuses, resp, err := d.client.Repositories.ListDeploymentStatuses(
		ctx,
		owner,
		repo,
		id,
		&github.ListOptions{PerPage: 1}, // Only need the latest status
	)
	if err != nil {
		retryable := resp != nil && (resp.StatusCode >= 500 || resp.StatusCode == 429)
		return nil, cicd.NewDeployerError(d.Name(), "GetDeploymentStatus", retryable,
			fmt.Errorf("failed to get deployment statuses: %w", err))
	}

	if len(statuses) == 0 {
		return &cicd.DeploymentResult{
			ID:      deploymentID,
			Status:  cicd.DeploymentStatusPending,
			Message: "No status updates yet",
		}, nil
	}

	latestStatus := statuses[0]
	status, message := d.mapGitHubStatus(latestStatus)

	return &cicd.DeploymentResult{
		ID:      deploymentID,
		Status:  status,
		Message: message,
		URL:     latestStatus.GetTargetURL(),
	}, nil
}

// buildPayload constructs the deployment payload from rollback metadata
// and user-provided options.
func (d *Deployer) buildPayload(req cicd.DeploymentRequest, infrastructureRevisionId string) map[string]interface{} {
	creator := req.Rollback.Spec.InitiatedBy.System
	if req.Rollback.Spec.InitiatedBy.User != "" {
		creator = req.Rollback.Spec.InitiatedBy.User
	}

	payload := map[string]interface{}{
		// Standard rollback fields
		"target":        req.ToRelease.ReleaseConfig.TargetName,
		"creator":       creator,
		"is_rollback":   true,
		"rollback_from": req.Rollback.Status.FromReleaseRef,
		"rollback_to":   req.Rollback.Spec.ToReleaseRef,
		"reason":        req.Rollback.Spec.Reason,
		"version":       3,
	}

	if infrastructureRevisionId != "" {
		payload["target_revision"] = infrastructureRevisionId
	}

	// Merge user-provided options
	for key, value := range req.Options {
		payload[key] = value
	}

	payload["skip_queue"] = true
	return payload
}

// findGitHubRevision finds the revision with Type="github".
// If repository is specified, it matches the revision with that Source.
// If no repository is specified and multiple github revisions exist, it returns an error.
func (d *Deployer) findGitHubRevision(revisions []deployv1alpha1.Revision, repository string) (*deployv1alpha1.Revision, error) {
	// If repository option is specified, find the matching revision
	if repository != "" {
		for i := range revisions {
			if revisions[i].Type == "github" && revisions[i].Source == repository {
				return &revisions[i], nil
			}
		}
		return nil, fmt.Errorf("no github revision found with source %q", repository)
	}

	// No optionsKey specified - find all github revisions
	var ghRevisions []*deployv1alpha1.Revision
	for i := range revisions {
		if revisions[i].Type == "github" {
			ghRevisions = append(ghRevisions, &revisions[i])
		}
	}

	if len(ghRevisions) == 0 {
		return nil, fmt.Errorf("no revision with type 'github' found in target release")
	}

	if len(ghRevisions) > 1 {
		var sources []string
		for _, r := range ghRevisions {
			sources = append(sources, r.Source)
		}
		return nil, fmt.Errorf("multiple github revisions found (%v); specify a 'repository' to select one", sources)
	}

	return ghRevisions[0], nil
}

// parseOwnerRepo parses "owner/repo" format into separate components.
func (d *Deployer) parseOwnerRepo(source string) (owner, repo string, err error) {
	parts := strings.Split(source, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid github repository format %q, expected 'owner/repo'", source)
	}
	return parts[0], parts[1], nil
}

// parseDeploymentURL parses a GitHub deployment URL into owner, repo, and deployment ID.
// Expected format: "https://github.com/owner/repo/deployments/123"
func (d *Deployer) parseDeploymentURL(url string) (owner, repo string, id int64, err error) {
	// Remove https://github.com/ prefix
	trimmed := strings.TrimPrefix(url, "https://github.com/")
	if trimmed == url {
		return "", "", 0, fmt.Errorf("invalid deployment URL format: %s", url)
	}

	// Expected: owner/repo/deployments/123
	parts := strings.Split(trimmed, "/")
	if len(parts) != 4 || parts[2] != "deployments" {
		return "", "", 0, fmt.Errorf("invalid deployment URL format: %s", url)
	}

	owner = parts[0]
	repo = parts[1]

	_, err = fmt.Sscanf(parts[3], "%d", &id)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid deployment ID in URL: %s", url)
	}

	return owner, repo, id, nil
}

// mapGitHubStatus converts GitHub deployment status to our generic status.
func (d *Deployer) mapGitHubStatus(status *github.DeploymentStatus) (cicd.DeploymentStatus, string) {
	state := status.GetState()
	description := status.GetDescription()

	switch state {
	case "success":
		return cicd.DeploymentStatusSucceeded, description
	case "failure", "error":
		return cicd.DeploymentStatusFailed, description
	case "pending", "queued":
		return cicd.DeploymentStatusPending, description
	case "in_progress":
		return cicd.DeploymentStatusInProgress, description
	default:
		return cicd.DeploymentStatusUnknown, fmt.Sprintf("unknown state: %s - %s", state, description)
	}
}
