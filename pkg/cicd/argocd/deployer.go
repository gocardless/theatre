package argocd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/template"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/cicd"
)

const (
	TargetRevisionNameKey = "target_revision"
	AppRevisionNameKey    = "app_revision"
	AppNameKey            = "argocd_app_name"
	AddSyncWindowKey      = "argocd_add_sync_window"
)

// appNameTemplateData is the data available to the app name template.
type appNameTemplateData struct {
	Namespace string
	Target    string
}

// Deployer implements cicd.Deployer using ArgoCD's REST API.
// It triggers rollbacks by updating an application's target revision
// and REVISION parameter, then syncing the application.
type Deployer struct {
	httpClient      *http.Client
	serverURL       string
	authToken       string
	appNameTemplate *template.Template
	logger          logr.Logger
}

var _ cicd.Deployer = &Deployer{}

// NewDeployer creates a new ArgoCD deployer.
// appNameTmpl is a Go text/template string for deriving the ArgoCD application name,
// e.g. "{{.Namespace}}-{{.Target}}" or "compute-lab-{{.Namespace}}-{{.Target}}".
// The template receives Namespace and Target as fields.
// If the Rollback's deploymentOptions contain "argocd_app_name", that takes precedence.
func NewDeployer(httpClient *http.Client, serverURL, authToken, appNameTmpl string, logger logr.Logger) (*Deployer, error) {
	var tmpl *template.Template

	if appNameTmpl != "" {
		var err error
		if tmpl, err = template.New("appName").Parse(appNameTmpl); err != nil {
			return nil, fmt.Errorf("invalid app name template: %w", err)
		}
	}

	return &Deployer{
		httpClient:      httpClient,
		serverURL:       serverURL,
		authToken:       authToken,
		appNameTemplate: tmpl,
		logger:          logger.WithValues("deployer", "argocd"),
	}, nil
}

func (d *Deployer) Name() string {
	return "argocd"
}

func (d *Deployer) TriggerDeployment(ctx context.Context, req cicd.DeploymentRequest) (*cicd.DeploymentResult, error) {
	appName, err := d.resolveAppName(req)
	if err != nil {
		return nil, cicd.NewDeployerError(d.Name(), "TriggerDeployment", false, err)
	}

	infraRevision, ok := req.Options[TargetRevisionNameKey].(string)
	if !ok || infraRevision == "" {
		return nil, cicd.NewDeployerError(d.Name(), "TriggerDeployment", false,
			fmt.Errorf("%s is a required deploymentOption for the ArgoCD backend", TargetRevisionNameKey))
	}

	appRevision, ok := req.Options[AppRevisionNameKey].(string)
	if !ok {
		// App revision isn't required, so we can ignore if there is an error
		appRevision = ""
	}

	if err := d.updateApplication(ctx, appName, infraRevision, appRevision); err != nil {
		return nil, err
	}

	if err := d.syncApplication(ctx, appName, infraRevision); err != nil {
		return nil, err
	}

	appURL := fmt.Sprintf("%s/applications/%s", d.serverURL, appName)

	return &cicd.DeploymentResult{
		ID:      appName,
		URL:     appURL,
		Status:  cicd.DeploymentStatusPending,
		Message: "ArgoCD sync triggered",
	}, nil
}

func (d *Deployer) GetDeploymentStatus(ctx context.Context, deploymentID string) (*cicd.DeploymentResult, error) {
	app, err := d.getApplication(ctx, deploymentID)
	if err != nil {
		return nil, err
	}

	status, message := d.mapSyncStatus(*app)

	return &cicd.DeploymentResult{
		ID:      deploymentID,
		Status:  status,
		Message: message,
		URL:     fmt.Sprintf("%s/applications/%s", d.serverURL, deploymentID),
	}, nil
}

func (d *Deployer) PostDeploymentHooks(ctx context.Context, req cicd.DeploymentRequest, deploymentID string) error {
	appName, err := d.resolveAppName(req)
	if err != nil {
		return err
	}

	addSyncWindow, ok := req.Options[AddSyncWindowKey].(bool)
	if !ok || !addSyncWindow {
		d.logger.Info("Skipping sync window addition", "appName", appName)
	} else {
		d.logger.Info("Adding sync window", "appName", appName)
		app, err := d.getApplication(ctx, appName)
		if err != nil {
			return err
		}

		projectName := app.Spec.Project

		err = d.addSyncWindow(ctx, projectName)
		if err != nil {
			return err
		}
	}
	return nil
}

// resolveAppName determines the ArgoCD application name for the deployment.
// If "argocd_app_name" is set in the deployment options, it is used directly.
// Otherwise, the configured app name template is rendered with Namespace and Target.
func (d *Deployer) resolveAppName(req cicd.DeploymentRequest) (string, error) {
	if appName, ok := req.Options[AppNameKey].(string); ok && appName != "" {
		return appName, nil
	}

	namespace := req.Rollback.Namespace
	target := req.Rollback.Spec.ToReleaseRef.Target

	if namespace == "" {
		return "", fmt.Errorf("rollback namespace is empty")
	}
	if target == "" {
		return "", fmt.Errorf("rollback target is empty")
	}

	var buf strings.Builder
	if err := d.appNameTemplate.Execute(&buf, appNameTemplateData{
		Namespace: namespace,
		Target:    target,
	}); err != nil {
		return "", fmt.Errorf("failed to render app name template: %w", err)
	}

	return buf.String(), nil
}

// findRevision finds a revision by name in the release's revisions list.
// If revisionName is empty and there is exactly one revision, it returns that one.
func (d *Deployer) findRevision(revisions []deployv1alpha1.Revision, revisionName string) (*deployv1alpha1.Revision, error) {
	if revisionName != "" {
		for i := range revisions {
			if revisions[i].Name == revisionName {
				return &revisions[i], nil
			}
		}
		return nil, fmt.Errorf("no revision found with name %q", revisionName)
	}

	if len(revisions) == 0 {
		return nil, fmt.Errorf("no revisions found in target release")
	}

	if len(revisions) > 1 {
		names := make([]string, len(revisions))
		for i, r := range revisions {
			names[i] = r.Name
		}
		return nil, fmt.Errorf("multiple revisions found (%v); specify %q or %q to select one", names, TargetRevisionNameKey, AppRevisionNameKey)
	}

	return &revisions[0], nil
}

// updateApplication patches the ArgoCD application to set the target revision
// and the REVISION parameter.
func (d *Deployer) updateApplication(ctx context.Context, appName, infraRevision, appRevision string) error {
	patch := applicationPatch{
		Spec: applicationPatchSpec{
			Source: applicationPatchSource{
				TargetRevision: infraRevision,
			},
		},
	}

	if appRevision != "" {
		patch.Spec.Source.Plugin = applicationPatchPlugin{
			Env: []applicationPatchEnv{
				{Name: "REVISION", Value: appRevision},
			},
		}
	}

	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return cicd.NewDeployerError(d.Name(), "TriggerDeployment", false,
			fmt.Errorf("failed to marshal patch: %w", err))
	}

	reqBody := applicationPatchRequest{
		Name:      appName,
		PatchType: "merge",
		Patch:     string(patchJSON),
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return cicd.NewDeployerError(d.Name(), "TriggerDeployment", false,
			fmt.Errorf("failed to marshal patch request: %w", err))
	}

	path := fmt.Sprintf("/api/v1/applications/%s", url.PathEscape(appName))
	resp, err := d.doRequest(ctx, http.MethodPatch, path, body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return d.handleErrorResponse(resp, "TriggerDeployment", "update application")
	}

	return nil
}

// syncApplication triggers an ArgoCD sync operation for the application.
func (d *Deployer) syncApplication(ctx context.Context, appName, revision string) error {
	path := fmt.Sprintf("/api/v1/applications/%s/sync", url.PathEscape(appName))
	resp, err := d.doRequest(ctx, http.MethodPost, path, []byte{}, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return d.handleErrorResponse(resp, "TriggerDeployment", "sync application")
	}

	return nil
}

// getApplication fetches the current state of an ArgoCD application.
func (d *Deployer) getApplication(ctx context.Context, appName string) (*applicationResponse, error) {
	path := fmt.Sprintf("/api/v1/applications/%s", url.PathEscape(appName))
	resp, err := d.doRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, d.handleErrorResponse(resp, "GetDeploymentStatus", "get application")
	}

	var app applicationResponse
	if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
		return nil, cicd.NewDeployerError(d.Name(), "GetDeploymentStatus", false,
			fmt.Errorf("failed to decode application response: %w", err))
	}

	return &app, nil
}

func (d *Deployer) addSyncWindow(ctx context.Context, project string) error {
	path := fmt.Sprintf("/api/v1/projects/%s", url.PathEscape(project))

	getResp, err := d.doRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	defer getResp.Body.Close()

	if getResp.StatusCode >= 400 {
		return d.handleErrorResponse(getResp, "AddSyncWindow", "get project")
	}

	var proj projectResponse
	if err := json.NewDecoder(getResp.Body).Decode(&proj); err != nil {
		return fmt.Errorf("failed to decode project response: %w", err)
	}

	proj.Spec.SyncWindows = append(proj.Spec.SyncWindows, SyncWindow{
		Kind:         "deny",
		Schedule:     "* * * * *",
		Duration:     "1h",
		Applications: []string{"*"},
		ManualSync:   false,
	})

	projectUpdate := projectUpdate{
		Project: proj,
	}

	payloadBytes, err := json.Marshal(projectUpdate)
	if err != nil {
		return fmt.Errorf("failed to marshal project update: %w", err)
	}

	putResp, err := d.doRequest(ctx, http.MethodPut, path, payloadBytes, "application/json")
	if err != nil {
		return err
	}
	defer putResp.Body.Close()

	if putResp.StatusCode >= 400 {
		return d.handleErrorResponse(putResp, "AddSyncWindow", "update project")
	}

	return nil
}

// mapSyncStatus maps ArgoCD application status to a cicd.DeploymentStatus.
func (d *Deployer) mapSyncStatus(app applicationResponse) (cicd.DeploymentStatus, string) {
	// Check operation state first — it reflects the active sync operation
	if op := app.Status.OperationState; op != nil && op.Phase != "" {
		switch op.Phase {
		case OperationPhaseRunning:
			return cicd.DeploymentStatusInProgress, op.Message
		case OperationPhaseError, OperationPhaseFailed:
			return cicd.DeploymentStatusFailed, op.Message
		case OperationPhaseSucceeded:
			return cicd.DeploymentStatusSucceeded, op.Message
		default:
			return cicd.DeploymentStatusPending, fmt.Sprintf("Operation phase: %s", op.Phase)
		}
	}

	return cicd.DeploymentStatusPending, "No active operation"
}

// doRequest performs an HTTP request to the ArgoCD API with authentication.
func (d *Deployer) doRequest(ctx context.Context, method, path string, body []byte, contentType string) (*http.Response, error) {
	reqURL := d.serverURL + path

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, cicd.NewDeployerError(d.Name(), method, false,
			fmt.Errorf("failed to create request: %w", err))
	}

	req.Header.Set("Authorization", "Bearer "+d.authToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, cicd.NewDeployerError(d.Name(), method, true,
			fmt.Errorf("request failed: %w", err))
	}

	return resp, nil
}

// handleErrorResponse creates a DeployerError from an HTTP error response.
func (d *Deployer) handleErrorResponse(resp *http.Response, operation, action string) error {
	body, _ := io.ReadAll(resp.Body)
	retryable := resp.StatusCode >= 500 || resp.StatusCode == 429
	return cicd.NewDeployerError(d.Name(), operation, retryable,
		fmt.Errorf("failed to %s (HTTP %d): %s", action, resp.StatusCode, string(body)))
}
