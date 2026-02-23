package deploy

const (
	// Generic CRUD events
	EventCreated                = "Created"
	EventSuccessfulStatusUpdate = "SuccessfulStatusUpdate"
	EventSuccessfulUpdate       = "SuccessfulUpdate"
	EventNoStatusUpdate         = "NoStatusUpdate"

	// Culling events
	EventReleaseCulled = "ReleasedCulled"

	// Deployment events
	EventDeploymentTriggered = "DeploymentTriggered"
	EventDeploymentFailed    = "DeploymentFailed"
	EventDeploymentSucceeded = "DeploymentSucceeded"
)
