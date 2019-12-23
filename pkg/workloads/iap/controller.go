package iap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	kitlog "github.com/go-kit/kit/log"
	"github.com/pkg/errors"
	compute "google.golang.org/api/compute/v1"
	iap "google.golang.org/api/iap/v1beta1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/pkg/recutil"
)

const (
	// Resource-level events

	EventDelete           = "Delete"
	EventSuccessfulCreate = "SuccessfulCreate"
	EventSuccessfulUpdate = "SuccessfulUpdate"
	EventNoCreateOrUpdate = "NoCreateOrUpdate"

	// Warning events

	EventUnknownOutcome       = "UnknownOutcome"
	EventInvalidSpecification = "InvalidSpecification"
	EventTemplateUnsupported  = "TemplateUnsupported"

	// Console log keys

	ConsoleStarted = "ConsoleStarted"
	ConsoleEnded   = "ConsoleEnded"
)

func Add(ctx context.Context, logger kitlog.Logger, mgr manager.Manager, googleClient *http.Client, projectID string, numericProjectID string, opts ...func(*controller.Options)) (controller.Controller, error) {
	logger = kitlog.With(logger, "component", "IAP")
	ctrlOptions := controller.Options{
		Reconciler: recutil.ResolveAndReconcile(
			ctx, logger, mgr, &workloadsv1alpha1.Console{},
			func(logger kitlog.Logger, request reconcile.Request, obj runtime.Object) (reconcile.Result, error) {
				reconciler := &reconciler{
					ctx:              ctx,
					logger:           logger,
					client:           mgr.GetClient(),
					service:          obj.(*v1.Service),
					name:             request.NamespacedName,
					projectID:        projectID,
					numericProjectID: numericProjectID,
				}

				return reconciler.Reconcile()
			},
		),
	}

	for _, opt := range opts {
		opt(&ctrlOptions)
	}

	ctrl, err := controller.New("iap-controller", mgr, ctrlOptions)
	if err != nil {
		return ctrl, err
	}

	return ctrl, ctrl.Watch(&source.Kind{Type: &v1.Service{}}, &handler.EnqueueRequestForObject{})
}

type reconciler struct {
	ctx     context.Context
	logger  kitlog.Logger
	client  client.Client
	service *v1.Service
	name    types.NamespacedName
}

const (
	ProtectionKey     = "iap.vault.crd.gocardless.com/protection"
	ProtectionEnabled = "enabled"
	MembersKey        = "iap.vault.crd.gocardless.com/members"
)

func (r *reconciler) Reconcile() (res reconcile.Result, err error) {
	if r.service.Annotations[ProtectionKey] != ProtectionEnabled {
		return reconcile.Result{Requeue: false}, nil
	}

	return reconcile.Result{Requeue: true, RequeueAfter: 5 * time.Minute}, nil
}

type IAPConfiguration struct {
	ProjectID        string
	NumericProjectID string
	ClientID         string
	ClientSecret     string
	Members          []string
}

func enableIAP(ctx context.Context, logger kitlog.Logger, service *v1.Service, client *http.Client, cfg IAPConfiguration) error {
	computeService, err := compute.New(client)
	if err != nil {
		return err
	}

	iapService, err := iap.New(client)
	if err != nil {
		return err
	}

	logger.Log("event", "backend_services.list", "project_number", cfg.NumericProjectID)
	services, err := computeService.BackendServices.List(cfg.ProjectID).Do()
	if err != nil {
		return err
	}

	// "{\"kubernetes.io/service-name\":\"production/team-webapp\",\"kubernetes.io/service-port\":\"80\"}"
	serviceNamespaceName := fmt.Sprintf("%s/%s", service.Namespace, service.Name)
	backend := findServiceBackend(serviceNamespaceName, services.Items)
	if backend == nil {
		return fmt.Errorf("failed to find kubernetes service %v", serviceNamespaceName)
	}

	logger = kitlog.With(logger, "backend", backend.Name)
	if backend.Iap != nil && backend.Iap.Enabled && (backend.Iap.Oauth2ClientId == cfg.ClientID) {
		logger.Log("event", "backend.iap_already_enabled")
	} else {
		// If IAP is enabled but for different credentials then we must first disable it to
		// properly set new credentials.
		if backend.Iap != nil && (backend.Iap.Oauth2ClientId != cfg.ClientID) {
			logger.Log("iap.disable", "reason", "bad_credentials",
				"msg", "IAP is configured for different credentials, must disable")
			patch := computeService.BackendServices.Patch(cfg.ProjectID, backend.Name, &compute.BackendService{
				Iap: &compute.BackendServiceIAP{Enabled: false},
			})

			disableOp, err := patch.Do()
			if err != nil {
				return errors.Wrap(err, "failed to disable IAP")
			}

			err = GcpWaitForOperation(
				ctx, computeService, cfg.ProjectID, disableOp,
				func(op *compute.Operation) {
					logger.Log("operation", op.Name, "status", op.Status, "event", "iap.disabling")
					time.Sleep(10 * time.Second)
				},
			)

			if err != nil {
				return errors.Wrap(err, "operation to disable IAP failed")
			}
		}

		patch := computeService.BackendServices.Patch(cfg.ProjectID, backend.Name, &compute.BackendService{
			Iap: &compute.BackendServiceIAP{
				Enabled:            true,
				Oauth2ClientId:     cfg.ClientID,
				Oauth2ClientSecret: cfg.ClientSecret,
			},
		})

		logger.Log("event", "iap.enable")
		enableOp, err := patch.Do()
		if err != nil {
			return errors.Wrap(err, "failed to enable IAP")
		}

		err = GcpWaitForOperation(
			ctx, computeService, cfg.ProjectID, enableOp,
			func(op *compute.Operation) {
				logger.Log("operation", op.Name, "status", op.Status, "event", "iap.enabling")
				time.Sleep(10 * time.Second)
			},
		)

		if err != nil {
			return errors.Wrap(err, "operation to enable IAP failed")
		}
	}

	logger.Log("event", "iap.configure", "members", strings.Join(cfg.Members, ","))
	policy, err := iapService.Projects.IapWeb.SetIamPolicy(
		fmt.Sprintf("projects/%d/iap_web/compute/services/%d", cfg.NumericProjectID, backend.Id),
		&iap.SetIamPolicyRequest{
			Policy: &iap.Policy{
				Bindings: []*iap.Binding{
					&iap.Binding{
						Role:    "roles/iap.httpsResourceAccessor",
						Members: cfg.Members,
					},
				},
			},
		}).Do()

	if err != nil {
		return errors.Wrap(err, "failed to set membership on IAP")
	}

	policyBytes, _ := policy.MarshalJSON()
	logger.Log("event", "iap.enabled", "service", serviceNamespaceName, "policy", string(policyBytes))

	return nil
}

type serviceDescription struct {
	Name string `json:"kubernetes.io/service-name"`
	Port string `json:"kubernetes.io/service-port"`
}

// findServiceBackend will return the compute.BackendService that is assigned to the
// kubernetes service name.
func findServiceBackend(name string, services []*compute.BackendService) *compute.BackendService {
	var desc serviceDescription

	for _, service := range services {
		if err := json.Unmarshal([]byte(service.Description), &desc); err == nil {
			if desc.Name == name {
				return service
			}
		}
	}

	return nil
}

// GcpWaitForOperation continually polls the GCP Compute API until the given operation has
// succeeded or failed. It relies on the supplied context timing out to bound execution.
func GcpWaitForOperation(
	ctx context.Context,
	computeService *compute.Service, projectID string, op *compute.Operation,
	progress func(*compute.Operation),
) (err error) {
	for {
		if err != nil {
			return err
		}

		if op.Status == "DONE" {
			if op.Error != nil && len(op.Error.Errors) > 0 {
				return fmt.Errorf("operation %s failed: %s", op.Name, op.Error.Errors[0].Message)
			}

			return nil
		}

		if progress != nil {
			progress(op)
		}

		// There also exist regional operations, but I don't know what format to expect for
		// these (what would parseRegion look like?). Let's wait until we require support to
		// implement it!
		if op.Zone == "" && op.Region == "" {
			op, err = computeService.GlobalOperations.Get(projectID, op.Name).Context(ctx).Do()
		} else {
			op, err = computeService.ZoneOperations.Get(projectID, parseZone(op.Zone), op.Name).Context(ctx).Do()
		}
	}
}

// parseZone takes the URL form of a GCP zone and returns the shortened zone format:
// parseZone(https://www.googleapis.com/compute/v1/projects/gc-lab-1eb1/zones/europe-west4-c)
// => europe-west4-c
func parseZone(zoneURL string) string {
	urlComponents := strings.Split(zoneURL, "/")
	return urlComponents[len(urlComponents)-1]
}
