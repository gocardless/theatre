package rollback

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

	var targetRelease *deployv1alpha1.Release
	copy := rollback.DeepCopy()

	// If ToReleaseRef.Name is already set, validate that the referenced Release exists
	if rollback.Spec.ToReleaseRef.Name != "" {
		targetRelease = &deployv1alpha1.Release{}
		if err := w.client.Get(ctx, client.ObjectKey{Name: rollback.Spec.ToReleaseRef.Name, Namespace: req.Namespace}, targetRelease); err != nil {
			if apierrors.IsNotFound(err) {
				return admission.Denied(fmt.Sprintf("ToReleaseRef %q not found", rollback.Spec.ToReleaseRef.Name))
			}
			return admission.Errored(http.StatusInternalServerError, err)
		}
		// Validate that the release belongs to the specified target
		if targetRelease.ReleaseConfig.TargetName != rollback.Spec.ToReleaseRef.Target {
			return admission.Denied(fmt.Sprintf("Release %q does not belong to target %q", rollback.Spec.ToReleaseRef.Name, rollback.Spec.ToReleaseRef.Target))
		}
	} else {
		w.logger.Info("ToReleaseRef.Name not set, finding latest healthy release for target", "target", rollback.Spec.ToReleaseRef.Target)

		releaseList := &deployv1alpha1.ReleaseList{}
		if err := w.client.List(ctx, releaseList, client.InNamespace(req.Namespace)); err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}

		// Filter releases by the specified target
		targetReleases := &deployv1alpha1.ReleaseList{}
		for _, release := range releaseList.Items {
			if release.ReleaseConfig.TargetName == rollback.Spec.ToReleaseRef.Target {
				targetReleases.Items = append(targetReleases.Items, release)
			}
		}

		if len(targetReleases.Items) == 0 {
			return admission.Denied(fmt.Sprintf("no releases found for target %q", rollback.Spec.ToReleaseRef.Target))
		}

		// Ensure there is an active release to roll back from
		activeRelease := deployv1alpha1.FindActiveRelease(targetReleases)
		if activeRelease == nil {
			return admission.Denied(fmt.Sprintf("no active release found for target %q to rollback from", rollback.Spec.ToReleaseRef.Target))
		}

		// Walk back from the active release to find the last healthy release
		targetRelease = deployv1alpha1.FindLastHealthyRelease(targetReleases)
		if targetRelease == nil {
			return admission.Denied(fmt.Sprintf("no healthy release found for target %q to rollback to", rollback.Spec.ToReleaseRef.Target))
		}

		w.logger.Info("auto-setting rollback target", "targetRelease", targetRelease.Name)
		copy.Spec.ToReleaseRef.Name = targetRelease.Name
	}

	// Set owner ref on the target release
	w.logger.Info("setting release owner reference on rollback")
	controllerutil.SetControllerReference(targetRelease, copy, w.client.Scheme())
	copyBytes, err := json.Marshal(copy)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, copyBytes)
}
