package v1alpha1

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	runtime "k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:object:generate=false
type ConsoleTemplateValidationWebhook struct {
	logger  logr.Logger
	decoder *admission.Decoder
}

func NewConsoleTemplateValidationWebhook(logger logr.Logger, scheme *runtime.Scheme) *ConsoleTemplateValidationWebhook {
	return &ConsoleTemplateValidationWebhook{
		logger:  logger,
		decoder: admission.NewDecoder(scheme),
	}
}

func (c *ConsoleTemplateValidationWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := c.logger.WithValues("uuid", string(req.UID))
	logger.Info("starting request", "event", "request.start")

	defer func(start time.Time) {
		logger.Info("request completed", "event", "request.end", "duration", time.Since(start).Seconds())
	}(time.Now())

	template := &ConsoleTemplate{}
	if err := c.decoder.Decode(req, template); err != nil {
		admission.Errored(http.StatusBadRequest, err)
	}

	if err := template.Validate(); err != nil {
		logger.Info("validation failure", "event", "validation.failure")
		return admission.ValidationResponse(false, fmt.Sprintf("the console template spec is invalid: %v", err))
	}

	logger.Info("completed validation", "event", "validation.success")
	return admission.ValidationResponse(true, "")
}
