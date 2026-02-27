package automatedrollbackpolicy

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	deploy "github.com/gocardless/theatre/v5/internal/controller/deploy"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type AutomatedRollbackPolicyValidateWebhook struct {
	logger  logr.Logger
	decoder admission.Decoder
	client  client.Client
}

func NewAutomatedRollbackPolicyValidateWebhook(logger logr.Logger, scheme *runtime.Scheme, client client.Client) *AutomatedRollbackPolicyValidateWebhook {
	decoder := admission.NewDecoder(scheme)
	return &AutomatedRollbackPolicyValidateWebhook{
		logger:  logger,
		decoder: decoder,
		client:  client,
	}
}

func (w *AutomatedRollbackPolicyValidateWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	policy := &deployv1alpha1.AutomatedRollbackPolicy{}
	if err := w.decoder.Decode(req, policy); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	targetName := policy.Spec.TargetName
	w.logger.Info("Validating automated rollback policy", "name", policy.Name, "namespace", policy.Namespace, "targetName", targetName)

	policies := &deployv1alpha1.AutomatedRollbackPolicyList{}
	if err := w.client.List(ctx, policies,
		client.InNamespace(req.Namespace),
		client.MatchingFields(map[string]string{deploy.IndexFieldPolicyTargetName: targetName}),
	); err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	if len(policies.Items) > 0 {
		return admission.Denied(fmt.Sprintf("automated rollback policy already exists for target %s", targetName))
	}

	return admission.Allowed("automated rollback policy is valid")
}
