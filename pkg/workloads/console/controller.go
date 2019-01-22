package console

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	kitlog "github.com/go-kit/kit/log"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
)

const (
	EventReconcile = "Reconcile"
	EventNotFound  = "NotFound"
	EventCreated   = "Created"
	EventError     = "Error"
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
	logger.Log("event", EventReconcile)

	defer func() {
		if err != nil {
			logger.Log("event", EventError, "error", err)
		}
	}()

	csl := &workloadsv1alpha1.Console{}
	if err := r.client.Get(r.ctx, request.NamespacedName, csl); err != nil {
		if errors.IsNotFound(err) {
			return res, logger.Log("event", EventNotFound)
		}

		return res, err
	}

	pod := &corev1.Pod{}
	err = r.client.Get(r.ctx, request.NamespacedName, pod)

	if err != nil {
		if errors.IsNotFound(err) {
			err = r.client.Create(r.ctx, newPod(request.NamespacedName))
			if err != nil {
				return res, err
			}
			logger.Log("pod", "created", "name", request.NamespacedName.Name, "user", csl.Spec.User)
		}

		return res, err
	}

	logger.Log("pod", "already exists", "name", request.NamespacedName.Name, "user", csl.Spec.User)
	logger.Log("event", "Reconciled")
	return res, err
}

func newPod(name types.NamespacedName) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				corev1.Container{
					Image:   "alpine:latest",
					Name:    "console-container-0",
					Command: []string{"sleep", "100"},
				},
			},
		},
	}
}
