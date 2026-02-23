package deploy

const (
	// IndexFieldOwner indexes objects by their controller owner reference
	IndexFieldOwner = ".metadata.controller"

	// IndexFieldReleaseTarget indexes releases by their target name
	IndexFieldReleaseTarget = ".config.targetName"

	// IndexFieldReleaseActive indexes releases by their active condition status
	IndexFieldReleaseActive = "status.conditions.active"

	// IndexFieldRollbackTarget indexes rollbacks by their target name
	IndexFieldRollbackTarget = ".spec.toReleaseRef.target"
)
