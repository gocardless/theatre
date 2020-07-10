package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:object:generate=false
type ConsoleAuthenticatorWebhook struct {
	logger  logr.Logger
	decoder *admission.Decoder
}

func NewConsoleAuthenticatorWebhook(logger logr.Logger) *ConsoleAuthenticatorWebhook {
	return &ConsoleAuthenticatorWebhook{
		logger: logger,
	}
}

func (c *ConsoleAuthenticatorWebhook) InjectDecoder(d *admission.Decoder) error {
	c.decoder = d
	return nil
}

func (c *ConsoleAuthenticatorWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := c.logger.WithValues(c.logger, "uuid", string(req.UID))
	logger.Info("starting request", "event", "request.start")
	defer func(start time.Time) {
		logger.Info("completed request", "event", "request.end", "duration", time.Now().Sub(start).Seconds())
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
