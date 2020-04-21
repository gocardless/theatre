package console

import (
	"context"
	"fmt"
	"net/http"
	"time"

	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/builder"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/types"

	kitlog "github.com/go-kit/kit/log"
)

func NewTemplateValidationWebhook(logger kitlog.Logger, mgr manager.Manager, opts ...func(*admission.Handler)) (*admission.Webhook, error) {
	var handler admission.Handler

	handler = &templateValidation{
		logger:  kitlog.With(logger, "component", "TemplateValidationWebhook"),
		decoder: serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer(),
	}

	for _, opt := range opts {
		opt(&handler)
	}

	return builder.NewWebhookBuilder().
		Name("console-template-validation.crd.gocardless.com").
		Validating().
		Operations(admissionregistrationv1beta1.Create, admissionregistrationv1beta1.Update).
		ForType(&workloadsv1alpha1.ConsoleTemplate{}).
		Handlers(handler).
		WithManager(mgr).
		Build()
}

type templateValidation struct {
	logger  kitlog.Logger
	decoder runtime.Decoder
}

func (c *templateValidation) Handle(ctx context.Context, req types.Request) types.Response {
	logger := kitlog.With(c.logger, "uuid", string(req.AdmissionRequest.UID))
	logger.Log("event", "request.start")

	defer func(start time.Time) {
		logger.Log("event", "request.end", "duration", time.Now().Sub(start).Seconds())
	}(time.Now())

	template := &workloadsv1alpha1.ConsoleTemplate{}
	if err := runtime.DecodeInto(c.decoder, req.AdmissionRequest.Object.Raw, template); err != nil {
		admission.ErrorResponse(http.StatusBadRequest, err)
	}

	if err := template.Validate(); err != nil {
		logger.Log("event", "validation.failure")
		return admission.ValidationResponse(false, fmt.Sprintf("the console template spec is invalid: %v", err))
	}

	logger.Log("event", "validation.success")
	return admission.ValidationResponse(true, "")
}
