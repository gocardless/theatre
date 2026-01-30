package deploy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/deploy"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	AnalysisTimeBeforeDeployment             = time.Second * -5
	AnalysisArgNameBeforeDeploymentTimestamp = "pre-release-timestamp"
	AnalysisArgLabelPrefix                   = "label_"
)

// AnalysisReconcileJoinedError is an error that wraps multiple errors and can
// be fatal or non-fatal
type AnalysisReconcileJoinedError struct {
	message string
	fatal   bool
	inner   []error
}

func (e AnalysisReconcileJoinedError) Error() string {
	return fmt.Sprintf("%s: %v", e.message, e.inner)
}

func (e AnalysisReconcileJoinedError) Unwrap() []error {
	return e.inner
}

// newAnalysisReconcileJoinedError returns a new AnalysisReconcileJoinedError
// with the specified message and inner errors. If any of the inner errors
// are of the same type and fatal, the returned error is also fatal. Otherwise,
// the value provided in `fatal` is used.
func newAnalysisReconcileJoinedError(message string, fatal bool, innerErr ...error) AnalysisReconcileJoinedError {
	ret := AnalysisReconcileJoinedError{
		message: message,
		fatal:   fatal,
		inner:   innerErr,
	}
	if fatal {
		return ret
	}

	// if any of contained errors are fatal, consider the whole error fatal
	for _, e := range innerErr {
		var errToTest AnalysisReconcileJoinedError
		if errors.As(e, &errToTest) && errToTest.fatal {
			ret.fatal = errToTest.fatal
			return ret
		}
	}
	return ret
}

func (r *ReleaseReconciler) ReconcileAnalysis(ctx context.Context, logger logr.Logger, req ctrl.Request, release *deployv1alpha1.Release) error {
	if !r.AnalysisEnabled {
		logger.Info("analysis is disabled, skipping")
		return nil
	}

	releaseActive := meta.IsStatusConditionTrue(release.Status.Conditions, deployv1alpha1.ReleaseConditionActive)

	analysisResultKnown := release.IsAnalysisStatusKnown()

	// collect non-fatal errors to schedule next reconciliation
	var collectedErr []error

	if !releaseActive && analysisResultKnown {
		// if release is inactive and health/rollback status is already known, there
		// is nothing left to do and we can return immediately
		logger.Info("release is inactive with known analysis status, skipping")
		return nil
	}

	var childAnalysisRuns analysisv1alpha1.AnalysisRunList

	// List owned AnalysisRuns already existing
	err := r.List(ctx, &childAnalysisRuns, client.InNamespace(req.Namespace), client.MatchingFields{IndexFieldOwner: release.Name})
	if err != nil {
		logger.Error(err, "failed to list owned AnalysisRuns")
		return err
	}

	healthAnalysisRuns, rollbackAnalysisRuns := splitHealthRollback(childAnalysisRuns)

	healthResults := parseAnalysisResults(healthAnalysisRuns)
	rollbackResults := parseAnalysisResults(rollbackAnalysisRuns)

	analysisToCreate := []*analysisv1alpha1.AnalysisRun{}

	// We will create missing AnalysisRuns if the release is active. If it is
	// inactive, we will only finish parsing the result of existing AnalysisRuns.
	if !releaseActive {
		logger.Info("inactive release, skipping creation of new AnalysisRuns")
	} else {
		analysisToCreate, err = r.generateAnalysisRuns(ctx, logger, req, release, childAnalysisRuns.Items)
		if err != nil {
			var analysisErr AnalysisReconcileJoinedError
			if errors.As(err, &analysisErr) && analysisErr.fatal {
				logger.Error(err, "fatal error while attempting to determine AnalysisRuns to create, stopping")
				return analysisErr
			}
			logger.Error(err, "error while attempting to determine AnalysisRuns to create, continuing")
			collectedErr = append(collectedErr, err)
		}
	}

	if len(analysisToCreate) > 0 {
		logger.Info("found missing AnalysisRuns, creating")

		for _, v := range analysisToCreate {
			logger.Info("creating new AnalysisRun", "name", v.Name)
			err := r.Create(ctx, v)
			if err != nil {
				logger.Error(err, "failed to create AnalysisRun", "name", v.Name)
				collectedErr = append(collectedErr, err)
				continue
			}

			// We just created this, so it is counted as pending.
			if metav1.HasLabel(v.ObjectMeta, "health") && v.Labels["health"] == "true" {
				healthResults[analysisv1alpha1.AnalysisPhasePending] = append(healthResults[analysisv1alpha1.AnalysisPhasePending], v.Name)
			}
			if metav1.HasLabel(v.ObjectMeta, "rollback") && v.Labels["rollback"] == "true" {
				rollbackResults[analysisv1alpha1.AnalysisPhasePending] = append(rollbackResults[analysisv1alpha1.AnalysisPhasePending], v.Name)
			}
		}
	}

	healthCondition := healthConditionGen.conditionFromResults(healthResults)
	rollbackCondition := rollbackConditionGen.conditionFromResults(rollbackResults)

	meta.SetStatusCondition(&release.Status.Conditions, healthCondition)
	meta.SetStatusCondition(&release.Status.Conditions, rollbackCondition)

	// return non-fatal errors to schedule another reconciliation
	if len(collectedErr) > 0 {
		return newAnalysisReconcileJoinedError("errors encountered during analysis reconciliation", false, collectedErr...)
	}
	return nil
}

// generateAnalysisRuns returns the AnalysisRuns that should be created for the
// given release. This is determined by collecting all AnalysisTemplates and
// ClusterAnalysisTemplates that match the release's selectors.
// Any generated AnalysisRuns that already exist in `existingRuns` are skipped.
func (r *ReleaseReconciler) generateAnalysisRuns(
	ctx context.Context,
	logger logr.Logger,
	req ctrl.Request,
	release *deployv1alpha1.Release,
	existingRuns []analysisv1alpha1.AnalysisRun,
) ([]*analysisv1alpha1.AnalysisRun, error) {
	ret := []*analysisv1alpha1.AnalysisRun{}

	namespacedSelectors, clusterSelectors := generateSelectors(release, logger)
	allAnalysisTemplateLists := []runtime.Object{}

	// collect non-fatal errors
	var collectedErr []error

	for _, v := range namespacedSelectors {
		var templateList analysisv1alpha1.AnalysisTemplateList
		err := r.List(ctx, &templateList, client.InNamespace(req.Namespace), client.MatchingLabelsSelector{Selector: v})
		if err != nil {
			logger.Error(err, "failed to list AnalysisTemplates", "selector", v.String())
			// TODO: we continue because we might still succeed listing other attempts
			// but we might want to note that we hed an error, and schedule reconciliation
			collectedErr = append(
				collectedErr,
				fmt.Errorf("failed to list AnalysisTemplates with selector '%s': %w", v.String(), err),
			)
			continue
		}

		allAnalysisTemplateLists = append(allAnalysisTemplateLists, &templateList)
	}

	for _, v := range clusterSelectors {
		var templateList analysisv1alpha1.ClusterAnalysisTemplateList
		err := r.List(ctx, &templateList, client.MatchingLabelsSelector{Selector: v})
		if err != nil {
			logger.Error(err, "failed to list ClusterAnalysisTemplates", "selector", v.String())
			collectedErr = append(
				collectedErr,
				fmt.Errorf("failed to list ClusterAnalysisTemplates with selector '%s': %w", v.String(), err),
			)
			continue
		}

		allAnalysisTemplateLists = append(allAnalysisTemplateLists, &templateList)
	}

	// collect all input templates in a generic list, so we can pass it all to a single function
	// NOTE: we use runtime.Object here to encompass both AnalysisTemplate and
	// ClusterAnalysisTemplate, but will convert to the correct type in analysisCreate
	allTemplates, err := concatTemplateLists(allAnalysisTemplateLists)
	if err != nil {
		logger.Error(err, "failed to collect all templates for analysis generation")
		return nil, newAnalysisReconcileJoinedError(
			"failed to collect all templates for analysis generation",
			true,
			append(collectedErr, err)...,
		)
	}

	for _, v := range allTemplates {
		analysis, err := createAnalysisRun(release, v)
		if err != nil {
			logger.Error(err, "failed to create AnalysisRun")
			collectedErr = append(
				collectedErr,
				fmt.Errorf("failed to create AnalysisRun: %w", err),
			)
			continue
		}
		controllerutil.SetControllerReference(release, analysis, r.Scheme)

		if !slices.ContainsFunc(existingRuns, func(run analysisv1alpha1.AnalysisRun) bool { return run.Name == analysis.Name }) {
			ret = append(ret, analysis)
		}
	}

	if len(collectedErr) > 0 {
		return ret, newAnalysisReconcileJoinedError("errors while generating AnalysisRuns", false, collectedErr...)
	}
	return ret, nil
}

// splitHealthRollback splits AnalysisRuns into separate lists of AnalysysRuns
// contributing to health or rollback status, based on their labels. The same
// resource can be included in both lists.
func splitHealthRollback(analysisList analysisv1alpha1.AnalysisRunList) ([]analysisv1alpha1.AnalysisRun, []analysisv1alpha1.AnalysisRun) {

	health := []analysisv1alpha1.AnalysisRun{}
	rollback := []analysisv1alpha1.AnalysisRun{}

	for _, v := range analysisList.Items {
		if metav1.HasLabel(v.ObjectMeta, "health") && v.Labels["health"] == "true" {
			health = append(health, v)
		}
		if metav1.HasLabel(v.ObjectMeta, "rollback") && v.Labels["rollback"] == "true" {
			rollback = append(rollback, v)
		}
	}
	return health, rollback
}

// parseAnalysisResults takes an AnalysisRunList and returns a map of
// AnalysisPhase to a list of AnalysisRun names in each phase.
// This makes it easier to count how many AnalysisRuns are in each phase.
func parseAnalysisResults(analysisList []analysisv1alpha1.AnalysisRun) map[analysisv1alpha1.AnalysisPhase][]string {
	out := map[analysisv1alpha1.AnalysisPhase][]string{
		analysisv1alpha1.AnalysisPhasePending:      {},
		analysisv1alpha1.AnalysisPhaseRunning:      {},
		analysisv1alpha1.AnalysisPhaseSuccessful:   {},
		analysisv1alpha1.AnalysisPhaseFailed:       {},
		analysisv1alpha1.AnalysisPhaseError:        {},
		analysisv1alpha1.AnalysisPhaseInconclusive: {},
	}

	for _, v := range analysisList {
		out[v.Status.Phase] = append(out[v.Status.Phase], v.Name)
	}

	return out
}

// concatTemplateLists takes a list of objects of type AnalysisTemplateList or
// ClusterAnalysisTemplateList and returns a list of runtime.Object containing
// all items from the lists
func concatTemplateLists(list []runtime.Object) ([]runtime.Object, error) {

	// count elements in each list
	total := 0
	counter := 0
	for _, v := range list {
		nsList, okNs := v.(*analysisv1alpha1.AnalysisTemplateList)
		clusterList, okCluster := v.(*analysisv1alpha1.ClusterAnalysisTemplateList)

		if okNs {
			total += len(nsList.Items)
		} else if okCluster {
			total += len(clusterList.Items)
		} else {
			return nil, errors.New("object is not an AnalysisTemplateList or ClusterAnalysisTemplateList")
		}
	}

	ret := make([]runtime.Object, total)

	for _, v := range list {
		nsList, okNs := v.(*analysisv1alpha1.AnalysisTemplateList)
		clusterList, okCluster := v.(*analysisv1alpha1.ClusterAnalysisTemplateList)

		if okNs {
			for _, v := range nsList.Items {
				ret[counter] = &v
				counter++
			}
		} else if okCluster {
			for _, v := range clusterList.Items {
				ret[counter] = &v
				counter++
			}
		} else {
			return nil, errors.New("object is not an AnalysisTemplateList or ClusterAnalysisTemplateList")
		}
	}
	return ret, nil
}

// createAnalysisRun generates an AnalysisRun for the given release, from the
// provided template. Template must be an AnalysisTemplate or ClusterAnalysisTemplate
func createAnalysisRun(release *deployv1alpha1.Release, template runtime.Object) (*analysisv1alpha1.AnalysisRun, error) {
	templateNamespaced, okNamespaced := template.(*analysisv1alpha1.AnalysisTemplate)
	templateCluster, okCluster := template.(*analysisv1alpha1.ClusterAnalysisTemplate)

	var (
		templateName string
		templateSpec analysisv1alpha1.AnalysisTemplateSpec
		// templateLabels map[string]string
	)

	if okNamespaced {
		templateName = templateNamespaced.Name
		templateSpec = *templateNamespaced.Spec.DeepCopy()
		// templateLabels = templateNamespaced.GetLabels()
	} else if okCluster {
		templateName = templateCluster.Name
		templateSpec = *templateCluster.Spec.DeepCopy()
		// templateLabels = templateCluster.GetLabels()
	} else {
		return nil, errors.New("object is not an AnalysisTemplate or ClusterAnalysisTemplate")
	}

	var args []analysisv1alpha1.Argument

	for _, v := range templateSpec.Args {
		ret := v

		// special value to insert timestamp evaluation
		if ret.Name == AnalysisArgNameBeforeDeploymentTimestamp {
			unix := release.Status.DeploymentStartTime.Time.Add(AnalysisTimeBeforeDeployment).Unix()
			unixStr := strconv.FormatInt(unix, 10)
			ret.Value = &unixStr
			ret.ValueFrom = nil
		} else if labelName, found := strings.CutPrefix(ret.Name, AnalysisArgLabelPrefix); found {
			// we replace prefixed args with release labels
			if metav1.HasLabel(release.ObjectMeta, labelName) {
				labelStr := release.Labels[labelName]
				ret.Value = &labelStr
			}
		}

		if ret.Value == nil && ret.ValueFrom == nil {
			return nil, fmt.Errorf("could not determine value for arg %s and no default value set", ret.Name)
		}

		args = append(args, ret)
	}

	run := &analysisv1alpha1.AnalysisRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploy.GenerateAnalysisRunName(release.Name, templateName),
			Namespace: release.Namespace,
			// TODO: should we merge release labels with template labels?
			Labels: release.GetLabels(),
		},
		Spec: analysisv1alpha1.AnalysisRunSpec{
			Args:    args,
			Metrics: templateSpec.Metrics,
		},
	}

	return run, nil
}

// generateSelectors generates the release selectors for AnalysisRun and ClusterAnalysisRun
func generateSelectors(release *deployv1alpha1.Release, logger logr.Logger) ([]labels.Selector, []labels.Selector) {
	useGlobal := true

	var customTemplateSelector labels.Selector
	if metav1.HasAnnotation(release.ObjectMeta, deployv1alpha1.AnnotationKeyReleaseAnalysisTemplateSelector) {
		templateSelectorStr := release.GetAnnotations()[deployv1alpha1.AnnotationKeyReleaseAnalysisTemplateSelector]
		parsedTemplateSelector, err := labels.Parse(templateSelectorStr)
		if err != nil {
			logger.Error(err, "failed to parse custom template selector, proceeding without", "selector", templateSelectorStr)
			customTemplateSelector = nil
		} else {
			customTemplateSelector = parsedTemplateSelector
		}
	}

	if metav1.HasAnnotation(release.ObjectMeta, deployv1alpha1.AnnotationKeyReleaseNoGlobalAnalysis) && release.GetAnnotations()[deployv1alpha1.AnnotationKeyReleaseNoGlobalAnalysis] == "true" {
		useGlobal = false
	}

	releaseLabelsSelector := labels.SelectorFromSet(release.GetLabels())
	globalSelector := labels.SelectorFromValidatedSet(labels.Set{"global": "true"})

	namespacedSelectors := []labels.Selector{releaseLabelsSelector}
	clusterSelectors := []labels.Selector{}

	if customTemplateSelector != nil {
		namespacedSelectors = append(namespacedSelectors, customTemplateSelector)
		clusterSelectors = append(clusterSelectors, customTemplateSelector)
	}

	if useGlobal {
		clusterSelectors = append(clusterSelectors, globalSelector)
	}

	return namespacedSelectors, clusterSelectors
}

// conditionGen is a helper struct to generate metav1.Condition
// we use custom struct to determine health/rollback status, because they have
// opposite status based on analysis result
type conditionGen struct {
	conditionType       string
	conditionStatusBad  metav1.ConditionStatus
	conditionStatusGood metav1.ConditionStatus
}

var healthConditionGen = conditionGen{
	conditionType:       deployv1alpha1.ReleaseConditionHealthy,
	conditionStatusGood: metav1.ConditionTrue,
	conditionStatusBad:  metav1.ConditionFalse,
}

var rollbackConditionGen = conditionGen{
	conditionType:       deployv1alpha1.ReleaseConditionRollbackRequired,
	conditionStatusGood: metav1.ConditionFalse,
	conditionStatusBad:  metav1.ConditionTrue,
}

// conditionFromResults takes a map of analysis results returned by
// parseAnalysisResults, and returns a metav1.Condition for the Release object.
// Condition reason is determined by priority list:
// 1. Any result failed: Healthy==False, otherwise
// 2. Any result [error|inconclusive|pending|running]: Healthy==Unknown, otherwise
// 3. Healthy==True (all results finished and successful)
func (c conditionGen) conditionFromResults(results map[analysisv1alpha1.AnalysisPhase][]string) metav1.Condition {
	ret := metav1.Condition{
		Type: c.conditionType,
	}

	numTotal := 0

	for _, v := range results {
		numTotal += len(v)
	}

	if numTotal == 0 {
		ret.Status = metav1.ConditionUnknown
		ret.Reason = deployv1alpha1.ReasonAnalysisMissing
		ret.Message = "No AnalysisRun(s) found"
		return ret
	}

	if len(results[analysisv1alpha1.AnalysisPhaseFailed]) > 0 {
		ret.Status = c.conditionStatusBad
		ret.Reason = deployv1alpha1.ReasonAnalysisFailed
		if len(results[analysisv1alpha1.AnalysisPhaseFailed]) == 1 {
			ret.Message = fmt.Sprintf("AnalysisRun \"%s\" failed", results[analysisv1alpha1.AnalysisPhaseFailed][0])
		} else {
			ret.Message = fmt.Sprintf("%d out of %d AnalysisRun(s) failed", len(results[analysisv1alpha1.AnalysisPhaseFailed]), numTotal)
		}
		return ret
	}

	numPendingOrRunning := len(results[analysisv1alpha1.AnalysisPhasePending]) + len(results[analysisv1alpha1.AnalysisPhaseRunning])
	numErrored := len(results[analysisv1alpha1.AnalysisPhaseError])
	numInconclusive := len(results[analysisv1alpha1.AnalysisPhaseInconclusive])
	numUnknowns := numPendingOrRunning + numErrored + numInconclusive

	if numUnknowns > 0 {
		ret.Status = metav1.ConditionUnknown

		if numErrored > 0 {
			ret.Reason = deployv1alpha1.ReasonAnalysisError
			if numErrored == 1 {
				ret.Message = fmt.Sprintf("AnalysisRun \"%s\" errored", results[analysisv1alpha1.AnalysisPhaseError][0])
			} else {
				ret.Message = fmt.Sprintf("%d out of %d AnalysisRuns errored", numErrored, numTotal)
			}
			return ret
		}

		if numInconclusive > 0 {
			ret.Reason = deployv1alpha1.ReasonAnalysisError
			if numInconclusive == 1 {
				ret.Message = fmt.Sprintf("AnalysisRun \"%s\" result is inconclusive", results[analysisv1alpha1.AnalysisPhaseInconclusive][0])
			} else {
				ret.Message = fmt.Sprintf("%d out of %d AnalysisRuns have inconclusive results", numInconclusive, numTotal)
			}
			return ret
		}

		if numPendingOrRunning > 0 {
			ret.Reason = deployv1alpha1.ReasonAnalysisInProgress
			if numPendingOrRunning == 1 {
				ret.Message = fmt.Sprintf("Awaiting results from AnalysisRun \"%s\"", results[analysisv1alpha1.AnalysisPhasePending][0])
			} else {
				ret.Message = fmt.Sprintf("Awaiting results from %d out of %d AnalysisRuns", numPendingOrRunning, numTotal)
			}
			return ret
		}
	}

	// all other options skipped, we can infer that release is healthy
	ret.Status = c.conditionStatusGood
	ret.Reason = deployv1alpha1.ReasonAnalysisSucceeded
	ret.Message = fmt.Sprintf("All %d AnalysisRuns succeeded", len(results[analysisv1alpha1.AnalysisPhaseSuccessful]))
	return ret
}
