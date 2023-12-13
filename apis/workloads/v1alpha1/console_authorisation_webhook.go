package v1alpha1

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/gocardless/theatre/v4/pkg/logging"
	rbacutils "github.com/gocardless/theatre/v4/pkg/rbac"
)

// +kubebuilder:object:generate=false
type ConsoleAuthorisationWebhook struct {
	client            client.Client
	lifecycleRecorder LifecycleEventRecorder
	logger            logr.Logger
	decoder           *admission.Decoder
}

func NewConsoleAuthorisationWebhook(c client.Client, lifecycleRecorder LifecycleEventRecorder, logger logr.Logger, scheme *runtime.Scheme) *ConsoleAuthorisationWebhook {
	return &ConsoleAuthorisationWebhook{
		client:            c,
		lifecycleRecorder: lifecycleRecorder,
		logger:            logger,
		decoder:           admission.NewDecoder(scheme),
	}
}

func (c *ConsoleAuthorisationWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := c.logger.WithValues("uuid", string(req.UID))
	logger.Info("starting request", "event", "request.start")
	defer func(start time.Time) {
		logger.Info("completed request", "event", "request.end", "duration", time.Since(start).Seconds())
	}(time.Now())

	// request console authorisation object
	updatedAuth := &ConsoleAuthorisation{}
	if err := c.decoder.DecodeRaw(req.Object, updatedAuth); err != nil {
		admission.Errored(http.StatusBadRequest, err)
	}

	// existing console authorisation object
	existingAuth := &ConsoleAuthorisation{}
	if err := c.decoder.DecodeRaw(req.OldObject, existingAuth); err != nil {
		admission.Errored(http.StatusBadRequest, err)
	}

	// user making the request
	user := req.AdmissionRequest.UserInfo.Username

	csl, err := c.getConsole(ctx, existingAuth.Spec.ConsoleRef.Name, existingAuth.Namespace)
	if err != nil {
		return admission.ValidationResponse(false, fmt.Sprintf("failed to retrieve console for the authorisation: %v", err))
	}

	update := &ConsoleAuthorisationUpdate{
		existingAuth: existingAuth,
		updatedAuth:  updatedAuth,
		user:         user,
		owner:        csl.Spec.User,
	}

	if err := update.Validate(); err != nil {
		logger.Info("authorisation failed", "event", "authorisation.failure", "error", err)
		return admission.ValidationResponse(false, fmt.Sprintf("the console authorisation spec is invalid: %v", err))
	}

	logger.Info("authorisation successful", "event", "authorisation.success")
	err = c.lifecycleRecorder.ConsoleAuthorise(ctx, csl, user)
	if err != nil {
		logging.WithNoRecord(logger).Error(err, "failed to record event", "event", "console.authorise")
	}

	return admission.ValidationResponse(true, "")
}

func (c *ConsoleAuthorisationWebhook) getConsole(ctx context.Context, name, namespace string) (*Console, error) {
	namespacedName := client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}

	csl := &Console{}

	return csl, c.client.Get(ctx, namespacedName, csl)
}

type ConsoleAuthorisationUpdate struct {
	existingAuth *ConsoleAuthorisation
	updatedAuth  *ConsoleAuthorisation
	user         string
	owner        string
}

func (u *ConsoleAuthorisationUpdate) Validate() error {
	var err error

	// check immutable fields haven't been updated
	if !reflect.DeepEqual(u.updatedAuth.Spec.ConsoleRef, u.existingAuth.Spec.ConsoleRef) {
		err = multierror.Append(err, errors.New("the spec.consoleRef field is immutable"))
	}

	// check no existing authorisation subjects have been modified and that a single subject has been added
	add := rbacutils.Diff(u.updatedAuth.Spec.Authorisations, u.existingAuth.Spec.Authorisations)
	remove := rbacutils.Diff(u.existingAuth.Spec.Authorisations, u.updatedAuth.Spec.Authorisations)

	if len(add) > 1 || len(remove) != 0 {
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
		if s.Name == u.owner {
			err = multierror.Append(err, errors.New("an authoriser cannot authorise their own console"))
			break
		}
	}

	return err
}
