package directoryrolebinding

import (
	"context"
	"fmt"
	stdlog "log"
	"time"

	kitlog "github.com/go-kit/kit/log"
	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/staging/src/k8s.io/client-go/util/workqueue"

	clientset "github.com/lawrencejones/rbac-directory/pkg/client/clientset/versioned"
	rbacscheme "github.com/lawrencejones/rbac-directory/pkg/client/clientset/versioned/scheme"
	rbacinformers "github.com/lawrencejones/rbac-directory/pkg/client/informers/externalversions/rbac/v1alpha1"
	listers "github.com/lawrencejones/rbac-directory/pkg/client/listers/rbac/v1alpha1"
)

const controllerAgentName = "rbac-controller"

type Controller struct {
	logger        kitlog.Logger
	kubeclientset kubernetes.Interface
	clientset     clientset.Interface

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
	ctx context.Context, logger kitlog.Logger,
	kubeclientset kubernetes.Interface, clientset clientset.Interface,
	drbInformer rbacinformers.DirectoryRoleBindingInformer,
) *Controller {
	// Add rbac types to the default Kubernetes Scheme so Events can be logged for the rbac
	// types.
	utilruntime.Must(rbacscheme.AddToScheme(scheme.Scheme))

	logger.Log("event", "broadcaster.create")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(stdlog.Printf)
	eventBroadcaster.StartRecordingToSink(
		&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")},
	)

	recorder := eventBroadcaster.NewRecorder(
		scheme.Scheme, corev1.EventSource{Component: controllerAgentName},
	)

	ctrl := &Controller{
		logger:        logger,
		kubeclientset: kubeclientset,
		clientset:     clientset,
		drbsLister:    drbInformer.Lister(),
		drbsSynced:    drbInformer.Informer().HasSynced,
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

	return ctrl
}

func (c *Controller) Run(ctx context.Context, threadiness int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	c.logger.Log("event", "controller.start")
	c.logger.Log("event", "cache.sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.drbsSynced); !ok {
		return fmt.Errorf("failed to wait for cached to sync")
	}

	for idx := 0; idx < threadiness; idx++ {
		c.logger.Log("event", "workers.start", "index", idx)
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
	return nil
}

// enqueueDirectoryRoleBinding receives a DirectoryRoleBinding resource and notifies the
// workqueue using the appropriate cache key.
func (c *Controller) enqueueDirectoryRoleBinding(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}

	c.workqueue.AddRateLimited(key)
}
