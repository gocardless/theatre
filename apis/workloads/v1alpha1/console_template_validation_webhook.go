package v1alpha1

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:object:generate=false
type ConsoleTemplateValidationWebhook struct {
	logger  logr.Logger
	decoder *admission.Decoder
}

func NewConsoleTemplateValidationWebhook(logger logr.Logger) *ConsoleTemplateValidationWebhook {
	return &ConsoleTemplateValidationWebhook{
		logger: logger,
	}
}

func (c *ConsoleTemplateValidationWebhook) InjectDecoder(d *admission.Decoder) error {
	c.decoder = d
	return nil
}

func (c *ConsoleTemplateValidationWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := c.logger.WithValues(c.logger, "uuid", string(req.UID))
	logger.Info("request starting", "event", "request.start")

	defer func(start time.Time) {
		logger.Info("request completed", "event", "request.end", "duration", time.Now().Sub(start).Seconds())
	}(time.Now())

	template := &ConsoleTemplate{}
	if err := c.decoder.Decode(req, template); err != nil {
		admission.Errored(http.StatusBadRequest, err)
	}

	if err := template.Validate(); err != nil {
		logger.Info("event", "validation.failure")
		return admission.ValidationResponse(false, fmt.Sprintf("the console template spec is invalid: %v", err))
	}

	logger.Info("event", "validation.success")
	return admission.ValidationResponse(true, "")
}
