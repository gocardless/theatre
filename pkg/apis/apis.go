package apis

import (
	"k8s.io/apimachinery/pkg/runtime"

	rbacv1alpha1 "github.com/gocardless/theatre/pkg/apis/rbac/v1alpha1"
	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
)

var (
	// AddToSchemes collects all the AddToScheme functions from each of our API groups into
	// a single SchemeBuilder, which can then provide the single function required to add
	// those resources to a Scheme.
	AddToSchemes = runtime.SchemeBuilder{
		rbacv1alpha1.AddToScheme,
		workloadsv1alpha1.AddToScheme,
	}

	// AddToScheme when called with a Scheme will add all our CRDs into the RESTClient
	AddToScheme = AddToSchemes.AddToScheme
)
