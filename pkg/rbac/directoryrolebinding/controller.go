package directoryrolebinding

import (
	"context"
	"fmt"
	"time"

	kitlog "github.com/go-kit/kit/log"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	rbacv1alpha1 "github.com/gocardless/theatre/pkg/apis/rbac/v1alpha1"
	"github.com/gocardless/theatre/pkg/logging"
	rbacutils "github.com/gocardless/theatre/pkg/rbac"
)

const (
	EventReconcile          = "Reconcile"
	EventNotFound           = "NotFound"
	EventRoleBindingCreated = "Created"
	EventError              = "Error"
	EventSubjectAdd         = "SubjectAdd"
	EventSubjectRemove      = "SubjectRemove"
	EventSubjectsModified   = "SubjectsModified"
)

// Add instantiates a DirectoryRoleBinding controller and adds it to the manager. To
// ensure we respond to changes in the directory source, we provide a refreshInterval
// duration that tells the controller to re-enqueue a reconcile after each successful
// process. Setting this to 0 will disable the re-enqueue.
func Add(ctx context.Context, logger kitlog.Logger, mgr manager.Manager, directory Directory, refreshInterval time.Duration, opts ...func(*controller.Options)) (controller.Controller, error) {
	logger = kitlog.With(logger, "component", "DirectoryRoleBinding")
	ctrlOptions := controller.Options{
		Reconciler: &DirectoryRoleBindingReconciler{
			ctx:      ctx,
			logger:   logger,
			recorder: mgr.GetRecorder("DirectoryRoleBinding"),
			client:   mgr.GetClient(),
			// Cache our directory results for a single refresh period. This should mean we can
			// scale the number of DRBs in the cluster with respect to the number of groups they
			// make use of, which more efficiently makes use of our external directory source.
			directory:       NewCachedDirectory(logger, directory, refreshInterval),
			refreshInterval: refreshInterval,
		},
	}

	for _, opt := range opts {
		opt(&ctrlOptions)
	}

	ctrl, err := controller.New("directoryrolebinding-controller", mgr, ctrlOptions)
	if err != nil {
		return ctrl, err
	}

	err = ctrl.Watch(
		&source.Kind{Type: &rbacv1alpha1.DirectoryRoleBinding{}},
		&handler.EnqueueRequestForObject{},
	)

	if err != nil {
		return nil, err
	}

	err = ctrl.Watch(
		&source.Kind{Type: &rbacv1.RoleBinding{}},
		&handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &rbacv1alpha1.DirectoryRoleBinding{},
		},
	)

	return ctrl, err
}

type DirectoryRoleBindingReconciler struct {
	ctx             context.Context
	logger          kitlog.Logger
	recorder        record.EventRecorder
	client          client.Client
	directory       Directory
	refreshInterval time.Duration
}

// Reconcile achieves the declarative state defined by DirectoryRoleBinding resources.
func (r *DirectoryRoleBindingReconciler) Reconcile(request reconcile.Request) (res reconcile.Result, err error) {
	logger := kitlog.With(r.logger, "request", request)
	logger.Log("event", EventReconcile)

	drb := &rbacv1alpha1.DirectoryRoleBinding{}
	if err := r.client.Get(r.ctx, request.NamespacedName, drb); err != nil {
		if errors.IsNotFound(err) {
			return res, logger.Log("event", EventNotFound)
		}

		logger.Log("event", EventError, "error", err)
		return res, err
	}

	logger = logging.WithRecorder(logger, r.recorder, drb)

	defer func() {
		if err != nil {
			logger.Log("event", EventError, "error", err)
		}
	}()

	rb := &rbacv1.RoleBinding{}
	identifier := types.NamespacedName{Name: drb.Name, Namespace: drb.Namespace}
	err = r.client.Get(r.ctx, identifier, rb)
	if err != nil {
		if !errors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      drb.Name,
				Namespace: drb.Namespace,
			},
			RoleRef:  drb.Spec.RoleRef,
			Subjects: []rbacv1.Subject{},
		}

		if err := controllerutil.SetControllerReference(drb, rb, scheme.Scheme); err != nil {
			return reconcile.Result{}, err
		}

		if err = r.client.Create(r.ctx, rb); err != nil {
			return reconcile.Result{}, err
		}

		logger.Log("event", EventRoleBindingCreated, "msg", fmt.Sprintf(
			"Created RoleBinding: %s", identifier,
		))
	}

	subjects, err := r.resolve(drb.Spec.Subjects)
	if err != nil {
		return reconcile.Result{}, err
	}

	add, remove := rbacutils.Diff(subjects, rb.Subjects), rbacutils.Diff(rb.Subjects, subjects)
	if len(add) > 0 || len(remove) > 0 {
		logger.Log("event", EventSubjectsModified, "add", len(add), "remove", len(remove), "msg", fmt.Sprintf(
			"Modifying subject list, adding %d and removing %d", len(add), len(remove),
		))

		for _, member := range add {
			logging.WithNoRecord(logger).Log("event", EventSubjectAdd, "subject", member.Name)
		}

		for _, member := range remove {
			logging.WithNoRecord(logger).Log("event", EventSubjectRemove, "subject", member.Name)
		}

		rb.Subjects = subjects
		if err := r.client.Update(r.ctx, rb); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{RequeueAfter: r.refreshInterval}, nil
}

func (r *DirectoryRoleBindingReconciler) membersOf(group string) ([]rbacv1.Subject, error) {
	subjects := make([]rbacv1.Subject, 0)
	members, err := r.directory.MembersOf(r.ctx, group)

	if err == nil {
		for _, member := range members {
			subjects = append(subjects, rbacv1.Subject{
				APIGroup: rbacv1.GroupName,
				Kind:     rbacv1.UserKind,
				Name:     member,
			})
		}
	}

	return subjects, err
}

func (r *DirectoryRoleBindingReconciler) resolve(in []rbacv1.Subject) ([]rbacv1.Subject, error) {
	out := make([]rbacv1.Subject, 0)
	for _, subject := range in {
		switch subject.Kind {
		case rbacv1alpha1.GoogleGroupKind:
			members, err := r.membersOf(subject.Name)
			if err != nil {
				return nil, err
			}

			// For each of our group members, add them if they weren't already here
			for _, member := range members {
				if !rbacutils.IncludesSubject(out, member) {
					out = append(out, member)
				}
			}

		default:
			out = append(out, subject)
		}
	}

	return out, nil
}
