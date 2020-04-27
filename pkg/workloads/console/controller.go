package console

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	kitlog "github.com/go-kit/kit/log"
	"github.com/pkg/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	rbacv1alpha1 "github.com/gocardless/theatre/pkg/apis/rbac/v1alpha1"
	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/pkg/client/clientset/versioned/scheme"
	"github.com/gocardless/theatre/pkg/logging"
	"github.com/gocardless/theatre/pkg/recutil"
)

const (
	// Resource-level events

	EventDelete           = "Delete"
	EventSuccessfulCreate = "SuccessfulCreate"
	EventSuccessfulUpdate = "SuccessfulUpdate"
	EventNoCreateOrUpdate = "NoCreateOrUpdate"

	// Warning events

	EventUnknownOutcome       = "UnknownOutcome"
	EventInvalidSpecification = "InvalidSpecification"
	EventTemplateUnsupported  = "TemplateUnsupported"

	// Console log keys

	ConsolePendingAuthorisation = "ConsolePendingAuthorisation"
	ConsoleAuthorised           = "ConsoleAuthorised"
	ConsoleStarted              = "ConsoleStarted"
	ConsoleEnded                = "ConsoleEnded"
	ConsoleDestroyed            = "ConsoleDestroyed"

	Job                  = "job"
	Console              = "console"
	ConsoleAuthorisation = "consoleauthorisation"
	ConsoleTemplate      = "consoletemplate"
	Role                 = "role"
	DirectoryRoleBinding = "directoryrolebinding"

	DefaultTTLBeforeRunning = 1 * time.Hour
	DefaultTTLAfterFinished = 24 * time.Hour
)

type IgnoreCreatePredicate struct {
	predicate.Funcs
}

func (IgnoreCreatePredicate) Create(e event.CreateEvent) bool {
	return false
}

func Add(ctx context.Context, logger kitlog.Logger, mgr manager.Manager, opts ...func(*controller.Options)) (controller.Controller, error) {
	logger = kitlog.With(logger, "component", "Console")
	ctrlOptions := controller.Options{
		Reconciler: recutil.ResolveAndReconcile(
			ctx, logger, mgr, &workloadsv1alpha1.Console{},
			func(logger kitlog.Logger, request reconcile.Request, obj runtime.Object) (reconcile.Result, error) {
				reconciler := &reconciler{
					ctx:     ctx,
					logger:  logger,
					client:  mgr.GetClient(),
					console: obj.(*workloadsv1alpha1.Console),
					name:    request.NamespacedName,
				}

				return reconciler.Reconcile()
			},
		),
	}

	for _, opt := range opts {
		opt(&ctrlOptions)
	}

	ctrl, err := controller.New("console-controller", mgr, ctrlOptions)
	if err != nil {
		return ctrl, err
	}

	err = ctrl.Watch(
		&source.Kind{Type: &workloadsv1alpha1.Console{}},
		&handler.EnqueueRequestForObject{},
	)
	if err != nil {
		return ctrl, err
	}

	// Watch for updates to console authorisations: if this is a console that
	// requires additional authorisation then this will be the trigger for
	// whether to actually create the console Job.
	err = ctrl.Watch(
		&source.Kind{Type: &workloadsv1alpha1.ConsoleAuthorisation{}},
		&handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &workloadsv1alpha1.Console{},
		},
		// Don't unnecessarily reconcile when the controller initially creates the
		// authorisation object.
		IgnoreCreatePredicate{},
	)
	if err != nil {
		return ctrl, err
	}

	// watch for Job events created by Consoles and trigger a reconcile for the
	// owner
	err = ctrl.Watch(
		&source.Kind{Type: &batchv1.Job{}},
		&handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &workloadsv1alpha1.Console{},
		},
	)

	return ctrl, err
}

type reconciler struct {
	ctx     context.Context
	logger  kitlog.Logger
	client  client.Client
	name    types.NamespacedName
	console *workloadsv1alpha1.Console
}

func (r *reconciler) Reconcile() (res reconcile.Result, err error) {
	// Fetch console template
	var tpl *workloadsv1alpha1.ConsoleTemplate
	if tpl, err = r.getConsoleTemplate(); err != nil {
		return res, errors.Wrap(err, "failed to retrieve console template")
	}

	// Set the template as owner of the console
	// This means the console will be deleted if the template is deleted
	console, err := setConsoleOwner(r.console, tpl)
	if err != nil {
		return res, errors.Wrap(err, "failed to set controller reference on console object")
	}

	console = setConsoleTTLs(console, tpl)

	console = r.setConsoleTimeout(console, tpl)

	// We call this function here to ensure that we perform an update on the
	// console object *if* one is needed; i.e. it defends against not correctly
	// wrapping an `Update()` call in a conditional. It also makes the console
	// object updates more consistent with the other resources we maintain in this
	// control loop.
	//
	// This will in turn call the recutil.CreateOrUpdate function, which _could_
	// be considered a slight impedance mismatch in this context: That function
	// will go and retrieve the latest version of the object from the Kubernetes
	// API before performing the update, but this isn't strictly necessary here
	// because we've already been provided a fresh version of the object when
	// entering the reconciliation loop.
	// For the moment though, the simplification and safety outweighs the cost of
	// the extra API call.
	if err := r.createOrUpdate(console, Console, consoleDiff); err != nil {
		return res, err
	}

	r.console = console

	// Get the command for the console to run
	var command []string
	if command, err = r.getCommand(tpl); err != nil {
		return res, errors.Wrap(err, "neither the console or template have a command to evaluate")
	}

	// Create an authorisation object, if required.
	var authRule *workloadsv1alpha1.ConsoleAuthorisationRule
	if tpl.HasAuthorisationRules() {
		rule, err := tpl.GetAuthorisationRuleForCommand(command)
		if err != nil {
			return res, errors.Wrap(err, "failed to determine authorisation rule for console command")
		}

		authRule = &rule
		if err := r.createAuthorisationObjects(authRule.Subjects); err != nil {
			return res, err
		}
	}

	// Try to get the consoles job
	var job *batchv1.Job
	if j, err := r.getJob(); err == nil {
		job = j
	}

	// Only create/update a job when the console is authorised and pending job
	// creation or when a job already exists, i.e. if we've already passed the
	// Creating phase, but the job no longer exists (it's been destroyed external
	// to this controller) then don't recreate it.
	authorised, err := r.isAuthorised(authRule)
	if err != nil {
		return res, err
	}
	if (authorised && r.console.PendingJob()) || job != nil {
		job = r.buildJob(tpl)
		if err := r.createOrUpdate(job, Job, jobDiff); err != nil {
			return res, err
		}
	}

	// Update the status fields in case they're out of sync, or the console spec
	// has been updated
	if err = r.updateStatus(authorised, job); err != nil {
		return res, err
	}

	switch {
	case r.console.PendingAuthorisation():
		// Requeue for when the console has reached its before-running TTL, so that
		// it can be deleted if it has not yet been authorised by that point.
		res = requeueAfterInterval(r.logger, time.Until(*r.console.GetGCTime()))
	case r.console.Pending():
		// Requeue every second while job has been created but there is not yet a
		// running pod: we won't receive an event via the job watcher when this
		// event happens, so this is a cheaper alternative to watching the
		// pods resource and triggering reconciliations via that.
		res = requeueAfterInterval(r.logger, time.Second)
	case r.console.Running():
		// Create or update the role
		// Role grants permissions for a specific resource name, we need to
		// wait until the Pod is running to know the resource name
		role := buildRole(r.name, r.console.Status.PodName)
		if err := r.createOrUpdate(role, Role, recutil.RoleDiff); err != nil {
			return res, err
		}

		// Create or update the directory role binding
		subjects := append(
			tpl.Spec.AdditionalAttachSubjects,
			rbacv1.Subject{Kind: "User", Name: r.console.Spec.User},
		)

		drb := buildDirectoryRoleBinding(r.name, role, subjects)
		if err := r.createOrUpdate(drb, DirectoryRoleBinding, recutil.DirectoryRoleBindingDiff); err != nil {
			return res, err
		}
	case r.console.PostRunning():
		// Requeue for when the console has reached its after finished TTL so it can be deleted
		res = requeueAfterInterval(r.logger, time.Until(*r.console.GetGCTime()))
	}

	if r.console.EligibleForGC() {
		r.logger.Log("event", EventDelete, "kind", Console, "msg", "Deleting expired console")
		if err = r.client.Delete(r.ctx, r.console, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
			return res, err
		}
		res = reconcile.Result{Requeue: false}
	}

	return res, err
}

func (r *reconciler) getConsoleTemplate() (*workloadsv1alpha1.ConsoleTemplate, error) {
	name := types.NamespacedName{
		Name:      r.console.Spec.ConsoleTemplateRef.Name,
		Namespace: r.name.Namespace,
	}

	tpl := &workloadsv1alpha1.ConsoleTemplate{}
	return tpl, r.client.Get(r.ctx, name, tpl)
}

func (r *reconciler) getCommand(template *workloadsv1alpha1.ConsoleTemplate) ([]string, error) {
	csl := r.console
	if len(csl.Spec.Command) > 0 {
		return csl.Spec.Command, nil
	}
	return template.GetDefaultCommandWithArgs()
}

func (r *reconciler) getConsoleAuthorisation() (*workloadsv1alpha1.ConsoleAuthorisation, error) {
	name := types.NamespacedName{
		Name:      r.name.Name,
		Namespace: r.name.Namespace,
	}

	auth := &workloadsv1alpha1.ConsoleAuthorisation{}
	return auth, r.client.Get(r.ctx, name, auth)
}

func (r *reconciler) getJob() (*batchv1.Job, error) {
	name := types.NamespacedName{
		Name:      getJobName(r.name.Name),
		Namespace: r.name.Namespace,
	}

	job := &batchv1.Job{}
	return job, r.client.Get(r.ctx, name, job)
}

func setConsoleOwner(console *workloadsv1alpha1.Console, consoleTemplate *workloadsv1alpha1.ConsoleTemplate) (*workloadsv1alpha1.Console, error) {
	updatedCsl := console.DeepCopy()
	if err := controllerutil.SetControllerReference(consoleTemplate, updatedCsl, scheme.Scheme); err != nil {
		return nil, errors.Wrap(err, "failed to set controller reference")
	}

	return updatedCsl, nil
}

func setConsoleTTLs(console *workloadsv1alpha1.Console, consoleTemplate *workloadsv1alpha1.ConsoleTemplate) *workloadsv1alpha1.Console {
	defaultTTLSecondsBeforeRunning := int32(DefaultTTLBeforeRunning.Seconds())
	defaultTTLSecondsAfterFinished := int32(DefaultTTLAfterFinished.Seconds())

	updatedCsl := console.DeepCopy()

	if console.Spec.TTLSecondsBeforeRunning != nil {
		updatedCsl.Spec.TTLSecondsBeforeRunning = console.Spec.TTLSecondsBeforeRunning
	} else if consoleTemplate.Spec.DefaultTTLSecondsBeforeRunning != nil {
		updatedCsl.Spec.TTLSecondsBeforeRunning = consoleTemplate.Spec.DefaultTTLSecondsBeforeRunning
	} else {
		updatedCsl.Spec.TTLSecondsBeforeRunning = &defaultTTLSecondsBeforeRunning
	}

	if console.Spec.TTLSecondsAfterFinished != nil {
		updatedCsl.Spec.TTLSecondsAfterFinished = console.Spec.TTLSecondsAfterFinished
	} else if consoleTemplate.Spec.DefaultTTLSecondsAfterFinished != nil {
		updatedCsl.Spec.TTLSecondsAfterFinished = consoleTemplate.Spec.DefaultTTLSecondsAfterFinished
	} else {
		updatedCsl.Spec.TTLSecondsAfterFinished = &defaultTTLSecondsAfterFinished
	}

	return updatedCsl
}

func (r *reconciler) createOrUpdate(expected recutil.ObjWithMeta, kind string, diffFunc recutil.DiffFunc) error {
	// If operating on the console itself, don't attempt to set the controller
	// reference, as this isn't valid.
	if kind != Console {
		if err := controllerutil.SetControllerReference(r.console, expected, scheme.Scheme); err != nil {
			return err
		}
	}

	outcome, err := recutil.CreateOrUpdate(r.ctx, r.client, expected, diffFunc)
	if err != nil {
		return errors.Wrap(err, "CreateOrUpdate failed")
	}

	// Use the same 'kind: obj-name' format as in the core controllers, when
	// emitting events.
	objDesc := fmt.Sprintf("%s: %s", kind, expected.GetName())

	switch outcome {
	case recutil.Create:
		r.logger.Log("event", EventSuccessfulCreate, "msg", "Created "+objDesc)
	case recutil.Update:
		r.logger.Log("event", EventSuccessfulUpdate, "msg", "Updated "+objDesc)
	case recutil.None:
		logging.WithNoRecord(r.logger).Log(
			"event", EventNoCreateOrUpdate, "msg", "Nothing to do for "+objDesc,
		)
	default:
		// This is only possible in case we implement new outcomes and forget to
		// add a case here; in which case we should log a warning.
		r.logger.Log(
			"event", EventUnknownOutcome,
			"error", fmt.Sprintf("Unknown outcome %s for %s", outcome, objDesc),
		)
	}

	return nil
}

// Ensure the console timeout is between [0, template.MaxTimeoutSeconds]
func (r *reconciler) setConsoleTimeout(console *workloadsv1alpha1.Console, template *workloadsv1alpha1.ConsoleTemplate) *workloadsv1alpha1.Console {
	var timeout int
	max := template.Spec.MaxTimeoutSeconds

	switch {
	case console.Spec.TimeoutSeconds < 1:
		timeout = template.Spec.DefaultTimeoutSeconds
	case console.Spec.TimeoutSeconds > max:
		r.logger.Log(
			"event", EventInvalidSpecification,
			"error", fmt.Sprintf("Specified timeout exceeded the template maximum; reduced to %ds", max),
		)
		timeout = max
	default:
		timeout = console.Spec.TimeoutSeconds
	}

	updatedCsl := console.DeepCopy()
	updatedCsl.Spec.TimeoutSeconds = timeout

	return updatedCsl
}

func (r *reconciler) isAuthorised(rule *workloadsv1alpha1.ConsoleAuthorisationRule) (bool, error) {
	if rule == nil {
		return true, nil
	}

	auth, err := r.getConsoleAuthorisation()
	if err != nil {
		return false, errors.Wrap(err, "failed to retrieve console authorisation")
	}

	if len(auth.Spec.Authorisations) >= rule.ConsoleAuthorisers.AuthorisationsRequired {
		return true, nil
	}

	return false, nil
}

func (r *reconciler) updateStatus(authorised bool, job *batchv1.Job) error {
	podList := &corev1.PodList{}
	pod := &corev1.Pod{}

	if job != nil {
		opts := client.
			InNamespace(r.name.Namespace).
			MatchingLabels(map[string]string{"job-name": job.ObjectMeta.Name})
		if err := r.client.List(r.ctx, opts, podList); err != nil {
			return err
		}
	}
	if len(podList.Items) > 0 {
		pod = &podList.Items[0]
	} else {
		pod = nil
	}

	newStatus := calculateStatus(r.console, authorised, job, pod)

	// If there's no changes in status, don't unnecessarily update the object.
	// This would cause an infinite loop!
	if reflect.DeepEqual(r.console.Status, newStatus) {
		return nil
	}

	if r.console.Creating() && newStatus.Phase == workloadsv1alpha1.ConsolePendingAuthorisation {
		auditLog(r.logger, r.console, nil, ConsolePendingAuthorisation, "Console pending authorisation")
	}

	// Console phase from Pending Authorisation
	if r.console.PendingAuthorisation() && newStatus.Phase != workloadsv1alpha1.ConsolePendingAuthorisation {
		auditLog(r.logger, r.console, nil, ConsoleAuthorised, "Console authorised")
	}

	// Console phase from Pending to Running
	if r.console.Pending() && newStatus.Phase == workloadsv1alpha1.ConsoleRunning {
		auditLog(r.logger, r.console, nil, ConsoleStarted, "Console started")
	}

	// Console phase from Running to Stopped
	if newStatus.CompletionTime != r.console.Status.CompletionTime && !job.Status.StartTime.IsZero() {
		duration := job.Status.CompletionTime.Sub(job.Status.StartTime.Time).Seconds()
		auditLog(r.logger, r.console, &duration, ConsoleEnded, "Console ended")
	}

	// Console phase from Running to Stopped without CompletionTime (expire)
	if r.console.Running() &&
		newStatus.CompletionTime == nil && newStatus.Phase == workloadsv1alpha1.ConsoleStopped {
		duration := r.console.Status.ExpiryTime.Sub(job.Status.StartTime.Time).Seconds()
		auditLog(r.logger, r.console, &duration, ConsoleEnded, "Console ended after reaching expiration time")
	}

	if !r.console.Destroyed() && newStatus.Phase == workloadsv1alpha1.ConsoleDestroyed {
		auditLog(r.logger, r.console, nil, ConsoleDestroyed, "Console destroyed")
	}

	updatedCsl := r.console.DeepCopy()
	updatedCsl.Status = newStatus

	// Run a full Update, rather than UpdateStatus, as we can't guarantee that
	// the CustomResourceSubresources feature will be available.
	if err := r.client.Update(r.ctx, updatedCsl); err != nil {
		return errors.Wrap(err, "failed to update status")
	}

	r.console = updatedCsl
	return nil
}

func calculateStatus(csl *workloadsv1alpha1.Console, authorised bool, job *batchv1.Job, pod *corev1.Pod) workloadsv1alpha1.ConsoleStatus {
	newStatus := csl.DeepCopy().Status

	if job != nil {
		// We want to give the console session *at least* the time specified in the
		// timeout, therefore base the expiry time on the job creation time, rather
		// than the console creation time, to take into account any delays in
		// reconciling the console object.
		// TODO: We may actually want to use a base of when the Pod entered the
		// Running phase, as image pull time could be significant in some cases.
		jobCreationTime := job.ObjectMeta.CreationTimestamp.Time
		expiryTime := metav1.NewTime(
			jobCreationTime.Add(time.Second * time.Duration(csl.Spec.TimeoutSeconds)),
		)
		newStatus.ExpiryTime = &expiryTime
		newStatus.CompletionTime = job.Status.CompletionTime
	}
	if pod != nil {
		newStatus.PodName = pod.ObjectMeta.Name
	}

	newStatus.Phase = calculatePhase(authorised, job, pod)

	return newStatus
}

func calculatePhase(authorised bool, job *batchv1.Job, pod *corev1.Pod) workloadsv1alpha1.ConsolePhase {
	if !authorised {
		return workloadsv1alpha1.ConsolePendingAuthorisation
	}

	if job == nil {
		return workloadsv1alpha1.ConsoleDestroyed
	}

	// Currently a job can only have two conditions: Complete and Failed
	// Both indicate that the console has stopped
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed {
			return workloadsv1alpha1.ConsoleStopped
		}
	}

	// If the pod exists and is running, then the console is running
	if pod != nil && pod.Status.Phase == corev1.PodRunning {
		return workloadsv1alpha1.ConsoleRunning
	}

	// Otherwise, assume the console is pending (i.e. still starting up)
	return workloadsv1alpha1.ConsolePending
}

func requeueAfterInterval(logger kitlog.Logger, interval time.Duration) reconcile.Result {
	logging.WithNoRecord(logger).Log(
		"event", recutil.EventRequeued, "msg", "Reconciliation requeued", "reconcile_after", interval,
	)
	return reconcile.Result{Requeue: true, RequeueAfter: interval}
}

func (r *reconciler) buildJob(template *workloadsv1alpha1.ConsoleTemplate) *batchv1.Job {
	csl := r.console

	timeout := int64(csl.Spec.TimeoutSeconds)

	username := strings.SplitN(csl.Spec.User, "@", 2)[0]
	jobTemplate := template.Spec.Template.DeepCopy()

	numContainers := len(jobTemplate.Spec.Containers)

	// If there's no containers in the spec then the controller will be emitting
	// warnings anyway, as the job will be rejected
	if numContainers > 0 {
		container := &jobTemplate.Spec.Containers[0]

		// Only replace the template command if one is specified
		if len(csl.Spec.Command) > 0 {
			container.Command = csl.Spec.Command[:1]
			container.Args = csl.Spec.Command[1:]
		}

		// Set these properties to ensure that it's possible to send input to the
		// container when attaching
		container.Stdin = true
		container.TTY = true
	}

	if numContainers > 1 {
		r.logger.Log(
			"event", EventTemplateUnsupported,
			"error", "Only the first container in the template will be usable as a console",
		)
	}

	// Job API SetDefaults_Job
	// https://github.com/kubernetes/kubernetes/blob/master/pkg/apis/batch/v1/defaults.go#L28
	completions := int32(1)
	parallelism := int32(1)

	// Do not retry console jobs if they fail. There is no guarantee that the
	// command that the user submits will be idempotent.
	// This also prevents multiple pods from being spawned by a job, which is
	// important as other parts of the controller assume there will only ever be
	// 1 pod per job.
	backoffLimit := int32(0)
	jobTemplate.Spec.RestartPolicy = corev1.RestartPolicyNever

	jobName := getJobName(r.name.Name)

	// Merged labels from the console template and console. In case of
	// conflicts second label set wins.
	// The labels on the console can be user-defined, so we do not want to allow a
	// user to create a console with a label that implies that it's for an application
	// different to the console.
	jobLabels := labels.Merge(csl.Labels, template.Labels)
	jobLabels = labels.Merge(jobLabels,
		map[string]string{
			"console-name": sanitiseLabel(csl.Name),
			"user":         sanitiseLabel(username),
		})

	jobTemplate.ObjectMeta.Labels = labels.Merge(
		jobLabels,
		jobTemplate.ObjectMeta.Labels,
	)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: r.name.Namespace,
			Labels:    jobLabels,
		},
		Spec: batchv1.JobSpec{
			Template:              *jobTemplate,
			Completions:           &completions,
			Parallelism:           &parallelism,
			ActiveDeadlineSeconds: &timeout,
			BackoffLimit:          &backoffLimit,
		},
	}
}

func buildRole(name types.NamespacedName, podName string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:         []string{"create"},
				APIGroups:     []string{""},
				Resources:     []string{"pods/exec", "pods/attach"},
				ResourceNames: []string{podName},
			},
			{
				Verbs:         []string{"get"},
				APIGroups:     []string{""},
				Resources:     []string{"pods/log"},
				ResourceNames: []string{podName},
			},
			{
				Verbs:         []string{"get", "delete"},
				APIGroups:     []string{""},
				Resources:     []string{"pods"},
				ResourceNames: []string{podName},
			},
		},
	}
}

func buildDirectoryRoleBinding(name types.NamespacedName, role *rbacv1.Role, subjects []rbacv1.Subject) *rbacv1alpha1.DirectoryRoleBinding {
	return &rbacv1alpha1.DirectoryRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
		Spec: rbacv1alpha1.DirectoryRoleBindingSpec{
			Subjects: subjects,
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     name.Name,
			},
		},
	}
}

func (r *reconciler) createAuthorisationObjects(subjects []rbacv1.Subject) error {
	authorisation := &workloadsv1alpha1.ConsoleAuthorisation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.name.Name,
			Namespace: r.name.Namespace,
			Labels:    r.console.Labels,
		},
		Spec: workloadsv1alpha1.ConsoleAuthorisationSpec{
			ConsoleRef:     corev1.LocalObjectReference{Name: r.name.Name},
			Authorisations: []rbacv1.Subject{},
		},
	}

	// Create the consoleauthorisation
	if err := r.createOrUpdate(authorisation, ConsoleAuthorisation, authorisationDiff); err != nil {
		return errors.Wrap(err, "failed to create consoleauthorisation")
	}

	// We already create roles and directory rolebindings with the same name as
	// the console to provide permissions on the job/pod. Therefore for the name
	// of these objects, suffix the console name with '-authorisation'.
	rbacName := types.NamespacedName{
		Name:      fmt.Sprintf("%s-%s", r.name.Name, "authorisation"),
		Namespace: r.name.Namespace,
	}

	// Create the role that allows updating this consoleauthorisation
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbacName.Name,
			Namespace: r.name.Namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:         []string{"update", "get"},
				APIGroups:     []string{"workloads.crd.gocardless.com"},
				Resources:     []string{"consoleauthorisations"},
				ResourceNames: []string{r.name.Name},
			},
		},
	}

	if err := r.createOrUpdate(role, Role, recutil.RoleDiff); err != nil {
		return errors.Wrap(err, "failed to create role for consoleauthorisation")
	}

	// Create or update the directory role binding
	drb := buildDirectoryRoleBinding(rbacName, role, subjects)
	if err := r.createOrUpdate(drb, DirectoryRoleBinding, recutil.DirectoryRoleBindingDiff); err != nil {
		return errors.Wrap(err, "failed to create directory rolebinding for consoleauthorisation")
	}

	return nil
}

// authorisationDiff is a reconcile.DiffFunc for ConsoleAuthorisations
func authorisationDiff(expectedObj runtime.Object, existingObj runtime.Object) recutil.Outcome {
	expected := expectedObj.(*workloadsv1alpha1.ConsoleAuthorisation)
	existing := existingObj.(*workloadsv1alpha1.ConsoleAuthorisation)
	operation := recutil.None

	// compare labels
	if !reflect.DeepEqual(expected.ObjectMeta.Labels, existing.ObjectMeta.Labels) {
		existing.ObjectMeta.Labels = expected.ObjectMeta.Labels
		operation = recutil.Update
	}

	// compare all spec fields other than `authorisations`, which will be mutated
	// by the authorising user.

	if !reflect.DeepEqual(expected.Spec.ConsoleRef, existing.Spec.ConsoleRef) {
		existing.Spec.ConsoleRef = expected.Spec.ConsoleRef
		operation = recutil.Update
	}

	return operation
}

// consoleDiff is a reconcile.DiffFunc for Consoles
func consoleDiff(expectedObj runtime.Object, existingObj runtime.Object) recutil.Outcome {
	expected := expectedObj.(*workloadsv1alpha1.Console)
	existing := existingObj.(*workloadsv1alpha1.Console)
	operation := recutil.None

	// Because this controller is responsible for the Console object the diff
	// calculation is simple: if any of the spec or status fields, or the
	// controller reference, have changed then perform an update.
	if !reflect.DeepEqual(expected.ObjectMeta.OwnerReferences, existing.ObjectMeta.OwnerReferences) {
		existing.ObjectMeta.OwnerReferences = expected.ObjectMeta.OwnerReferences
		operation = recutil.Update
	}

	if !reflect.DeepEqual(expected.Spec, existing.Spec) {
		existing.Spec = expected.Spec
		operation = recutil.Update
	}

	if !reflect.DeepEqual(expected.Status, existing.Status) {
		existing.Status = expected.Status
		operation = recutil.Update
	}

	return operation
}

// jobDiff is a reconcile.DiffFunc for Jobs
func jobDiff(expectedObj runtime.Object, existingObj runtime.Object) recutil.Outcome {
	expected := expectedObj.(*batchv1.Job)
	existing := existingObj.(*batchv1.Job)
	operation := recutil.None

	// compare all mutable fields in jobSpec and labels
	if !reflect.DeepEqual(expected.ObjectMeta.Labels, existing.ObjectMeta.Labels) {
		existing.ObjectMeta.Labels = expected.ObjectMeta.Labels
		operation = recutil.Update
	}

	if !reflect.DeepEqual(expected.Spec.ActiveDeadlineSeconds, existing.Spec.ActiveDeadlineSeconds) {
		existing.Spec.ActiveDeadlineSeconds = expected.Spec.ActiveDeadlineSeconds
		operation = recutil.Update
	}

	if !reflect.DeepEqual(expected.Spec.BackoffLimit, existing.Spec.BackoffLimit) {
		existing.Spec.BackoffLimit = expected.Spec.BackoffLimit
		operation = recutil.Update
	}

	if !reflect.DeepEqual(expected.Spec.Completions, existing.Spec.Completions) {
		existing.Spec.Completions = expected.Spec.Completions
		operation = recutil.Update
	}

	if !reflect.DeepEqual(expected.Spec.Parallelism, existing.Spec.Parallelism) {
		existing.Spec.Parallelism = expected.Spec.Parallelism
		operation = recutil.Update
	}

	if !reflect.DeepEqual(expected.Spec.TTLSecondsAfterFinished, existing.Spec.TTLSecondsAfterFinished) {
		existing.Spec.TTLSecondsAfterFinished = expected.Spec.TTLSecondsAfterFinished
		operation = recutil.Update
	}

	return operation
}

// auditLog decorated a logger for for audit purposes
func auditLog(logger kitlog.Logger, c *workloadsv1alpha1.Console, duration *float64, event string, msg string) {
	loggerCtx := logging.WithNoRecord(logger)
	loggerCtx = kitlog.With(loggerCtx, "event", event, "kind", Console,
		"console_name", c.Name,
		"console_user", c.Spec.User,
		"reason", c.Spec.Reason)

	if duration != nil {
		loggerCtx = kitlog.With(loggerCtx, "duration", duration)
	}

	loggerCtx = logging.WithLabels(loggerCtx, c.Labels, "console_")
	loggerCtx.Log("msg", msg)
}

// Ensure that the job name (after suffixing with `-console`) does not exceed 63
// characters. This is the string length limit on labels and the job name is added
// as a label to the pods it creates.
func getJobName(consoleName string) string {
	return fmt.Sprintf("%s-%s", truncateString(consoleName, 55), "console")
}

// Kubernetes labels must satisfy (([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])? and not
// exceed 63 characters in length.
// We don't bother with the first and last character sanitisation here - just anything
// dodgy in the middle.
// This is mostly so that, in tests, we correctly handle the system:unsecured user.
func sanitiseLabel(l string) string {
	return truncateString(regexp.MustCompile(`[^A-z0-9\-_.]`).ReplaceAllString(l, "-"), 63)
}

func truncateString(str string, length int) string {
	if len(str) > length {
		return str[0:length]
	}
	return str
}
