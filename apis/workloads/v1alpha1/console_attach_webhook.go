package v1alpha1

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/gocardless/theatre/v4/pkg/logging"
)

// +kubebuilder:object:generate=false
type ConsoleAttachObserverWebhook struct {
	client            client.Client
	recorder          record.EventRecorder
	lifecycleRecorder LifecycleEventRecorder
	logger            logr.Logger
	decoder           *admission.Decoder
	requestTimeout    time.Duration
}

func NewConsoleAttachObserverWebhook(c client.Client, recorder record.EventRecorder, lifecycleRecorder LifecycleEventRecorder, logger logr.Logger, requestTimeout time.Duration, scheme *runtime.Scheme) *ConsoleAttachObserverWebhook {
	return &ConsoleAttachObserverWebhook{
		client:            c,
		recorder:          recorder,
		lifecycleRecorder: lifecycleRecorder,
		logger:            logger,
		requestTimeout:    requestTimeout,
		decoder:           admission.NewDecoder(scheme),
	}
}

func (c *ConsoleAttachObserverWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := c.logger.WithValues(
		"uuid", string(req.UID),
		"pod", req.Name,
		"namespace", req.Namespace,
		"user", req.UserInfo.Username,
	)
	logger.Info("starting request", "event", "request.start")
	defer func(start time.Time) {
		logging.WithNoRecord(logger).Info("completed request", "event", "request.end", "duration", time.Since(start).Seconds())
	}(time.Now())

	attachOptions := &corev1.PodAttachOptions{}
	if err := c.decoder.Decode(req, attachOptions); err != nil {
		logger.Error(err, "failed to decode attach options")
		return admission.Errored(http.StatusBadRequest, err)
	}

	rctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	// Get the associated pod. This will have the same name, and
	// exist in the same namespace as the pod attach options we
	// receive in the request.
	pod := &corev1.Pod{}
	if err := c.client.Get(rctx, client.ObjectKey{
		Namespace: req.Namespace,
		Name:      req.Name,
	}, pod); err != nil {
		logger.Error(err, "failed to get pod")
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Skip the rest of our lookups if we're not in a console.  We
	// can determine this by looking for a "console-name" in the
	// pod labels.
	if _, ok := pod.Labels["console-name"]; !ok {
		return admission.Allowed("not a console; skipping observation")
	}

	rctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	// Get the console associated to publish events
	csl := &Console{}
	if err := c.client.Get(rctx, client.ObjectKey{
		Namespace: req.Namespace,
		Name:      pod.Labels["console-name"],
	}, csl); err != nil {
		logger.Error(
			err, "failed to get console",
			"console", pod.Labels["console-name"],
		)
		return admission.Errored(http.StatusInternalServerError, err)
	}

	logger.WithValues(
		"pod", pod.Name,
		"namespace", pod.Namespace,
		"user", req.UserInfo.Username,
		"console", csl.Name,
		"event", "console.attach",
	)

	// If performing a dry-run we only want to log the attachment.
	if *req.DryRun {
		// Log an event observing the attachment
		logger.Info(
			fmt.Sprintf(
				"observed dry-run attach for pod %s/%s by user %s",
				pod.Namespace, pod.Name, req.UserInfo.Username,
			),
			"console", csl.Name,
			"dry-run", true,
		)
		return admission.Allowed("dry-run set; skipping attachment observation")
	}

	// Attach an event recorder to the logger, based on the
	// associated pod
	logger = logging.WithEventRecorder(logger.GetSink(), c.recorder, pod)

	// Log an event observing the attachment
	logger.Info(
		fmt.Sprintf(
			"observed attach to pod %s/%s by user %s",
			pod.Namespace, pod.Name, req.UserInfo.Username,
		),
		"event", "ConsoleAttach",
	)
	err := c.lifecycleRecorder.ConsoleAttach(ctx, csl, req.UserInfo.Username, attachOptions.Container)
	if err != nil {
		logging.WithNoRecord(logger).Error(err, "failed to record event")
	}

	return admission.Allowed("attachment observed")
}
