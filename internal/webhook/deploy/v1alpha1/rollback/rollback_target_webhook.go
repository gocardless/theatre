package rollback

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	admission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// RollbackTargetWebhook is a mutating webhook that sets the ToReleaseRef field
// if it's not already set. It finds the last healthy release by walking back
// from the active release using the PreviousRelease field.
type RollbackTargetWebhook struct {
	logger  logr.Logger
	decoder admission.Decoder
	client  client.Client
}

func NewRollbackTargetWebhook(logger logr.Logger, scheme *runtime.Scheme, client client.Client) *RollbackTargetWebhook {
	decoder := admission.NewDecoder(scheme)
	return &RollbackTargetWebhook{
		logger:  logger,
		decoder: decoder,
		client:  client,
	}
}

func (w *RollbackTargetWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	rollback := &deployv1alpha1.Rollback{}
	if err := w.decoder.Decode(req, rollback); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// If ToReleaseRef is already set, no mutation needed
	if rollback.Spec.ToReleaseRef != (deployv1alpha1.ReleaseReference{}) {
		return admission.Allowed("ToReleaseRef already set")
	}

	releaseList := &deployv1alpha1.ReleaseList{}
	if err := w.client.List(ctx, releaseList, client.InNamespace(req.Namespace)); err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	// Walk back from the active release to find the last healthy release
	targetRelease := deployv1alpha1.FindLastHealthyRelease(releaseList)
	if targetRelease == nil {
		return admission.Denied("no healthy release found to rollback to")
	}

	w.logger.Info("auto-setting rollback target", "targetRelease", targetRelease.Name)

	// Mutate the rollback to set the target release
	copy := rollback.DeepCopy()
	copy.Spec.ToReleaseRef = deployv1alpha1.ReleaseReference{Name: targetRelease.Name}

	copyBytes, err := json.Marshal(copy)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, copyBytes)
}
