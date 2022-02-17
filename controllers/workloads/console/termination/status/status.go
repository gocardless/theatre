package status

import (
	workloadsv1alpha1 "github.com/gocardless/theatre/v3/apis/workloads/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func UpdateStatus(c client.Client, instance *workloadsv1alpha1.Console, result *Result) error {
	return nil
}
