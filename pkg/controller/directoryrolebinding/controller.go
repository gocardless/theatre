package directoryrolebinding

import (
	"context"
	"fmt"
	stdlog "log"
	"time"

	"github.com/davecgh/go-spew/spew"
	kitlog "github.com/go-kit/kit/log"
	admin "google.golang.org/api/admin/directory/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	rbacinformers "k8s.io/client-go/informers/rbac/v1"
	"k8s.io/client-go/kubernetes"
	kubescheme "k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	rbaclisters "k8s.io/client-go/listers/rbac/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/staging/src/k8s.io/client-go/util/workqueue"

	// We import our own clients here, without providing any prefix. Everything we pull in
	// from the Kubernetes stdlib we will prefix.
	rbac "github.com/lawrencejones/theatre/pkg/apis/rbac"
	clientset "github.com/lawrencejones/theatre/pkg/client/clientset/versioned"
	scheme "github.com/lawrencejones/theatre/pkg/client/clientset/versioned/scheme"
	informers "github.com/lawrencejones/theatre/pkg/client/informers/externalversions/rbac/v1alpha1"
	listers "github.com/lawrencejones/theatre/pkg/client/listers/rbac/v1alpha1"
)

const controllerAgentName = "rbac-controller"

type Controller struct {
	logger     kitlog.Logger
	client     *admin.Service
	kubeClient kubernetes.Interface
	rbacClient clientset.Interface

	// RoleBinding informer fields
	rbsLister rbaclisters.RoleBindingLister
	rbsSynced cache.InformerSynced

	// DirectoryRoleBinding informer fields
	drbsLister listers.DirectoryRoleBindingLister
	drbsSynced cache.InformerSynced

	// workqueue is a rate limited work queue, used to ensure we can process a steady stream
	// of work and that we're never processing events for the same resource simultaneously.
	workqueue workqueue.RateLimitingInterface

	// recorder allows us to submit events to the Kubernetes API
	recorder record.EventRecorder
}

func NewController(
	ctx context.Context,
	logger kitlog.Logger,
	client *admin.Service,
	kubeClient kubernetes.Interface,
	rbInformer rbacinformers.RoleBindingInformer,
	rbacClient clientset.Interface,
	drbInformer informers.DirectoryRoleBindingInformer,
) *Controller {
	// Add rbac types to the default Kubernetes Scheme so Events can be logged for the rbac
	// types.
	utilruntime.Must(kubescheme.AddToScheme(scheme.Scheme))

	logger.Log("event", "broadcaster.create")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(stdlog.Printf)
	eventBroadcaster.StartRecordingToSink(
		&typedcorev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")},
	)

	recorder := eventBroadcaster.NewRecorder(
		kubescheme.Scheme, corev1.EventSource{Component: controllerAgentName},
	)

	ctrl := &Controller{
		logger:     logger,
		client:     client,
		kubeClient: kubeClient,
		rbacClient: rbacClient,
		// RoleBindings listeners
		rbsLister: rbInformer.Lister(),
		rbsSynced: rbInformer.Informer().HasSynced,
		// DirectoryRoleBindings listeners
		drbsLister: drbInformer.Lister(),
		drbsSynced: drbInformer.Informer().HasSynced,
		workqueue: workqueue.NewNamedRateLimitingQueue(
			workqueue.DefaultControllerRateLimiter(), "DirectoryRoleBindings",
		),
		recorder: recorder,
	}

	logger.Log("event", "handlers.configure", "resource", "DirectoryRoleBinding")
	drbInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: ctrl.enqueueDirectoryRoleBinding,
			UpdateFunc: func(old, new interface{}) {
				ctrl.enqueueDirectoryRoleBinding(new)
			},
		},
	)

	logger.Log("event", "handlers.configure", "resource", "RoleBinding")
	rbInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: ctrl.handleObject,
			UpdateFunc: func(old, new interface{}) {
				ctrl.handleObject(new)
			},
			DeleteFunc: ctrl.handleObject,
		},
	)

	return ctrl
}

// Run the control workloop until the context expires, using `threads` number of workers.
func (c *Controller) Run(ctx context.Context, threads int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	c.logger.Log("event", "controller.start")
	c.logger.Log("event", "cache.sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.drbsSynced); !ok {
		return fmt.Errorf("failed to wait for cached to sync")
	}

	c.logger.Log("event", "workers.start", "count", threads)
	for idx := 0; idx < threads; idx++ {
		go wait.Until(c.runWorker, time.Second, ctx.Done())
	}

	<-ctx.Done()

	return nil
}

// runWorker continually processes items from the workqueue until told to shutdown
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}

	logger := kitlog.With(c.logger, "obj", obj)
	logger.Log("event", "workqueue.process")

	err := func(obj interface{}) error {
		defer c.workqueue.Done(obj)

		key, ok := obj.(string)
		if !ok {
			// This item is invalid, don't process it again
			c.workqueue.Forget(obj)
			return fmt.Errorf("expected string in workqueue")
		}

		// Sync the object represented by our key, where key is <namespace>/<name>
		if err := c.syncHandler(key); err != nil {
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, re-queuing", key, err.Error())
		}

		c.workqueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		logger.Log("event", "workqueue.error", "error", err)
	} else {
		logger.Log("event", "workqueue.success")
	}

	return true
}

// syncHandler performs our reconciliation process for the given resource
func (c *Controller) syncHandler(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		c.logger.Log("event", "sync.error", "error", "invalid namespace/name key")
	}

	logger := kitlog.With(c.logger, "namespace", namespace, "name", name)
	logger.Log("event", "sync.start")

	drb, err := c.drbsLister.DirectoryRoleBindings(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Log("event", "sync.error", "error", "item no longer exists")
			return nil
		}

		return err
	}

	var subjects = make([]rbacv1.Subject, 0)
	for _, subject := range drb.Subjects {
		if subject.APIGroup != rbac.GroupName {
			subjects = append(subjects, subject)
			continue
		}

		switch subject.Kind {
		case "GoogleGroup":
			resp, err := c.client.Members.List(subject.Name).Do()
			if err != nil {
				logger.Log("event", "sync.error", "error", err.Error())
				break
			}

			for _, member := range resp.Members {
				subjects = append(subjects, rbacv1.Subject{
					APIGroup: rbacv1.GroupName,
					Kind:     rbacv1.UserKind,
					Name:     member.Email,
				})
			}
		default:
			logger.Log("event", "sync.error", "kind", subject.Kind, "error", "unrecognised kind")
		}
	}

	// TODO
	spew.Dump(drb)
	spew.Dump(subjects)

	return nil
}

func (c *Controller) handleObject(obj interface{}) {
	logger := kitlog.With(c.logger, "obj", obj)
	object, ok := obj.(metav1.Object)
	if !ok {
		logger.Log("event", "handle.error", "error", "failed to decode object, invalid type")
		return
	}

	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		if ownerRef.Kind != "DirectoryRoleBinding" {
			logger.Log("event", "handler.unrelated", "msg", "object is not handled by this controller")
			return
		}

		drb, err := c.drbsLister.DirectoryRoleBindings(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			logger.Log("event", "handler.orphaned", "msg", "ignoring orphaned object")
			return
		}

		logger.Log("event", "handler.enqueuing", "selflink", drb.GetSelfLink())
		c.enqueueDirectoryRoleBinding(drb)
	}
}

// enqueueDirectoryRoleBinding receives a DirectoryRoleBinding resource and notifies the
// workqueue using the appropriate cache key.
func (c *Controller) enqueueDirectoryRoleBinding(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		c.logger.Log("event", "enqueue.error", "error", err.Error())
		return
	}

	c.workqueue.AddRateLimited(key)
}
