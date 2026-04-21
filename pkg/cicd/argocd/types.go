package argocd

// ArgoCD sync status codes.
// See: https://github.com/argoproj/argo-cd/blob/master/pkg/apis/application/v1alpha1/types.go
const (
	SyncStatusSynced    = "Synced"
	SyncStatusOutOfSync = "OutOfSync"
)

// ArgoCD health status codes.
// See: https://github.com/argoproj/gitops-engine/blob/master/pkg/health/health.go
const (
	HealthStatusHealthy     = "Healthy"
	HealthStatusDegraded    = "Degraded"
	HealthStatusProgressing = "Progressing"
)

// ArgoCD operation phases.
// See: https://github.com/argoproj/gitops-engine/blob/master/pkg/sync/common/types.go
const (
	OperationPhaseSucceeded = "Succeeded"
	OperationPhaseRunning   = "Running"
	OperationPhaseError     = "Error"
	OperationPhaseFailed    = "Failed"
)

// applicationResponse represents the relevant fields from the ArgoCD Application API response.
type applicationResponse struct {
	Status applicationStatusResponse `json:"status"`
}

type applicationStatusResponse struct {
	Sync           applicationSyncStatus      `json:"sync"`
	Health         applicationHealthStatus    `json:"health"`
	OperationState *applicationOperationState `json:"operationState,omitempty"`
}

type applicationSyncStatus struct {
	Status string `json:"status"`
}

type applicationHealthStatus struct {
	Status string `json:"status"`
}

type applicationOperationState struct {
	Phase   string `json:"phase"`
	Message string `json:"message"`
}

// applicationPatchRequest is the body sent to the ArgoCD PATCH endpoint.
// The "patch" field is a JSON string containing the actual patch content.
// See: https://github.com/argoproj/argo-cd/blob/master/server/application/application.proto
type applicationPatchRequest struct {
	Name      string `json:"name"`
	PatchType string `json:"patchType"`
	Patch     string `json:"patch"`
}

// applicationPatch represents the merge-patch content (serialized to a string in the request).
type applicationPatch struct {
	Spec applicationPatchSpec `json:"spec"`
}

type applicationPatchSpec struct {
	Source applicationPatchSource `json:"source"`
}

type applicationPatchSource struct {
	TargetRevision string                 `json:"targetRevision"`
	Plugin         applicationPatchPlugin `json:"plugin"`
}

type applicationPatchPlugin struct {
	// This will replace the whole env array, fine for now, but might be
	// an issue if there are multiple parameters in the future.
	Env []applicationPatchEnv `json:"env"`
}

type applicationPatchEnv struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
