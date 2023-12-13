package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:object:generate=false
type ConsoleAuthenticatorWebhook struct {
	lifecycleRecorder LifecycleEventRecorder
	logger            logr.Logger
	decoder           *admission.Decoder
}

func NewConsoleAuthenticatorWebhook(lifecycleRecorder LifecycleEventRecorder, logger logr.Logger, scheme *runtime.Scheme) *ConsoleAuthenticatorWebhook {
	return &ConsoleAuthenticatorWebhook{
		lifecycleRecorder: lifecycleRecorder,
		logger:            logger,
		decoder:           admission.NewDecoder(scheme),
	}
}

func (c *ConsoleAuthenticatorWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := c.logger.WithValues("uuid", string(req.UID))
	logger.Info("starting request", "event", "request.start")
	defer func(start time.Time) {
		logger.Info("completed request", "event", "request.end", "duration", time.Since(start).Seconds())
	}(time.Now())

	csl := &Console{}
	if err := c.decoder.Decode(req, csl); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	user := req.UserInfo.Username
	copy := csl.DeepCopy()
	copy.Spec.User = user

	copyBytes, err := json.Marshal(copy)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	logger.Info(fmt.Sprintf("authentication successful for user %s", user), "event", "authentication.success", "user", user)

	return admission.PatchResponseFromRaw(req.Object.Raw, copyBytes)
}
