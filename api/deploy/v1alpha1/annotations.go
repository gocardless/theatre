package v1alpha1

const (
	AnnotationKeyBase = "theatre.gocardless.com"

	// AnnotationKeyReleaseActivate is an annotation that can be set on a Release
	// to set status.conditions.active` to `true`.
	AnnotationKeyReleaseActivate = AnnotationKeyBase + "/release-active"

	// AnnotationValueReleaseActivateTrue is the only valid value for
	// AnnotationKeyReleaseActivate.
	AnnotationValueReleaseActivateTrue = "true"

	// AnnotationKeyReleaseDeploymentStartTime is an annotation that can be set on a Release
	// to set `status.deploymentStartTime`.
	AnnotationKeyReleaseDeploymentStartTime = AnnotationKeyBase + "/release-deployment-start-time"

	// AnnotationKeyReleaseDeploymentEndTime is an annotation that can be set on a Release
	// to set `status.deploymentEndTime`.
	AnnotationKeyReleaseDeploymentEndTime = AnnotationKeyBase + "/release-deployment-end-time"

	// AnnotationKeyReleasePreviousRelease is an annotation that can be set on a Release
	// to set `status.previousRelease.releaseRef`.
	AnnotationKeyReleasePreviousRelease = AnnotationKeyBase + "/release-previous-release"
)
