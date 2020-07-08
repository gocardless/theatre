//
package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	PriorityInjectorFQDN = "priority-injector.workloads.crd.gocardless.com"
	NamespaceLabel       = "theatre-priority-injector"
)

// +kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,failurePolicy=fail,groups="",resources=pods,verbs=create,versions=v1,name=priority-injector.workloads.crd.gocardless.com
// +kubebuilder:object:generate=false
type PriorityInjector struct {
	client  client.Client
	log     logr.Logger
	decoder *admission.Decoder
}

func NewPriorityInjector(c client.Client, logger logr.Logger) *PriorityInjector {
	return &PriorityInjector{
		client: c,
		log:    logger,
	}
}

var (
	podLabels   = []string{"pod_namespace"}
	handleTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_workloads_priority_injector_handle_total",
			Help: "Count of requests handled by the webhook",
		},
		podLabels,
	)
	mutateTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_workloads_priority_injector_mutate_total",
			Help: "Count of pods mutated by the webhook",
		},
		podLabels,
	)
	skipTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_workloads_priority_injector_skip_total",
			Help: "Count of pods skipped by the webhook",
		},
		podLabels,
	)
	errorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_workloads_priority_injector_errors_total",
			Help: "Count of not-allowed responses from webhook",
		},
		podLabels,
	)
)

func (i *PriorityInjector) InjectDecoder(d *admission.Decoder) error {
	i.decoder = d
	return nil
}

func (i *PriorityInjector) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	labels := prometheus.Labels{"pod_namespace": req.Namespace}
	logger := i.log.WithValues(
		"component", "PriorityInjector",
		"uuid", string(req.UID),
	)
	logger.Info("starting request", "event", "request.start")
	defer func(start time.Time) {
		logger.Info("completed request", "event", "request.end", "duration", time.Since(start).Seconds())

		handleTotal.With(labels).Inc()
		{ // add 0 to initialise the metrics
			mutateTotal.With(labels).Add(0)
			skipTotal.With(labels).Add(0)
			errorsTotal.With(labels).Add(0)
		}

		// Catch any Allowed=false responses, as this means we've failed to accept this pod
		if !resp.Allowed {
			errorsTotal.With(labels).Inc()
		}
	}(time.Now())

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: req.Namespace,
		},
	}
	nsName, _ := client.ObjectKeyFromObject(ns)
	if err := i.client.Get(ctx, nsName, ns); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	pod := &corev1.Pod{}
	if err := i.decoder.Decode(req, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	priorityClassName, ok := ns.ObjectMeta.Labels[NamespaceLabel]
	if !ok {
		logger.Info("skipping pod without priority label", "event", "pod.skipped", "msg", "no priority label found")
		skipTotal.With(labels).Inc()
		return admission.Allowed("no priority label found")
	}

	mutateTotal.With(labels).Inc() // we are committed to mutating this pod now

	logger.Info(fmt.Sprintf("pod assigned priority class %s", priorityClassName), "event", "pod.assign_priority_class", "class", priorityClassName)
	copy := pod.DeepCopy()
	copy.Spec.PriorityClassName = priorityClassName
	copy.Spec.Priority = nil

	// TODO(jackatbancast): convert using JSON patch operations
	marshaledPod, err := json.Marshal(copy)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}
