package console

import (
	"context"
	"reflect"
	"regexp"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	kitlog "github.com/go-kit/kit/log"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/pkg/client/clientset/versioned/scheme"
	"github.com/gocardless/theatre/pkg/recutil"
)

const (
	EventStart          = "Start"
	EventComplete       = "End"
	EventCreateOrUpdate = "CreateOrUpdate"
	EventRequeued       = "Requeued"
	EventDeleted        = "Deleted"
	EventSetOwner       = "SetOwner"
	EventSetTTL         = "SetTTL"

	Job             = "Job"
	Console         = "Console"
	ConsoleTemplate = "ConsoleTemplate"
	Role            = "Role"
	RoleBinding     = "RoleBinding"

	DefaultTTL = 24 * time.Hour
)

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
		&source.Kind{Type: &workloadsv1alpha1.Console{}}, &handler.EnqueueRequestForObject{},
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
		return res, err
	}

	// Set the template as owner of the console
	// This means the console will be deleted if the template is deleted
	if err := r.setConsoleOwner(tpl); err != nil {
		return res, err
	}

	// Set the TTL of the console
	if err := r.setConsoleTTL(tpl); err != nil {
		return res, err
	}

	// Clamp the console timeout
	if err := r.clampTimeout(tpl); err != nil {
		return res, err
	}

	// Create or update the job
	job := buildJob(r.name, r.console, tpl)
	if err := r.createOrUpdate(job, Job, jobDiff); err != nil {
		return res, err
	}

	// Create or update the role
	role := buildRole(r.name)
	if err := r.createOrUpdate(role, Role, recutil.RoleDiff); err != nil {
		return res, err
	}

	// Create or update the role binding
	rb := buildRoleBinding(r.name, role, r.console, tpl)
	if err := r.createOrUpdate(rb, RoleBinding, recutil.RoleBindingDiff); err != nil {
		return res, err
	}

	// Update the status fields in case they're out of sync, or the console spec
	// has been updated
	if err = r.updateStatus(job); err != nil {
		return res, err
	}

	// Requeue reconciliation if the status may change
	switch {
	case r.console.Pending():
		res = requeueAfterInterval(r.logger, time.Second)
	case r.console.Running():
		res = requeueForExpiration(r.logger, r.console.Status)
	case r.console.Stopped():
		// Requeue for when the console needs to be deleted
		// In the future we could allow this to be configured in the template
		res = requeueAfterInterval(r.logger, r.console.TTLDuration())
	}

	if r.console.EligibleForGC() {
		r.logger.Log("event", EventDeleted, "resource", Console, "msg", "deleted console")
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

func (r *reconciler) setConsoleOwner(consoleTemplate *workloadsv1alpha1.ConsoleTemplate) error {
	updatedCsl := r.console.DeepCopy()
	if err := controllerutil.SetControllerReference(consoleTemplate, updatedCsl, scheme.Scheme); err != nil {
		return err
	}

	if reflect.DeepEqual(r.console.ObjectMeta, updatedCsl.ObjectMeta) {
		return nil
	}
	if err := r.client.Update(r.ctx, updatedCsl); err != nil {
		return err
	}

	r.console = updatedCsl
	r.logger.Log("resource", Console, "event", EventSetOwner, "owner", consoleTemplate.ObjectMeta.Name)
	return nil
}

func (r *reconciler) setConsoleTTL(consoleTemplate *workloadsv1alpha1.ConsoleTemplate) error {
	if r.console.Spec.TTLSecondsAfterFinished != nil {
		return nil
	}

	updatedCsl := r.console.DeepCopy()
	defaultTTL := int32(DefaultTTL.Seconds())

	if consoleTemplate.Spec.DefaultTTLSecondsAfterFinished != nil {
		updatedCsl.Spec.TTLSecondsAfterFinished = consoleTemplate.Spec.DefaultTTLSecondsAfterFinished
	} else {
		updatedCsl.Spec.TTLSecondsAfterFinished = &defaultTTL
	}

	if err := r.client.Update(r.ctx, updatedCsl); err != nil {
		return err
	}

	r.console = updatedCsl
	r.logger.Log("resource", Console, "event", EventSetTTL, "ttl", updatedCsl.Spec.TTLSecondsAfterFinished, "msg", "setting ttl")
	return nil
}

func (r *reconciler) createOrUpdate(expected recutil.ObjWithMeta, kind string, diffFunc recutil.DiffFunc) error {
	if err := controllerutil.SetControllerReference(r.console, expected, scheme.Scheme); err != nil {
		return err
	}

	outcome, err := recutil.CreateOrUpdate(r.ctx, r.client, expected, kind, diffFunc)
	if err != nil {
		return err
	}

	r.logger.Log("resource", kind, "event", EventCreateOrUpdate, "outcome", outcome)
	return nil
}

// Ensure the console timeout is between [0, template.MaxTimeoutSeconds]
func (r *reconciler) clampTimeout(template *workloadsv1alpha1.ConsoleTemplate) error {
	var timeout int
	switch {
	case r.console.Spec.TimeoutSeconds < 1:
		timeout = template.Spec.DefaultTimeoutSeconds
	case r.console.Spec.TimeoutSeconds > template.Spec.MaxTimeoutSeconds:
		timeout = template.Spec.MaxTimeoutSeconds
	default:
		timeout = r.console.Spec.TimeoutSeconds
	}

	if timeout == r.console.Spec.TimeoutSeconds {
		return nil
	}

	updatedCsl := r.console.DeepCopy()
	updatedCsl.Spec.TimeoutSeconds = timeout
	if err := r.client.Update(r.ctx, updatedCsl); err != nil {
		return err
	}

	r.console = updatedCsl
	return nil
}

func (r *reconciler) updateStatus(job *batchv1.Job) error {
	// Fetch the job's pod
	podList := &corev1.PodList{}
	pod := &corev1.Pod{}
	opts := client.
		InNamespace(r.name.Namespace).
		MatchingLabels(map[string]string{"job-name": job.ObjectMeta.Name})
	if err := r.client.List(r.ctx, opts, podList); err != nil {
		return err
	}
	if len(podList.Items) > 0 {
		pod = &podList.Items[0]
	} else {
		pod = nil
	}
	newStatus := calculateStatus(r.console, job, pod)

	// If there's no changes in status, don't unnecessarily update the object.
	// This would cause an infinite loop!
	if reflect.DeepEqual(r.console.Status, newStatus) {
		return nil
	}

	updatedCsl := r.console.DeepCopy()
	updatedCsl.Status = newStatus

	// Run a full Update, rather than UpdateStatus, as we can't guarantee that
	// the CustomResourceSubresources feature will be available.
	if err := r.client.Update(r.ctx, updatedCsl); err != nil {
		return err
	}

	r.console = updatedCsl
	return nil
}

func calculateStatus(csl *workloadsv1alpha1.Console, job *batchv1.Job, pod *corev1.Pod) workloadsv1alpha1.ConsoleStatus {
	newStatus := csl.DeepCopy().Status

	// We may have been passed an empty Job struct, if the job no longer exists,
	// so determine whether it's a real job by checking if it has a name.
	if job != nil && len(job.Name) != 0 {
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
		if pod != nil {
			newStatus.PodName = pod.ObjectMeta.Name
		}

		newStatus.Phase = calculatePhase(job, pod)
	}

	return newStatus
}

func calculatePhase(job *batchv1.Job, pod *corev1.Pod) workloadsv1alpha1.ConsolePhase {
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

func requeueForExpiration(logger kitlog.Logger, status workloadsv1alpha1.ConsoleStatus) reconcile.Result {
	// Requeue after the expiry is hit. Add a second to be on the safe side,
	// ensuring that we'll always re-reconcile *after* the expiry time has been
	// hit (even if the clock drifts), as metav1.Time only has second-resolution.
	requeueTime := status.ExpiryTime.Time.Add(time.Second)
	sleepDuration := requeueTime.Sub(time.Now())

	res := reconcile.Result{}
	res.RequeueAfter = sleepDuration

	logger.Log("event", EventRequeued, "reconcile_at", requeueTime)

	return res
}

func requeueAfterInterval(logger kitlog.Logger, interval time.Duration) reconcile.Result {
	logger.Log("event", EventRequeued, "reconcile_at", interval)
	return reconcile.Result{Requeue: true, RequeueAfter: interval}
}

func buildJob(name types.NamespacedName, csl *workloadsv1alpha1.Console, template *workloadsv1alpha1.ConsoleTemplate) *batchv1.Job {
	timeout := int64(csl.Spec.TimeoutSeconds)

	username := sanitiseLabel(csl.Spec.User)
	jobTemplate := template.Spec.Template.DeepCopy()

	if jobTemplate.Labels == nil {
		jobTemplate.Labels = map[string]string{}
	}
	jobTemplate.Labels["user"] = username
	jobTemplate.Labels["repo"] = csl.Labels["repo"]
	jobTemplate.Labels["environment"] = csl.Labels["environment"]

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
		// TODO: Emit a warning event that only the first container will have its
		// command replaced
	}

	// Do not retry console jobs if they fail. There is no guarantee that the
	// command that the user submits will be idempotent.
	// This also prevents multiple pods from being spawned by a job, which is
	// important as other parts of the controller assume there will only ever be
	// 1 pod per job.
	backoffLimit := int32(0)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
			Labels: map[string]string{
				"user":        username,
				"repo":        csl.Labels["repo"],
				"environment": csl.Labels["environment"],
			},
		},
		Spec: batchv1.JobSpec{
			Template:              *jobTemplate,
			ActiveDeadlineSeconds: &timeout,
			BackoffLimit:          &backoffLimit,
		},
	}
}

func buildRole(name types.NamespacedName) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
		Rules: []rbacv1.PolicyRule{
			rbacv1.PolicyRule{
				Verbs:         []string{"*"},
				APIGroups:     []string{"core"},
				Resources:     []string{"pods/exec"},
				ResourceNames: []string{name.Name},
			},
		},
	}
}

func buildRoleBinding(name types.NamespacedName, role *rbacv1.Role, csl *workloadsv1alpha1.Console, tpl *workloadsv1alpha1.ConsoleTemplate) *rbacv1.RoleBinding {
	subjects := append(
		tpl.Spec.AdditionalAttachSubjects,
		rbacv1.Subject{Kind: "User", Name: csl.Spec.User},
	)

	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
		Subjects: subjects,
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     name.Name,
		},
	}
}

// jobDiff is a reconcile.DiffFunc for Jobs
func jobDiff(expectedObj runtime.Object, existingObj runtime.Object) recutil.Outcome {
	expected := expectedObj.(*batchv1.Job)
	existing := existingObj.(*batchv1.Job)
	operation := recutil.None

	// k8s manages the job's metadata, and doesn't allow us to clobber some of the values
	// it has set (for example, the controller-uid label). To avoid this we only update
	// the pod template spec.
	if !reflect.DeepEqual(expected.Spec.Template.Spec, existing.Spec.Template.Spec) {
		existing.Spec.Template.Spec = expected.Spec.Template.Spec
		operation = recutil.Update
	}

	if !reflect.DeepEqual(expected.Spec.ActiveDeadlineSeconds, existing.Spec.ActiveDeadlineSeconds) {
		existing.Spec.ActiveDeadlineSeconds = expected.Spec.ActiveDeadlineSeconds
		operation = recutil.Update
	}

	return operation
}

// Kubernetes labels must satisfy (([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?
// We don't bother with the first and last character sanitisation here - just anything
// dodgy in the middle.
// This is mostly so that, in tests, we correctly handle the system:unsecured user.

func sanitiseLabel(l string) string {
	return regexp.MustCompile("[^A-z0-9\\-_.]").ReplaceAllString(l, "-")
}
