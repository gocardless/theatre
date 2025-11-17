package release

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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

func getReleaseName(release deployv1alpha1.Release) (string, error) {
	missingFields := []string{}
	if release.Spec.UtopiaServiceTargetRelease == "" {
		missingFields = append(missingFields, "utopiaServiceTargetRelease")
	}
	if release.Spec.ApplicationRevision.ID == "" {
		missingFields = append(missingFields, "applicationRevision")
	}
	if release.Spec.InfrastructureRevision.ID == "" {
		missingFields = append(missingFields, "infrastructureRevision")
	}

	if len(missingFields) > 0 {
		return "", fmt.Errorf("missing required fields: %s", strings.Join(missingFields, ", "))
	}

	name := release.Spec.UtopiaServiceTargetRelease +
		"-" + release.Spec.InfrastructureRevision.ID[:7] +
		"-" + release.Spec.ApplicationRevision.ID[:7]
	return name, nil
}

func (i *ReleaseNamerWebhook) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	release := &deployv1alpha1.Release{}
	if err := i.decoder.Decode(req, release); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	releaseName, err := getReleaseName(*release)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	copy := release.DeepCopy()
	copy.Name = releaseName

	copyBytes, err := json.Marshal(copy)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, copyBytes)
}
