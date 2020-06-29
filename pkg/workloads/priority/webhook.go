package priority

import (
	"context"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kitlog "github.com/go-kit/kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	PriorityInjectorFQDN = "priority-injector.workloads.crd.gocardless.com"
	NamespaceLabel       = "theatre-priority-injector"
)

func NewWebhook(logger kitlog.Logger, mgr manager.Manager, injectorOpts InjectorOptions, opts ...func(*admission.Handler)) (*admission.Webhook, error) {
	var handler admission.Handler
	handler = &injector{
		logger: kitlog.With(logger, "component", "PriorityInjector"),
		// decoder: mgr.GetDecoder(),
		client: mgr.GetClient(),
		opts:   injectorOpts,
	}

	for _, opt := range opts {
		opt(&handler)
	}

	_ = &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			metav1.LabelSelectorRequirement{
				Key:      NamespaceLabel,
				Operator: metav1.LabelSelectorOpExists,
			},
		},
	}

	return nil, nil

	// return builder.NewWebhookBuilder().
	// 	Name(PriorityInjectorFQDN).
	// 	Mutating().
	// 	Operations(admissionregistrationv1beta1.Create).
	// 	ForType(&corev1.Pod{}).
	// 	FailurePolicy(admissionregistrationv1beta1.Fail).
	// 	NamespaceSelector(namespaceSelectors).
	// 	Handlers(handler).
	// 	WithManager(mgr).
	// 	Build()
}

type injector struct {
	logger  kitlog.Logger
	decoder admission.Decoder
	client  client.Client
	opts    InjectorOptions
}

type InjectorOptions struct{}

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

func (i *injector) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	labels := prometheus.Labels{"pod_namespace": req.Namespace}
	logger := kitlog.With(i.logger, "uuid", string(req.UID))
	logger.Log("event", "request.start")
	defer func(start time.Time) {
		logger.Log("event", "request.end", "duration", time.Since(start).Seconds())

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
		logger.Log("event", "pod.skipped", "msg", "no priority label found")
		skipTotal.With(labels).Inc()
		return admission.Patched("no priority label found")
	}

	mutateTotal.With(labels).Inc() // we are committed to mutating this pod now

	logger.Log("event", "pod.assign_priority_class", "class", priorityClassName)
	copy := pod.DeepCopy()
	copy.Spec.PriorityClassName = priorityClassName
	copy.Spec.Priority = nil

	return admission.Patched("TODO")
	// return admission.Patched(pod, copy)
}
