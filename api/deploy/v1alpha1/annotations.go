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

	// AnnotationKeyReleaseAnalysisTemplateSelector is the name of the annotation
	// containing optional analysis template selectors
	AnnotationKeyReleaseAnalysisTemplateSelector = AnnotationKeyBase + "/analysis-selector"

	// AnnotationKeyReleaseNoGlobalAnalysis is the name of the annotation
	// to opt-out of using global analysis templates for the release
	AnnotationKeyReleaseNoGlobalAnalysis = AnnotationKeyBase + "/no-global-analysis"
	// AnnotationKeyMaxReleasesPerTarget is an annotation that can be set on a namespace
	// to limit the number of releases per target.
	AnnotationKeyReleaseLimit = AnnotationKeyBase + "/release-limit"

	// AnnotationKeyCullingStrategy is an annotation that can be set on a namespace
	// to specify the culling strategy to use.
	AnnotationKeyCullingStrategy = AnnotationKeyBase + "/culling-strategy"

	// There are two culling strategies:
	// 1. end-time: Cull releases based on the status.deploymentEndTime
	// 2. signature: Cull releases repeating status.signature first,
	// before deleting the oldest releases sorted by status.deploymentEndTime
	AnnotationValueCullingStrategyEndTime   = "end-time"
	AnnotationValueCullingStrategySignature = "signature"
)
