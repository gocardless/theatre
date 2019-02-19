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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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

	Job                  = "job"
	Console              = "console"
	ConsoleTemplate      = "consoletemplate"
	Role                 = "role"
	DirectoryRoleBinding = "directoryrolebinding"

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
		return res, errors.Wrap(err, "failed to retrieve console template")
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
	job := r.buildJob(tpl)
	if err := r.createOrUpdate(job, Job, jobDiff); err != nil {
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
		// Create or update the role
		// Role grants permissions for a specific resource name, we need to
		// wait until the Pod is running to know the resource name
		role := buildRole(r.name, r.console.Status.PodName)
		if err := r.createOrUpdate(role, Role, recutil.RoleDiff); err != nil {
			return res, err
		}

		// Create or update the directory role binding
		drb := buildDirectoryRoleBinding(r.name, role, r.console, tpl)
		if err := r.createOrUpdate(drb, DirectoryRoleBinding, recutil.DirectoryRoleBindingDiff); err != nil {
			return res, err
		}

		res = requeueForExpiration(r.logger, r.console.Status)
	case r.console.Stopped():
		// Requeue for when the console needs to be deleted
		// In the future we could allow this to be configured in the template
		res = requeueAfterInterval(r.logger, r.console.TTLDuration())
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

func (r *reconciler) setConsoleOwner(consoleTemplate *workloadsv1alpha1.ConsoleTemplate) error {
	updatedCsl := r.console.DeepCopy()
	if err := controllerutil.SetControllerReference(consoleTemplate, updatedCsl, scheme.Scheme); err != nil {
		return errors.Wrap(err, "failed to set controller reference")
	}

	if reflect.DeepEqual(r.console.ObjectMeta, updatedCsl.ObjectMeta) {
		return nil
	}
	if err := r.client.Update(r.ctx, updatedCsl); err != nil {
		return errors.Wrap(err, "failed to update controller reference")
	}

	r.console = updatedCsl
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
		return errors.Wrap(err, "failed to set TTL")
	}

	r.console = updatedCsl
	return nil
}

func (r *reconciler) createOrUpdate(expected recutil.ObjWithMeta, kind string, diffFunc recutil.DiffFunc) error {
	if err := controllerutil.SetControllerReference(r.console, expected, scheme.Scheme); err != nil {
		return err
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
func (r *reconciler) clampTimeout(template *workloadsv1alpha1.ConsoleTemplate) error {
	var timeout int
	max := template.Spec.MaxTimeoutSeconds

	switch {
	case r.console.Spec.TimeoutSeconds < 1:
		timeout = template.Spec.DefaultTimeoutSeconds
	case r.console.Spec.TimeoutSeconds > max:
		r.logger.Log(
			"event", EventInvalidSpecification,
			"error", fmt.Sprintf("Specified timeout exceeded the template maximum; reduced to %ds", max),
		)
		timeout = max
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
		return errors.Wrap(err, "failed to update status")
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
	sleepDuration := time.Until(requeueTime)

	// Avoid requeueing a negative duration, as this effectively results in no
	// re-reconcile ocurring.
	// This case will be hit when the Console still has a phase of Running, but
	// the expiration time has passed, which occurs when the pod has not
	// terminated within 1s (as per the above second that's added to the expiry)
	// of the job deadline.
	if sleepDuration < 0 {
		sleepDuration = time.Second
	}
	return requeueAfterInterval(logger, sleepDuration)
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

	// Do not retry console jobs if they fail. There is no guarantee that the
	// command that the user submits will be idempotent.
	// This also prevents multiple pods from being spawned by a job, which is
	// important as other parts of the controller assume there will only ever be
	// 1 pod per job.
	backoffLimit := int32(0)
	jobTemplate.Spec.RestartPolicy = corev1.RestartPolicyNever

	// Ensure that the job name (after suffixing with `-console`) does not exceed 57
	// characters, to allow an additional 6 characters to appended when the job
	// creates a pod without truncation of the `-console` suffix.
	jobName := fmt.Sprintf("%s-%s", truncateString(r.name.Name, 49), "console")

	// Merged labels from the console template and console. In case of
	// conflicts second label set wins.
	// The labels on the console can be user-defined, so we do not want to allow a
	// user to create a console with a label that implies that it's for an application
	// different to the console.
	jobLabels := labels.Merge(csl.Labels, template.Labels)
	jobLabels = labels.Merge(jobLabels,
		map[string]string{
			"console-name": truncateString(csl.Name, 63),
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
				Resources:     []string{"pods/logs"},
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

func buildDirectoryRoleBinding(name types.NamespacedName, role *rbacv1.Role, csl *workloadsv1alpha1.Console, tpl *workloadsv1alpha1.ConsoleTemplate) *rbacv1alpha1.DirectoryRoleBinding {
	subjects := append(
		tpl.Spec.AdditionalAttachSubjects,
		rbacv1.Subject{Kind: "User", Name: csl.Spec.User},
	)

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
	return regexp.MustCompile(`[^A-z0-9\-_.]`).ReplaceAllString(l, "-")
}

func truncateString(str string, length int) string {
	if len(str) > length {
		return str[0:length]
	}
	return str
}
