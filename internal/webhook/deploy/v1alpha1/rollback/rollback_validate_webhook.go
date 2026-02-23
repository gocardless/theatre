package rollback

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	deploy "github.com/gocardless/theatre/v5/internal/controller/deploy"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	admission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// RollbackValidateWebhook is a validating webhook that ensures the isn't another
// rollback in progress for the same target.
type RollbackValidateWebhook struct {
	logger  logr.Logger
	decoder admission.Decoder
	client  client.Client
}

func NewRollbackValidateWebhook(logger logr.Logger, scheme *runtime.Scheme, client client.Client) *RollbackValidateWebhook {
	decoder := admission.NewDecoder(scheme)
	return &RollbackValidateWebhook{
		logger:  logger,
		decoder: decoder,
		client:  client,
	}
}

func (w *RollbackValidateWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	rollback := &deployv1alpha1.Rollback{}
	if err := w.decoder.Decode(req, rollback); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	target := rollback.Spec.ToReleaseRef.Target

	// List all rollbacks for the target
	targetRollbacks := &deployv1alpha1.RollbackList{}
	matchFields := client.MatchingFields(map[string]string{deploy.IndexFieldRollbackTarget: target})
	if err := w.client.List(ctx, targetRollbacks, client.InNamespace(req.Namespace), matchFields); err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	inProgressRollback := deployv1alpha1.FindInProgressRollback(targetRollbacks)
	if inProgressRollback != nil {
		return admission.Denied(fmt.Sprintf("another rollback in-progress for target %q", target))
	}

	return admission.Allowed("no in-progress rollbacks found")
}
