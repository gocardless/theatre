package console

import (
	"context"
	"net/http"
	"time"

	kitlog "github.com/go-kit/kit/log"

	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
)

func NewAuthenticatorWebhook(logger kitlog.Logger, mgr manager.Manager, opts ...func(*admission.Handler)) (*admission.Webhook, error) {
	var handler admission.Handler
	handler = &consoleAuthenticator{
		logger: kitlog.With(logger, "component", "ConsoleAuthenticatorWebhook"),
		// decoder: mgr.GetAdmissionDecoder(),
	}

	for _, opt := range opts {
		opt(&handler)
	}

	return nil, nil

	// return builder.NewWebhookBuilder().
	// 	Name("console-authenticator.crd.gocardless.com").
	// 	Mutating().
	// 	Operations(admissionregistrationv1beta1.Create).
	// 	ForType(&workloadsv1alpha1.Console{}).
	// 	Handlers(handler).
	// 	WithManager(mgr).
	// 	Build()
}

type consoleAuthenticator struct {
	logger  kitlog.Logger
	decoder admission.Decoder
}

func (c *consoleAuthenticator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := kitlog.With(c.logger, "uuid", string(req.UID))
	logger.Log("event", "request.start")
	defer func(start time.Time) {
		logger.Log("event", "request.end", "duration", time.Now().Sub(start).Seconds())
	}(time.Now())

	csl := &workloadsv1alpha1.Console{}
	if err := c.decoder.Decode(req, csl); err != nil {
		admission.Errored(http.StatusBadRequest, err)
	}

	user := req.UserInfo.Username
	copy := csl.DeepCopy()
	copy.Spec.User = user

	logger.Log("event", "authentication.success", "user", user)

	return admission.Patched("TODO")
	// return admission.PatchResponse(csl, copy)
}
