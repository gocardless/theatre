package console

import (
	"context"

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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/pkg/client/clientset/versioned/scheme"
	"github.com/gocardless/theatre/pkg/logging"
)

const (
	EventStart    = "reconcile.start"
	EventComplete = "reconcile.end"
	EventFound    = "found"
	EventNotFound = "not_found"
	EventCreated  = "created"
	EventError    = "error"

	Job             = "Job"
	Console         = "Console"
	ConsoleTemplate = "ConsoleTemplate"
	Role            = "Role"
	RoleBinding     = "RoleBindings"
)

func Add(ctx context.Context, logger kitlog.Logger, mgr manager.Manager, opts ...func(*controller.Options)) (controller.Controller, error) {
	logger = kitlog.With(logger, "component", "Console")
	ctrlOptions := controller.Options{
		Reconciler: &ConsoleReconciler{
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

type ConsoleReconciler struct {
	ctx      context.Context
	logger   kitlog.Logger
	recorder record.EventRecorder
	client   client.Client
}

func (r *ConsoleReconciler) Reconcile(request reconcile.Request) (res reconcile.Result, err error) {
	logger := kitlog.With(r.logger, "request", request)
	logger.Log("event", EventStart)

	defer func() {
		if err != nil {
			logger.Log("event", EventError, "error", err)
		}
	}()

	name := request.NamespacedName
	csl := &workloadsv1alpha1.Console{}
	if err := r.client.Get(r.ctx, name, csl); err != nil {
		if errors.IsNotFound(err) {
			return res, logger.Log("event", EventNotFound, "resource", Console)
		}

		return res, err
	}

	logger = logging.WithRecorder(logger, r.recorder, csl)

	// Fetch the console template
	consoleTemplateName := types.NamespacedName{
		Name:      csl.Spec.ConsoleTemplateRef.Name,
		Namespace: name.Namespace,
	}
	consoleTemplate := &workloadsv1alpha1.ConsoleTemplate{}
	if err := r.client.Get(r.ctx, consoleTemplateName, consoleTemplate); err != nil {
		if errors.IsNotFound(err) {
			logger.Log("event", EventNotFound, "resource", ConsoleTemplate)
		}
		return res, err
	}

	// Try to find the job
	job := &batchv1.Job{}
	err = r.client.Get(r.ctx, name, job)

	// If it can't be found, create it
	if err != nil {
		if !errors.IsNotFound(err) {
			return res, err
		}

		logger.Log("event", EventNotFound, "resource", Job)
		job = buildJob(name, consoleTemplate.Spec.Template)
		if err := controllerutil.SetControllerReference(csl, job, scheme.Scheme); err != nil {
			return res, err
		}
		if err = r.client.Create(r.ctx, job); err != nil {
			return res, err
		}
		logger.Log(
			"event", EventCreated,
			"resource", Job,
			"name", job.ObjectMeta.Name,
			"user", csl.Spec.User,
		)
	}

	logger.Log(
		"event", EventFound,
		"resource", Job,
		"name", name.Name,
		"user", csl.Spec.User,
	)

	// Find or create the role and role bindings
	if err := r.updateRoleBindings(csl, consoleTemplate, name); err != nil {
		return res, err
	}

	// TODO:
	//   Terminate the console if it has expired
	//   GC the terminated console if necessary

	logger.Log("event", EventComplete)
	return res, err
}

func (r *ConsoleReconciler) updateRoleBindings(csl *workloadsv1alpha1.Console, tmpl *workloadsv1alpha1.ConsoleTemplate, name types.NamespacedName) error {
	role := buildRole(name)
	if err := controllerutil.SetControllerReference(csl, role, scheme.Scheme); err != nil {
		return err
	}
	if err := findOrCreate(r.ctx, r.client, role, name); err != nil {
		r.logger.Log("event", EventError, "resource", Role, "error", err)
		return err
	}
	r.logger.Log("event", EventCreated, "resource", Role)

	subjects := append(
		tmpl.Spec.AdditionalAttachSubjects,
		rbacv1.Subject{Kind: "User", Name: csl.Spec.User},
	)

	rb := buildRoleBinding(name, role, subjects)
	if err := controllerutil.SetControllerReference(csl, rb, scheme.Scheme); err != nil {
		return err
	}
	if err := findOrCreate(r.ctx, r.client, rb, name); err != nil {
		return err
	}
	r.logger.Log("event", EventCreated, "resource", RoleBinding, "subjectcount", len(rb.Subjects))

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

func findOrCreate(ctx context.Context, c client.Client, obj runtime.Object, name types.NamespacedName) error {
	key := client.ObjectKey{
		Name:      name.Name,
		Namespace: name.Namespace,
	}
	err := c.Get(ctx, key, obj)
	if errors.IsNotFound(err) {
		err = c.Create(ctx, obj)
	}
	return err
}
