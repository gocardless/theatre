package deploy

const (
	// IndexFieldOwner indexes objects by their controller owner reference
	IndexFieldOwner = ".metadata.controller"

	// IndexFieldTargetName indexes releases by their target name
	IndexFieldTargetName = ".config.targetName"

	// IndexFieldActiveName indexes releases by their active condition status
	IndexFieldActiveName = "status.conditions.active"
)
