package v1alpha1

const (
	AnnotationKeyBase = "theatre.gocardless.com"

	// AnnotationKeyReleaseActivate is an annotation that can be set on a Release
	// to set status.conditions.active` to `true`.
	AnnotationKeyReleaseActivate = AnnotationKeyBase + "/active"

	// AnnotationValueReleaseActivateTrue is the only valid value for
	// AnnotationKeyReleaseActivate.
	AnnotationValueReleaseActivateTrue = "true"

	// AnnotationKeyReleaseDeploymentStartTime is an annotation that can be set on a Release
	// to set `status.deploymentStartTime`.
	AnnotationKeyReleaseDeploymentStartTime = AnnotationKeyBase + "/deployment-start-time"

	// AnnotationKeyReleaseDeploymentEndTime is an annotation that can be set on a Release
	// to set `status.deploymentEndTime`.
	AnnotationKeyReleaseDeploymentEndTime = AnnotationKeyBase + "/deployment-end-time"

	// AnnotationKeyReleasePreviousRelease is an annotation that can be set on a Release
	// to set `status.previousRelease.releaseRef`.
	AnnotationKeyReleasePreviousRelease = AnnotationKeyBase + "/previous-release"
)
