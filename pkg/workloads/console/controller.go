package console

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	kitlog "github.com/go-kit/kit/log"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	k8rec "sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/pkg/client/clientset/versioned/scheme"
	"github.com/gocardless/theatre/pkg/reconcile"
)

const (
	EventStart    = "Start"
	EventComplete = "End"
	EventRequeued = "Requeued"
	EventFound    = "Found"
	EventNotFound = "NotFound"
	EventCreated  = "Created"
	EventExpired  = "Expired"
	EventDeleted  = "Deleted"
	EventError    = "Error"

	Job             = "Job"
	Console         = "Console"
	ConsoleTemplate = "ConsoleTemplate"
	Role            = "Role"
	RoleBinding     = "RoleBinding"
)

func Add(ctx context.Context, logger kitlog.Logger, mgr manager.Manager, opts ...func(*controller.Options)) (controller.Controller, error) {
	logger = kitlog.With(logger, "component", "Console")
	ctrlOptions := controller.Options{
		Reconciler: &Controller{
			ctx:      ctx,
			logger:   logger,
			recorder: mgr.GetRecorder("Console"),
			client:   mgr.GetClient(),
		},
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

type Controller struct {
	ctx      context.Context
	logger   kitlog.Logger
	recorder record.EventRecorder
	client   client.Client
}

func (c *Controller) Reconcile(request k8rec.Request) (k8rec.Result, error) {
	logger := kitlog.With(c.logger, "request", request)
	logger.Log("event", EventStart)

	csl := &workloadsv1alpha1.Console{}
	if err := c.client.Get(c.ctx, request.NamespacedName, csl); err != nil {
		if errors.IsNotFound(err) {
			logger.Log("event", EventNotFound, "resource", Console)
		}
		// If we can't find the console, there's no meaningful reconciliation we can do. For
		// example, the console may have been deleted. We don't want to retry in this case, as
		// we'll be retrying forever. So just return a successful reconcile result.
		return k8rec.Result{}, nil
	}

	// This is temporarily disabled as our logs don't pass k8s event validation
	// logger = logging.WithRecorder(logger, c.recorder, csl)

	reconciler := &reconciler{
		ctx:     c.ctx,
		logger:  logger,
		client:  c.client,
		name:    request.NamespacedName,
		console: csl,
	}
	result, err := reconciler.Reconcile()
	if err != nil {
		logger.Log("event", EventError, "error", err)
	}
	return result, err
}

type reconciler struct {
	ctx     context.Context
	logger  kitlog.Logger
	client  client.Client
	name    types.NamespacedName
	console *workloadsv1alpha1.Console
}

func (r *reconciler) Reconcile() (res k8rec.Result, err error) {
	// Fetch the console template
	consoleTemplateName := types.NamespacedName{
		Name:      r.console.Spec.ConsoleTemplateRef.Name,
		Namespace: r.name.Namespace,
	}
	consoleTemplate := &workloadsv1alpha1.ConsoleTemplate{}
	if err := r.client.Get(r.ctx, consoleTemplateName, consoleTemplate); err != nil {
		if errors.IsNotFound(err) {
			r.logger.Log("event", EventNotFound, "resource", ConsoleTemplate)
		}
		return res, err
	}

	// Create or update the job
	job := buildJob(r.name, r.console, consoleTemplate.Spec.Template)
	if err := r.createOrUpdate(job, Job, jobDiff); err != nil {
		return res, err
	}

	// Create or update the role
	role := buildRole(r.name)
	if err := r.createOrUpdate(role, Role, reconcile.RoleDiff); err != nil {
		return res, err
	}

	// Create or update the role binding
	subjects := append(
		consoleTemplate.Spec.AdditionalAttachSubjects,
		rbacv1.Subject{Kind: "User", Name: r.console.Spec.User},
	)
	rb := buildRoleBinding(r.name, role, subjects)
	if err := r.createOrUpdate(rb, RoleBinding, reconcile.RoleBindingDiff); err != nil {
		return res, err
	}

	// Update the status fields in case they're out of sync, or the console spec
	// has been updated
	if err = r.updateStatus(job); err != nil {
		return res, err
	}

	// Requeue reconciliation if the status may change
	switch r.console.Status.Phase {
	case workloadsv1alpha1.ConsolePending:
		res = requeueAfterInterval(r.logger, time.Second)
	case workloadsv1alpha1.ConsoleRunning:
		res = requeueForExpiration(r.logger, r.console.Status)
	}

	// TODO:
	//   GC the terminated console if necessary

	r.logger.Log("event", EventComplete)
	return res, err
}

func (r *reconciler) createOrUpdate(expected reconcile.ObjWithMeta, kind string, diffFunc reconcile.DiffFunc) error {
	if err := controllerutil.SetControllerReference(r.console, expected, scheme.Scheme); err != nil {
		return err
	}

	outcome, err := reconcile.CreateOrUpdate(r.ctx, r.client, expected, kind, diffFunc)
	if err != nil {
		return err
	}

	r.logger.Log("resource", kind, "event", "CreateOrUpdate", "outcome", outcome)
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

func requeueForExpiration(logger kitlog.Logger, status workloadsv1alpha1.ConsoleStatus) k8rec.Result {
	// Requeue after the expiry is hit. Add a second to be on the safe side,
	// ensuring that we'll always re-reconcile *after* the expiry time has been
	// hit (even if the clock drifts), as metav1.Time only has second-resolution.
	requeueTime := status.ExpiryTime.Time.Add(time.Second)
	sleepDuration := requeueTime.Sub(time.Now())

	res := k8rec.Result{}
	res.RequeueAfter = sleepDuration

	logger.Log("event", EventRequeued, "reconcile_at", requeueTime)

	return res
}

func requeueAfterInterval(logger kitlog.Logger, interval time.Duration) k8rec.Result {
	logger.Log("event", EventRequeued, "reconcile_at", interval)
	return k8rec.Result{Requeue: true, RequeueAfter: interval}
}

func buildJob(name types.NamespacedName, csl *workloadsv1alpha1.Console, podTemplate corev1.PodTemplateSpec) *batchv1.Job {
	timeout := int64(csl.Spec.TimeoutSeconds)
	username := strings.SplitN(csl.Spec.User, "@", 2)[0]
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
			Labels:    map[string]string{"user": sanitiseLabel(username)},
		},
		Spec: batchv1.JobSpec{
			Template:              podTemplate,
			ActiveDeadlineSeconds: &timeout,
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

func buildRoleBinding(name types.NamespacedName, role *rbacv1.Role, subjects []rbacv1.Subject) *rbacv1.RoleBinding {
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
func jobDiff(expectedObj runtime.Object, existingObj runtime.Object) reconcile.Outcome {
	expected := expectedObj.(*batchv1.Job)
	existing := existingObj.(*batchv1.Job)
	operation := reconcile.None

	// k8s manages the job's metadata, and doesn't allow us to clobber some of the values
	// it has set (for example, the controller-uid label). To avoid this we only update
	// the pod template spec.
	if !reflect.DeepEqual(expected.Spec.Template.Spec, existing.Spec.Template.Spec) {
		existing.Spec.Template.Spec = expected.Spec.Template.Spec
		operation = reconcile.Update
	}

	if !reflect.DeepEqual(expected.Spec.ActiveDeadlineSeconds, existing.Spec.ActiveDeadlineSeconds) {
		existing.Spec.ActiveDeadlineSeconds = expected.Spec.ActiveDeadlineSeconds
		operation = reconcile.Update
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
