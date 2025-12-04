package release

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	admission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type ReleaseNamerWebhook struct {
	logger  logr.Logger
	decoder admission.Decoder
}

func NewReleaseNamerWebhook(logger logr.Logger, scheme *runtime.Scheme) *ReleaseNamerWebhook {
	decoder := admission.NewDecoder(scheme)
	return &ReleaseNamerWebhook{
		logger:  logger,
		decoder: decoder,
	}
}

func (i *ReleaseNamerWebhook) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	release := &deployv1alpha1.Release{}
	if err := i.decoder.Decode(req, release); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	releaseName, err := generateReleaseName(*release)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if release.Name == releaseName {
		return admission.ValidationResponse(true, "Release name already set")
	}

	copy := release.DeepCopy()
	copy.Name = releaseName

	// Marshal the updated release object
	copyBytes, err := json.Marshal(copy)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, copyBytes)
}
