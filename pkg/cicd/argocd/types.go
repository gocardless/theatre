package argocd

// ArgoCD sync status codes.
// See: https://github.com/argoproj/argo-cd/blob/master/pkg/apis/application/v1alpha1/types.go
const (
	SyncStatusSynced    = "Synced"
	SyncStatusOutOfSync = "OutOfSync"
)

// ArgoCD health status codes.
const (
	HealthStatusHealthy     = "Healthy"
	HealthStatusDegraded    = "Degraded"
	HealthStatusProgressing = "Progressing"
)

// ArgoCD operation phases.
const (
	OperationPhaseSucceeded = "Succeeded"
	OperationPhaseRunning   = "Running"
	OperationPhaseError     = "Error"
	OperationPhaseFailed    = "Failed"
)

// applicationResponse represents the relevant fields from the ArgoCD Application API response.
type applicationResponse struct {
	Spec   applicationSpecResponse   `json:"spec"`
	Status applicationStatusResponse `json:"status"`
}

type applicationSpecResponse struct {
	Project string `json:"project"`
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

type projectUpdate struct {
	Project projectResponse `json:"project"`
}

// projectResponse represents the full ArgoCD Project object returned by GET /api/v1/projects/{name}.
// It is also used as the body for PUT /api/v1/projects/{name}.
type projectResponse struct {
	Metadata projectMetadata `json:"metadata"`
	Spec     projectSpec     `json:"spec"`
}

type projectMetadata struct {
	Name            string            `json:"name"`
	ResourceVersion string            `json:"resourceVersion"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
}

type projectSpec struct {
	SourceRepos                []string             `json:"sourceRepos,omitempty"`
	Destinations               []projectDestination `json:"destinations,omitempty"`
	ClusterResourceWhitelist   []projectGroupKind   `json:"clusterResourceWhitelist,omitempty"`
	NamespaceResourceBlacklist []projectGroupKind   `json:"namespaceResourceBlacklist,omitempty"`
	Roles                      []projectRole        `json:"roles,omitempty"`
	SyncWindows                []SyncWindow         `json:"syncWindows,omitempty"`
}

type projectDestination struct {
	Server    string `json:"server,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
}

type projectGroupKind struct {
	Group string `json:"group"`
	Kind  string `json:"kind"`
}

type applicationProjectSpec struct {
	SyncWindows []SyncWindow `json:"syncWindows"`
}

type SyncWindow struct {
	Kind         string   `json:"kind"`
	Schedule     string   `json:"schedule"`
	Duration     string   `json:"duration"`
	Applications []string `json:"applications"`
	Namespaces   []string `json:"namespaces"`
	Clusters     []string `json:"clusters"`
	ManualSync   bool     `json:"manualSync"`
	TimeZone     string   `json:"timeZone"`
}

type projectRole struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Policies    []string `json:"policies"`
	Groups      []string `json:"groups"`
}
