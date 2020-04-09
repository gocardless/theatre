package console

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
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
	multierror "github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"

	rbacutils "github.com/gocardless/theatre/pkg/rbac"
)

func NewAuthorisationWebhook(logger kitlog.Logger, mgr manager.Manager, opts ...func(*admission.Handler)) (*admission.Webhook, error) {
	var handler admission.Handler

	handler = &consoleAuthorisation{
		logger:  kitlog.With(logger, "component", "ConsoleAuthorisation"),
		decoder: serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer(),
	}

	for _, opt := range opts {
		opt(&handler)
	}

	return builder.NewWebhookBuilder().
		Name("console-authorisation.crd.gocardless.com").
		Validating().
		Operations(admissionregistrationv1beta1.Update).
		ForType(&workloadsv1alpha1.ConsoleAuthorisation{}).
		Handlers(handler).
		WithManager(mgr).
		Build()
}

type consoleAuthorisation struct {
	logger  kitlog.Logger
	decoder runtime.Decoder
}

func (c *consoleAuthorisation) Handle(ctx context.Context, req types.Request) types.Response {
	logger := kitlog.With(c.logger, "uuid", string(req.AdmissionRequest.UID))
	logger.Log("event", "request.start")
	defer func(start time.Time) {
		logger.Log("event", "request.end", "duration", time.Now().Sub(start).Seconds())
	}(time.Now())

	// request console authorisation object
	updatedAuth := &workloadsv1alpha1.ConsoleAuthorisation{}
	if err := runtime.DecodeInto(c.decoder, req.AdmissionRequest.Object.Raw, updatedAuth); err != nil {
		admission.ErrorResponse(http.StatusBadRequest, err)
	}

	// existing console authorisation object
	existingAuth := &workloadsv1alpha1.ConsoleAuthorisation{}
	if err := runtime.DecodeInto(c.decoder, req.AdmissionRequest.OldObject.Raw, existingAuth); err != nil {
		admission.ErrorResponse(http.StatusBadRequest, err)
	}

	// user making the request
	user := req.AdmissionRequest.UserInfo.Username

	update := &consoleAuthorisationUpdate{
		existingAuth,
		updatedAuth,
		user,
	}

	if err := update.Validate(); err != nil {
		logger.Log("event", "authorisation.failure")
		return admission.ValidationResponse(false, fmt.Sprintf("the console authorisation spec is invalid: %v", err))
	}

	logger.Log("event", "authorisation.success")
	return admission.ValidationResponse(true, "")
}

type consoleAuthorisationUpdate struct {
	existingAuth *workloadsv1alpha1.ConsoleAuthorisation
	updatedAuth  *workloadsv1alpha1.ConsoleAuthorisation
	user         string
}

func (u *consoleAuthorisationUpdate) Validate() error {
	var err error

	// check immutable fields haven't been updated
	if !reflect.DeepEqual(u.updatedAuth.Spec.ConsoleRef, u.existingAuth.Spec.ConsoleRef) {
		err = multierror.Append(err, errors.New("the spec.consoleRef field is immutable"))
	}
	if u.updatedAuth.Spec.Owner != u.existingAuth.Spec.Owner {
		err = multierror.Append(err, errors.New("the spec.owner field is immutable"))
	}

	// check no existing authorisation subjects have been modified and that a single subject has been added
	add := rbacutils.Diff(u.updatedAuth.Spec.Authorisations, u.existingAuth.Spec.Authorisations)
	remove := rbacutils.Diff(u.existingAuth.Spec.Authorisations, u.updatedAuth.Spec.Authorisations)

	if len(add) != 1 || len(remove) != 0 {
		err = multierror.Append(err, errors.New("the spec.authorisations field can only be appended to (with one subject) per update"))
	}

	// check the user is only adding themselves to the list of authorisers
	for _, s := range add {
		if s.Name != u.user {
			err = multierror.Append(err, errors.New("only the current user can be added as an authoriser"))
			break
		}
	}

	// check the owner of the console isn't adding themselves to the list of authorisers
	for _, s := range add {
		if s.Name == u.existingAuth.Spec.Owner {
			err = multierror.Append(err, errors.New("an authoriser cannot authorise their own console"))
			break
		}
	}

	return err
}
