package deploy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/recutil"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const AnalysisTimeBeforeDeployment = time.Second * -5

var apiGVStr = deployv1alpha1.GroupVersion.String()

type ReleaseReconciler struct {
	client.Client
	Log                  logr.Logger
	Scheme               *runtime.Scheme
	MaxReleasesPerTarget int
}

func (r *ReleaseReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "Release")

	fieldIndexer := mgr.GetFieldIndexer()

	err := fieldIndexer.IndexField(
		ctx,
		&deployv1alpha1.Release{},
		"config.targetName",
		func(rawObj client.Object) []string {
			release := rawObj.(*deployv1alpha1.Release)
			return []string{release.ReleaseConfig.TargetName}
		},
	)

	if err != nil {
		return err
	}

	err = fieldIndexer.IndexField(
		ctx,
		&deployv1alpha1.Release{},
		"status.conditions.active",
		func(rawObj client.Object) []string {
			release := rawObj.(*deployv1alpha1.Release)
			condition := meta.FindStatusCondition(release.Status.Conditions, deployv1alpha1.ReleaseConditionActive)
			if condition == nil {
				return []string{}
			}
			return []string{string(condition.Status)}
		},
	)

	if err != nil {
		return err
	}

	// Index AnalysisRuns by their owner Release
	err = fieldIndexer.IndexField(
		ctx,
		&analysisv1alpha1.AnalysisRun{},
		".metadata.controller",
		func(rawObj client.Object) []string {
			run := rawObj.(*analysisv1alpha1.AnalysisRun)
			owner := metav1.GetControllerOf(run)
			if owner == nil {
				return nil
			}
			if owner.APIVersion != apiGVStr || owner.Kind != "Release" {
				return nil
			}
			return []string{owner.Name}
		},
	)

	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&deployv1alpha1.Release{}).
		Owns(&analysisv1alpha1.AnalysisRun{}).
		Complete(
			recutil.ResolveAndReconcile(
				ctx, logger, mgr, &deployv1alpha1.Release{},
				func(logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
					return r.Reconcile(ctx, logger, request, obj.(*deployv1alpha1.Release))
				},
			),
		)
}

// +kubebuilder:rbac:groups=argoproj.io,resources=analysisruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=analysistemplates;clusteranalysistemplates,verbs=get;list
func (r *ReleaseReconciler) Reconcile(ctx context.Context, logger logr.Logger, req ctrl.Request, release *deployv1alpha1.Release) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", req.Namespace, "release", release.Name)
	logger.Info("reconciling release")

	// Analysis begin

	// TODO: determine parameters from config

	// Should health analysis be reused for rollback condition
	// healthAsRollback := false

	releaseActive := meta.IsStatusConditionTrue(release.Status.Conditions, deployv1alpha1.ReleaseConditionActive)

	analysisResultKnown := statusKnown(release, []string{
		deployv1alpha1.ReleaseConditionHealthy,
		deployv1alpha1.ReleaseConditionRollbackRequired,
	})

	if !releaseActive && analysisResultKnown {
		// if release is inactive and health/rollback status is already known, there
		// is nothing left to do and we can return immediately
		logger.Info("release is inactive with known analysis status, skipping")
		return ctrl.Result{}, nil
	}

	var childAnalysisRuns analysisv1alpha1.AnalysisRunList

	// List owned AnalysisRuns already existing
	err := r.List(ctx, &childAnalysisRuns, client.InNamespace(req.Namespace), client.MatchingFields{".metadata.controller": release.Name})
	if err != nil {
		logger.Error(err, "failed to list owned AnalysisRuns")
		// TODO: update condition of Release?
		return ctrl.Result{}, err
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

		namespacedSelectors, clusterSelectors := generateSelectors(release)
		allAnalysisTemplateLists := []runtime.Object{}

		for _, v := range namespacedSelectors {
			var templateList analysisv1alpha1.AnalysisTemplateList
			err = r.List(ctx, &templateList, client.InNamespace(req.Namespace), client.MatchingLabelsSelector{Selector: v})
			if err != nil {
				logger.Error(err, "failed to list AnalysisTemplates")
				return ctrl.Result{}, err
			}

			allAnalysisTemplateLists = append(allAnalysisTemplateLists, &templateList)
		}

		for _, v := range clusterSelectors {
			var templateList analysisv1alpha1.ClusterAnalysisTemplateList
			err = r.List(ctx, &templateList, client.MatchingLabelsSelector{Selector: v})
			if err != nil {
				logger.Error(err, "failed to list ClusterAnalysisTemplates")
				return ctrl.Result{}, err
			}

			allAnalysisTemplateLists = append(allAnalysisTemplateLists, &templateList)
		}

		// collect all input templates in a generic list, so we can pass it all to a single function
		// NOTE: we use runtime.Object here to encompass both AnalysisTemplate and
		// ClusterAnalysisTemplate, but will convert to the correct type in analysisCreate
		allTemplates, err := concatTemplateLists(allAnalysisTemplateLists)
		if err != nil {
			logger.Error(err, "failed to generate AnalysisRun")
			// return?
		}

		for _, v := range allTemplates {
			analysis, err := r.analysisCreate(release, v)
			if err != nil {
				logger.Error(err, "failed to generate AnalysisRun")
				// return?
			}

			if !slices.ContainsFunc(childAnalysisRuns.Items, func(r analysisv1alpha1.AnalysisRun) bool { return r.Name == analysis.Name }) {
				analysisToCreate = append(analysisToCreate, analysis)
			}
		}
	}

	if len(analysisToCreate) > 0 {
		logger.Info("found missing AnalysisRuns, creating")

		for _, v := range analysisToCreate {
			logger.Info("creating new AnalysisRun", "name", v.Name)
			err := r.Create(ctx, v)
			if err != nil {
				logger.Error(err, "failed to create AnalysisRun", "name", v.Name)
				// return?
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

	healthConditionGen := conditionGen{
		conditionType:       deployv1alpha1.ReleaseConditionHealthy,
		conditionStatusGood: metav1.ConditionTrue,
		conditionStatusBad:  metav1.ConditionFalse,
	}

	rollbackConditionGen := conditionGen{
		conditionType:       deployv1alpha1.ReleaseConditionRollbackRequired,
		conditionStatusGood: metav1.ConditionFalse,
		conditionStatusBad:  metav1.ConditionTrue,
	}

	healthCondition := healthConditionGen.conditionFromResults(healthResults)
	rollbackCondition := rollbackConditionGen.conditionFromResults(rollbackResults)

	meta.SetStatusCondition(&release.Status.Conditions, healthCondition)
	meta.SetStatusCondition(&release.Status.Conditions, rollbackCondition)

	if statusErr := r.Status().Update(ctx, release); statusErr != nil {
		logger.Error(statusErr, "failed to update Release status")
	}

	return ctrl.Result{}, nil
}

// generateSelectors generates the release selectors for AnalysisRun and ClusterAnalysisRun
func generateSelectors(release *deployv1alpha1.Release) ([]labels.Selector, []labels.Selector) {

	useGlobal := true
	// TODO: implement reading custom selector
	var customTemplateSelector labels.Selector
	if metav1.HasAnnotation(release.ObjectMeta, "placeholder-selector") {
		parsedTemplateSelector, err := labels.Parse(release.GetAnnotations()["placeholder-selector"])
		if err != nil {
			// log
			customTemplateSelector = nil
		} else {
			customTemplateSelector = parsedTemplateSelector
		}
	}

	if metav1.HasAnnotation(release.ObjectMeta, "placeholder-no-global") && release.GetAnnotations()["placeholder-no-global"] == "true" {
		useGlobal = false
	}

	releaseLabelsSelector := labels.SelectorFromSet(release.GetLabels())
	globalSelector := labels.SelectorFromValidatedSet(labels.Set{"global": "true"})

	namespacedSelectors := []labels.Selector{releaseLabelsSelector}
	var clusterSelectors []labels.Selector

	if customTemplateSelector != nil {
		namespacedSelectors = append(namespacedSelectors, customTemplateSelector)
		clusterSelectors = append(clusterSelectors, customTemplateSelector)
	}

	if useGlobal {
		clusterSelectors = append(clusterSelectors, globalSelector)
	}

	return namespacedSelectors, clusterSelectors
}

func (r *ReleaseReconciler) analysisCreate(release *deployv1alpha1.Release, obj runtime.Object) (*analysisv1alpha1.AnalysisRun, error) {
	templateNamespaced, okNamespaced := obj.(*analysisv1alpha1.AnalysisTemplate)
	templateCluster, okCluster := obj.(*analysisv1alpha1.ClusterAnalysisTemplate)

	var (
		templateName   string
		templateSpec   analysisv1alpha1.AnalysisTemplateSpec
		templateLabels map[string]string
	)

	if okNamespaced {
		templateName = templateNamespaced.Name
		templateSpec = *templateNamespaced.Spec.DeepCopy()
		templateLabels = templateNamespaced.Labels
	} else if okCluster {
		templateName = templateCluster.Name
		templateSpec = *templateCluster.Spec.DeepCopy()
		templateLabels = templateCluster.Labels
	} else {
		return nil, errors.New("object is not an AnalysisTemplate or ClusterAnalysisTemplate")
	}

	var args []analysisv1alpha1.Argument

	for _, v := range templateSpec.Args {
		ret := v

		// special value to insert timestamp evaluation
		if ret.Name == "prev-timestamp" {
			unix := release.Status.DeploymentStartTime.Time.Add(AnalysisTimeBeforeDeployment).Unix()
			unixStr := strconv.FormatInt(unix, 10)
			ret.Value = &unixStr
			ret.ValueFrom = nil
		} else {
			// in normal case, we replace args with release labels
			if metav1.HasLabel(release.ObjectMeta, ret.Name) {
				labelStr := release.Labels[ret.Name]
				ret.Value = &labelStr
			}

			if ret.Value == nil && ret.ValueFrom == nil {
				return nil, fmt.Errorf("could not determine value for arg %s and no default value set", ret.Name)
			}
		}
		args = append(args, ret)
	}

	run := &analysisv1alpha1.AnalysisRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      analysisRunName(release.Name, templateName),
			Namespace: release.Namespace,
			Labels:    templateLabels,
		},
		Spec: analysisv1alpha1.AnalysisRunSpec{
			Args:    args,
			Metrics: templateSpec.Metrics,
		},
	}

	controllerutil.SetControllerReference(release, run, r.Scheme)
	return run, nil
}

func hashString(b []byte) string {
	hash := sha256.Sum256(b)
	return fmt.Sprintf("%x", hash)[:7]
}

// analysisRunName generates a name for an AnalysisRun by concatenating the
// release name and template. If the result would be too long, parts are trimmed
// to 31 characters and a hash is appended..
func analysisRunName(releaseName, templateName string) string {
	parts := []string{releaseName, templateName}
	candidate := strings.Join(parts, "-")

	if len(candidate) < 64 {
		return candidate
	}

	hash := hashString([]byte(releaseName + templateName))

	for i, v := range parts {
		partLen := len(v)
		if partLen > 31 {
			parts[i] = v[:32]
		}
	}

	parts = append(parts, hash)

	return strings.Join(parts, "-")
}

// concatTemplateLists takes a list of objects of type AnalysisTemplateList or ClusterAnalysisTemplateList and returns a list of runtime.Object containing all items from the lists
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
// AnalysisPhase to a list of AnalysisRun names in each phase
func parseAnalysisResults(analysisList []analysisv1alpha1.AnalysisRun) map[analysisv1alpha1.AnalysisPhase][]string {
	out := map[analysisv1alpha1.AnalysisPhase][]string{
		analysisv1alpha1.AnalysisPhasePending:      []string{},
		analysisv1alpha1.AnalysisPhaseRunning:      []string{},
		analysisv1alpha1.AnalysisPhaseSuccessful:   []string{},
		analysisv1alpha1.AnalysisPhaseFailed:       []string{},
		analysisv1alpha1.AnalysisPhaseError:        []string{},
		analysisv1alpha1.AnalysisPhaseInconclusive: []string{},
	}

	for _, v := range analysisList {
		out[v.Status.Phase] = append(out[v.Status.Phase], v.Name)
	}

	return out
}

type conditionGen struct {
	conditionType       string
	conditionStatusBad  metav1.ConditionStatus
	conditionStatusGood metav1.ConditionStatus
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

// statusKnown returns true if all provided conditions are present and have
// true or false, but not unknown, status
func statusKnown(release *deployv1alpha1.Release, conditionTypes []string) bool {
	for _, v := range conditionTypes {
		cond := meta.FindStatusCondition(release.Status.Conditions, v)
		if cond == nil || cond.Status == metav1.ConditionUnknown {
			return false
		}
	}
	return true
}
