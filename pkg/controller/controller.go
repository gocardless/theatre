package controller

import (
	"context"

	kitlog "github.com/go-kit/kit/log"
	clientset "github.com/lawrencejones/rbac-directory/pkg/client/clientset/versioned"
	rbacinformers "github.com/lawrencejones/rbac-directory/pkg/client/informers/externalversions/rbac/v1alpha1"
	"k8s.io/client-go/kubernetes"
)

type Controller struct {
	logger        kitlog.Logger
	kubeclientset kubernetes.Interface
	clientset     clientset.Interface
}

func NewController(
	ctx context.Context, logger kitlog.Logger,
	kubeclientset kubernetes.Interface, clientset clientset.Interface,
	directoryRoleBindingsInformer rbacinformers.DirectoryRoleBindingInformer,
) *Controller {
	return nil
}
