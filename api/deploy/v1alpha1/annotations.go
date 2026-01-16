package v1alpha1

const (
	AnnotationKeyBase = "theatre.gocardless.com"

	// AnnotationKeyReleaseSetDeploymentStartTime is an annotation that can be set on a Release
	// to trigger a deployment start time update.
	AnnotationKeyReleaseSetDeploymentStartTime = AnnotationKeyBase + "/release-set-deploy-start-time"

	// AnnotationKeyReleaseSetDeploymentEndTime is an annotation that can be set on a Release
	// to trigger a deployment end time update.
	AnnotationKeyReleaseSetDeploymentEndTime = AnnotationKeyBase + "/release-set-deploy-end-time"
)
