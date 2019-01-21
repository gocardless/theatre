package console

import (
	"context"
	"net/http"
	"time"

	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"

	kitlog "github.com/go-kit/kit/log"

	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/builder"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/types"

	workloadsv1alpha1 "github.com/lawrencejones/theatre/pkg/apis/workloads/v1alpha1"
)

func NewWebhook(logger kitlog.Logger, mgr manager.Manager, opts ...func(*admission.Handler)) (*admission.Webhook, error) {
	var handler admission.Handler
	handler = &ConsoleAuthenticator{
		logger:  kitlog.With(logger, "component", "ConsoleAuthenticator"),
		decoder: mgr.GetAdmissionDecoder(),
	}

	for _, opt := range opts {
		opt(&handler)
	}

	return builder.NewWebhookBuilder().
		Name("console-authenticator.crd.gocardless.com").
		Mutating().
		Operations(admissionregistrationv1beta1.Create).
		ForType(&workloadsv1alpha1.Console{}).
		Handlers(handler).
		WithManager(mgr).
		Build()
}

type ConsoleAuthenticator struct {
	logger  kitlog.Logger
	decoder types.Decoder
}

func (c *ConsoleAuthenticator) Handle(ctx context.Context, req types.Request) types.Response {
	logger := kitlog.With(c.logger, "uuid", string(req.AdmissionRequest.UID))
	logger.Log("event", "request.start")
	defer func(start time.Time) {
		logger.Log("event", "request.end", "duration", time.Now().Sub(start).Seconds())
	}(time.Now())

	csl := &workloadsv1alpha1.Console{}
	if err := c.decoder.Decode(req, csl); err != nil {
		admission.ErrorResponse(http.StatusBadRequest, err)
	}

	user := req.AdmissionRequest.UserInfo.Username
	copy := csl.DeepCopy()
	copy.Spec.User = user

	logger.Log("event", "authentication.success", "user", user)

	return admission.PatchResponse(csl, copy)
}
