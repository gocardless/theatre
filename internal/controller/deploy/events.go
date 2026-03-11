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
	EventDeploymentTriggered     = "DeploymentTriggered"
	EventDeploymentTriggerFailed = "DeploymentTriggerFailed"
	EventDeploymentFailed        = "DeploymentFailed"
	EventRollbackSucceeded       = "RollbackSucceeded"
	EventRollbackFailed          = "RollbackFailed"

	// Automated rollback events
	EventErrorGettingRollbackPolicy = "ErrorGettingRollbackPolicy"
	EventAutomatedRollbackTriggered = "AutomatedRollbackTriggered"
)
