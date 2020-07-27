package v1alpha1

type ConsolePhase string

// These are valid phases for a console
const (
	// ConsolePendingAuthorisation means the console been created but it is not yet authorised to run
	ConsolePendingAuthorisation ConsolePhase = "Pending Authorisation"
	// ConsolePending means the console has been created but its pod is not yet ready
	ConsolePending ConsolePhase = "Pending"
	// ConsoleRunning means the pod has started and is running
	ConsoleRunning ConsolePhase = "Running"
	// ConsoleStopped means the console has completed or timed out
	ConsoleStopped ConsolePhase = "Stopped"
	// ConsoleDestroyed means the consoles job has been deleted
	ConsoleDestroyed ConsolePhase = "Destroyed"
)
