package release

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	admission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type ReleaseValidateWebhook struct {
	logger  logr.Logger
	decoder admission.Decoder
}

func NewReleaseValidateWebhook(logger logr.Logger, scheme *runtime.Scheme) *ReleaseValidateWebhook {
	decoder := admission.NewDecoder(scheme)
	return &ReleaseValidateWebhook{
		logger:  logger,
		decoder: decoder,
	}
}

func (i *ReleaseValidateWebhook) validateSpecImmutable(oldSpec, newSpec deployv1alpha1.ReleaseSpec) error {
	if oldSpec.UtopiaServiceTargetRelease != newSpec.UtopiaServiceTargetRelease {
		return fmt.Errorf(
			"spec.utopiaServiceTargetRelease is immutable; cannot change from %q to %q",
			oldSpec.UtopiaServiceTargetRelease, newSpec.UtopiaServiceTargetRelease,
		)
	}

	if oldSpec.ApplicationRevision.ID != newSpec.ApplicationRevision.ID {
		return fmt.Errorf(
			"spec.applicationRevision.id is immutable; cannot change from %q to %q",
			oldSpec.ApplicationRevision.ID, newSpec.ApplicationRevision.ID,
		)
	}

	if oldSpec.InfrastructureRevision.ID != newSpec.InfrastructureRevision.ID {
		return fmt.Errorf(
			"spec.InfrastructureRevision.id is immutable; cannot change from %q to %q",
			oldSpec.InfrastructureRevision.ID, newSpec.InfrastructureRevision.ID,
		)
	}
	return nil
}

func (i *ReleaseValidateWebhook) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	release := &deployv1alpha1.Release{}
	if err := i.decoder.DecodeRaw(req.Object, release); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if req.Operation == admissionv1.Update {
		oldRelease := &deployv1alpha1.Release{}
		if err := i.decoder.DecodeRaw(req.OldObject, oldRelease); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		if err := i.validateSpecImmutable(oldRelease.Spec, release.Spec); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	}

	return admission.Allowed("")
}
