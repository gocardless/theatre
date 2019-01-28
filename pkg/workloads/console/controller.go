package console

import (
	"context"
	"reflect"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	EventStart    = "reconcile.start"
	EventComplete = "reconcile.end"
	EventRequeued = "reconcile.requeued"
	EventFound    = "found"
	EventNotFound = "not_found"
	EventCreated  = "created"
	EventExpired  = "expired"
	EventDeleted  = "deleted"
	EventError    = "error"

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
		return k8rec.Result{}, err
	}

	// This is temporarily disabled as our logs don't pass k8s event validation
	// logger = logging.WithRecorder(logger, c.recorder, csl)

	reconciler := &ConsoleReconciler{
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

type ConsoleReconciler struct {
	ctx     context.Context
	logger  kitlog.Logger
	client  client.Client
	name    types.NamespacedName
	console *workloadsv1alpha1.Console
}

func (r *ConsoleReconciler) Reconcile() (res k8rec.Result, err error) {
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

	// Try to find the job
	jobExists := false
	job := &batchv1.Job{}
	err = r.client.Get(r.ctx, r.name, job)

	if err != nil {
		if !errors.IsNotFound(err) {
			return res, err
		}
	} else {
		// Clear the 'not found' error, as we don't want this to cause a retry of
		// the reconciliation.
		err = nil
		jobExists = true
	}

	// If it can't be found, create it
	if !jobExists {
		// Only create a job if it hasn't already expired (and therefore been
		// deleted)
		if !isJobExpired(r.console) {
			job = buildJob(r.name, consoleTemplate.Spec.Template)
			if err = r.client.Create(r.ctx, job); err != nil {
				return res, err
			}
			r.logger.Log(
				"event", EventCreated,
				"resource", Job,
				"name", r.name.Name,
				"user", r.console.Spec.User,
			)

		} else {
			r.logger.Log(
				"event", EventExpired,
				"resource", Job,
				"name", r.name.Name,
				"msg", "Not creating new job for expired console",
			)
		}
	} else {
		// The console already exists
		r.logger.Log(
			"event", EventFound,
			"resource", Job,
			"name", r.name.Name,
			"user", r.console.Spec.User,
		)
	}

	// Find or create the role and role bindings
	if err := r.updateRoleBindings(consoleTemplate); err != nil {
		return res, err
	}

	// Update the status fields in case they're out of sync, or the console spec
	// has been updated
	if err = r.updateStatus(job); err != nil {
		return res, err
	}

	// Terminate if necessary
	if jobExists && isJobExpired(r.console) {
		r.logger.Log(
			"event", EventExpired,
			"kind", Job,
			"resource", r.name,
		)

		err = r.client.Delete(
			r.ctx,
			job,
			client.PropagationPolicy(metav1.DeletePropagationBackground),
		)
		if err != nil {
			// If we fail to delete then the reconciliation will be retried
			return res, err
		}
		jobExists = false

		r.logger.Log(
			"event", EventDeleted,
			"kind", Job,
			"resource", r.name,
		)
	}

	if jobExists {
		// Requeue a reconciliation for when we expect the console expiry to fire
		res = requeueForExpiration(r.logger, r.console.Status)
	}

	// TODO:
	//   GC the terminated console if necessary

	r.logger.Log("event", EventComplete)
	return res, err
}

func (r *ConsoleReconciler) updateRoleBindings(tmpl *workloadsv1alpha1.ConsoleTemplate) error {
	role := buildRole(r.name)
	if err := controllerutil.SetControllerReference(r.console, role, scheme.Scheme); err != nil {
		return err
	}
	operation, err := reconcile.CreateOrUpdate(r.ctx, r.client, role, Role, reconcile.RoleDiff)
	if err != nil {
		r.logger.Log("event", EventError, "resource", Role, "error", err)
		return err
	}
	r.logger.Log("event", operation, "resource", Role)

	subjects := append(
		tmpl.Spec.AdditionalAttachSubjects,
		rbacv1.Subject{Kind: "User", Name: r.console.Spec.User},
	)

	rb := buildRoleBinding(r.name, role, subjects)
	if err := controllerutil.SetControllerReference(r.console, rb, scheme.Scheme); err != nil {
		return err
	}
	operation, err = reconcile.CreateOrUpdate(r.ctx, r.client, rb, RoleBinding, reconcile.RoleBindingDiff)
	if err != nil {
		r.logger.Log("event", EventError, "resource", Role, "error", err)
		return err
	}
	r.logger.Log("event", operation, "resource", Role, "subjectcount", len(rb.Subjects))

	return nil
}

func (r *ConsoleReconciler) updateStatus(job *batchv1.Job) error {
	newStatus := calculateStatus(r.console, job)

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

func buildJob(name types.NamespacedName, podTemplate corev1.PodTemplateSpec) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: podTemplate,
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

func requeueForExpiration(logger kitlog.Logger, status workloadsv1alpha1.ConsoleStatus) k8rec.Result {
	// Requeue after the expiry is hit. Add a second to be on the safe side,
	// ensuring that we'll always re-reconcile *after* the expiry time has been
	// hit (even if the clock drifts), as metav1.Time only has second-resolution.
	requeueTime := status.ExpiryTime.Time.Add(time.Second)
	sleepDuration := requeueTime.Sub(time.Now())

	res := k8rec.Result{}
	res.RequeueAfter = sleepDuration

	logger.Log(
		"event", EventRequeued,
		"reconcile_at", requeueTime,
	)

	return res
}

func calculateStatus(csl *workloadsv1alpha1.Console, job *batchv1.Job) workloadsv1alpha1.ConsoleStatus {
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
	}

	return newStatus
}

func isJobExpired(csl *workloadsv1alpha1.Console) bool {
	return csl.Status.ExpiryTime != nil && csl.Status.ExpiryTime.Time.Before(time.Now())
}
