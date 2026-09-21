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
	rbacv1 "k8s.io/api/rbac/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	workloadsv1alpha1 "github.com/gocardless/theatre/v5/api/workloads/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/logging"
	rbacutils "github.com/gocardless/theatre/v5/pkg/rbac"
)

// +kubebuilder:object:generate=false
type ConsoleAuthorisationWebhook struct {
	client            client.Client
	lifecycleRecorder workloadsv1alpha1.LifecycleEventRecorder
	logger            logr.Logger
	decoder           admission.Decoder
}

func NewConsoleAuthorisationWebhook(c client.Client, lifecycleRecorder workloadsv1alpha1.LifecycleEventRecorder, logger logr.Logger, scheme *runtime.Scheme) *ConsoleAuthorisationWebhook {
	decoder := admission.NewDecoder(scheme)

	return &ConsoleAuthorisationWebhook{
		client:            c,
		lifecycleRecorder: lifecycleRecorder,
		logger:            logger,
		decoder:           decoder,
	}
}

func (c *ConsoleAuthorisationWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := c.logger.WithValues("uuid", string(req.UID))
	logger.Info("starting request", "event", "request.start")
	defer func(start time.Time) {
		logger.Info("completed request", "event", "request.end", "duration", time.Since(start).Seconds())
	}(time.Now())

	// request console authorisation object
	updatedAuth := &workloadsv1alpha1.ConsoleAuthorisation{}
	if err := c.decoder.DecodeRaw(req.Object, updatedAuth); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// existing console authorisation object
	existingAuth := &workloadsv1alpha1.ConsoleAuthorisation{}
	if err := c.decoder.DecodeRaw(req.OldObject, existingAuth); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
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

func (c *ConsoleAuthorisationWebhook) getConsole(ctx context.Context, name, namespace string) (*workloadsv1alpha1.Console, error) {
	namespacedName := client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}

	csl := &workloadsv1alpha1.Console{}

	return csl, c.client.Get(ctx, namespacedName, csl)
}

type ConsoleAuthorisationUpdate struct {
	existingAuth *workloadsv1alpha1.ConsoleAuthorisation
	updatedAuth  *workloadsv1alpha1.ConsoleAuthorisation
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

	// Diff only reports subjects that aren't already present, so re-adding a
	// subject that has already authorised the console produces an empty add
	// list. Comparing lengths catches that case too, preventing the same
	// approver from being counted more than once.
	expectedLen := len(u.existingAuth.Spec.Authorisations) + len(add)
	if len(add) > 1 || len(remove) != 0 || len(u.updatedAuth.Spec.Authorisations) != expectedLen {
		err = multierror.Append(err, errors.New("the spec.authorisations field can only be appended to (with one subject) per update"))
	}

	// check the user is only adding themselves to the list of authorisers
	for _, s := range add {
		if s.Name != u.user {
			err = multierror.Append(err, errors.New("only the current user can be added as an authoriser"))
			break
		}
	}

	// An authorisation records an authenticated Kubernetes user, so the subject
	// must say so. Authorising subjects are copied verbatim into the console's
	// attach DirectoryRoleBinding, where the kind decides how the name is
	// interpreted - a GoogleGroup subject, for example, is expanded into that
	// group's members - so a non-User kind would grant attach access to
	// identities that never authenticated.
	for _, s := range add {
		if s.Kind != rbacv1.UserKind {
			err = multierror.Append(err, errors.Errorf("an authoriser must be a %s subject, got %q", rbacv1.UserKind, s.Kind))
			break
		}

		// An empty apiGroup is defaulted to the RBAC group for User subjects,
		// and is what the theatre-consoles CLI sends.
		if s.APIGroup != "" && s.APIGroup != rbacv1.GroupName {
			err = multierror.Append(err, errors.Errorf("an authoriser must belong to the %q apiGroup, got %q", rbacv1.GroupName, s.APIGroup))
			break
		}
	}

	// Name is the only part of a Subject that is tied to the authenticated
	// identity. Namespace is not checked at all - it is meaningless for User
	// subjects, and the theatre-consoles CLI sets it to the console's
	// namespace - so it must not be usable to make the same approver count as
	// more than one authoriser. Reject the update outright if this user
	// already appears in the existing authorisations, whatever the other
	// fields of those entries say.
	for _, s := range u.existingAuth.Spec.Authorisations {
		if s.Name == u.user {
			err = multierror.Append(err, errors.New("this user has already authorised the console"))
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
