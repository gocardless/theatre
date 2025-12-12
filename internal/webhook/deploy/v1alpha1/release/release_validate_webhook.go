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

		if !oldRelease.ReleaseConfig.Equals(&release.ReleaseConfig) {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("release .config.targetName, config.revision[].name and"+
				" config.revision[].id are immutable"))
		}
	}

	return admission.Allowed("")
}
