package console

import (
	"context"
	"reflect"

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
	// "github.com/gocardless/theatre/pkg/logging"
)

const (
	EventStart    = "ReconcileStart"
	EventComplete = "ReconcileEnd"
	EventFound    = "Found"
	EventNotFound = "NotFound"
	EventCreated  = "Created"
	EventError    = "Error"
	EventRecreate = "Recreated"
	EventUpdate   = "Updated"
	EventNoOp     = "NoOp"

	Job             = "Job"
	Console         = "Console"
	ConsoleTemplate = "ConsoleTemplate"
	Role            = "Role"
	RoleBinding     = "RoleBindings"
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

	// Fetch the console
	csl := &workloadsv1alpha1.Console{}
	err := c.client.Get(c.ctx, request.NamespacedName, csl)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Log("event", EventNotFound, "resource", Console)
		}
		return k8rec.Result{}, err
	}
	logger.Log("event", EventFound, "resource", Console)

	// This is disabled for now because our logs don't pass k8s event validation
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
	r.logger.Log("event", EventFound, "resource", ConsoleTemplate)

	// Find or create the job
	job := r.buildJob(consoleTemplate.Spec.Template)
	if err := r.createOrUpdate(job, "Job", jobDiff); err != nil {
		return res, err
	}

	// Find or create the role
	role := r.buildRole()
	if err := r.createOrUpdate(role, "Role", reconcile.RoleDiff); err != nil {
		return res, err
	}

	// Find or create the role binding
	subjects := append(
		consoleTemplate.Spec.AdditionalAttachSubjects,
		rbacv1.Subject{Kind: "User", Name: r.console.Spec.User},
	)
	rb := r.buildRoleBinding(role, subjects)
	if err := r.createOrUpdate(rb, "RoleBinding", reconcile.RoleBindingDiff); err != nil {
		return res, err
	}

	// TODO:
	//   Terminate the console if it has expired
	//   GC the terminated console if necessary

	r.logger.Log("event", EventComplete)
	return res, err
}

func (r *ConsoleReconciler) buildJob(podTemplate corev1.PodTemplateSpec) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.name.Name,
			Namespace: r.name.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: podTemplate,
		},
	}
}

func (r *ConsoleReconciler) buildRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.name.Name,
			Namespace: r.name.Namespace,
		},
		Rules: []rbacv1.PolicyRule{
			rbacv1.PolicyRule{
				Verbs:         []string{"*"},
				APIGroups:     []string{"core"},
				Resources:     []string{"pods/exec"},
				ResourceNames: []string{r.name.Name},
			},
		},
	}
}

func (r *ConsoleReconciler) buildRoleBinding(role *rbacv1.Role, subjects []rbacv1.Subject) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.name.Name,
			Namespace: r.name.Namespace,
		},
		Subjects: subjects,
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     r.name.Name,
		},
	}
}

func (r *ConsoleReconciler) createOrUpdate(existing reconcile.ObjWithMeta, kind string, diffFunc reconcile.DiffFunc) error {
	if err := controllerutil.SetControllerReference(r.console, existing, scheme.Scheme); err != nil {
		return err
	}

	operation, err := reconcile.CreateOrUpdate(r.ctx, r.client, existing, kind, diffFunc)
	if err != nil {
		return err
	}

	r.logger.Log("event", operation, "resource", kind)
	return nil
}

// jobDiff is a reconcile.DiffFunc for Jobs
func jobDiff(expectedObj runtime.Object, existingObj runtime.Object) reconcile.Operation {
	expected := expectedObj.(*batchv1.Job)
	existing := existingObj.(*batchv1.Job)
	if expected.ObjectMeta.Name != existing.ObjectMeta.Name {
		return reconcile.Recreate
	}

	if !reflect.DeepEqual(expected.Spec.Template, existing.Spec.Template) {
		// We don't update the pod template's metadata, as this has already been modified by
		// k8s and we're not allowed to clobber some of those values.
		existing.Spec.Template.Spec = expected.Spec.Template.Spec

		return reconcile.Update
	}

	return reconcile.None
}
