package handler

import (
	workloadsv1alpha1 "github.com/gocardless/theatre/v3/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/v3/controllers/workloads/console/termination/status"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {
}

type TerminationHandler struct {
	client client.Client
}

type Options struct{}

func NewTerminationHandler(c client.Client, opts *Options) *TerminationHandler {
	return &TerminationHandler{
		client: c,
	}
}

func (h *TerminationHandler) Handle(instance *workloadsv1alpha1.Console) (*status.Result, error) {
	switch instance.Status.Phase {
	default:
		return nil, nil
	}
}
