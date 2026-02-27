package automatedrollbackpolicy

import (
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type PolicyValidateWebhook struct {
	logger  logr.Logger
	decoder admission.Decoder
	client  client.Client
}
