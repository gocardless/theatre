package istio

import (
	"context"
	"net/http"
	"path"
	"time"

	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kitlog "github.com/go-kit/kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/builder"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/types"
)

const (
	IstioInjectorFQDN = "istio-injector.istio.crd.gocardless.com"
	BinaryName        = "envoy-preflight"
	VolumeName        = "envoy-preflight-install"
	InitContainerName = "envoy-preflight-injector"
)

func NewWebhook(logger kitlog.Logger, mgr manager.Manager, injectorOpts InjectorOptions, opts ...func(*admission.Handler)) (*admission.Webhook, error) {
	var handler admission.Handler
	handler = &injector{
		logger:  kitlog.With(logger, "component", "IstioInjector"),
		decoder: mgr.GetAdmissionDecoder(),
		client:  mgr.GetClient(),
		opts:    injectorOpts,
	}

	for _, opt := range opts {
		opt(&handler)
	}

	namespaceSelectors := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			metav1.LabelSelectorRequirement{
				Key:      injectorOpts.NamespaceLabel,
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{"enabled"},
			},
		},
	}

	return builder.NewWebhookBuilder().
		Name(IstioInjectorFQDN).
		Mutating().
		Operations(admissionregistrationv1beta1.Create).
		ForType(&corev1.Pod{}).
		FailurePolicy(admissionregistrationv1beta1.Fail).
		NamespaceSelector(namespaceSelectors).
		Handlers(handler).
		WithManager(mgr).
		Build()
}

type injector struct {
	logger  kitlog.Logger
	decoder types.Decoder
	client  client.Client
	opts    InjectorOptions
}

type InjectorOptions struct {
	Image          string // image of theatre to use when constructing pod
	InstallPath    string // location of istio installation directory
	NamespaceLabel string // namespace label that enables webhook to operate on
}

var (
	podLabels   = []string{"pod_namespace"}
	handleTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_istio_injector_handle_total",
			Help: "Count of requests handled by the webhook",
		},
		podLabels,
	)
	mutateTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_istio_injector_mutate_total",
			Help: "Count of pods mutated by the webhook",
		},
		podLabels,
	)
	skipTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_istio_injector_skip_total",
			Help: "Count of pods skipped by the webhook, as they lack annotations",
		},
		podLabels,
	)
	errorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_istio_injector_errors_total",
			Help: "Count of not-allowed responses from webhook",
		},
		podLabels,
	)
)

func (i *injector) Handle(ctx context.Context, req types.Request) (resp types.Response) {
	labels := prometheus.Labels{"pod_namespace": req.AdmissionRequest.Namespace}
	logger := kitlog.With(i.logger, "uuid", string(req.AdmissionRequest.UID))
	logger.Log("event", "request.start")
	defer func(start time.Time) {
		logger.Log("event", "request.end", "duration", time.Since(start).Seconds())

		handleTotal.With(labels).Inc()
		{ // add 0 to initialise the metrics
			mutateTotal.With(labels).Add(0)
			skipTotal.With(labels).Add(0)
			errorsTotal.With(labels).Add(0)
		}

		// Catch any Allowed=false responses, as this means we've failed to accept this pod
		if !resp.Response.Allowed {
			errorsTotal.With(labels).Inc()
		}
	}(time.Now())

	pod := &corev1.Pod{}
	if err := i.decoder.Decode(req, pod); err != nil {
		return admission.ErrorResponse(http.StatusBadRequest, err)
	}

	// Skip mutation for pods that have set the sidecar.istio.io/inject annotation to false.
	if v, ok := pod.Annotations["sidecar.istio.io/inject"]; ok && v == "false" {
		logger.Log("event", "pod.skipped", "msg", "sidecar.istio.io/inject annotation set to false")
		skipTotal.With(labels).Inc()
		return admission.PatchResponse(pod, pod)
	}

	// if the request object (pod) has a namespace use it
	pod.Namespace = req.AdmissionRequest.Namespace

	// if the request object (pod) has a name use it
	if req.AdmissionRequest.Name != "" {
		pod.Name = req.AdmissionRequest.Name
	}

	logger = kitlog.With(logger,
		"pod_namespace", pod.Namespace,
		"pod_name", pod.Name,
	)

	mutateTotal.With(labels).Inc() // we're committed to mutating this pod now

	mutatedPod := podInjector{InjectorOptions: i.opts}.Inject(*pod)

	return admission.PatchResponse(pod, mutatedPod)
}

// podInjector isolates the logic around injecting envoy-preflight away from anything to
// do with mutating webhooks. This makes it easy to unit test without getting tangled in
// webhook noise.
type podInjector struct {
	InjectorOptions
}

// Inject configures the given pod to use envoy-preflight. If it returns nil, it's
// because the pod isn't configured for injection.
func (i podInjector) Inject(pod corev1.Pod) *corev1.Pod {
	mutatedPod := pod.DeepCopy()

	initContainerExists := false
	for _, c := range mutatedPod.Spec.InitContainers {
		if c.Name == InitContainerName {
			initContainerExists = true
		}
	}
	if !initContainerExists {
		mutatedPod.Spec.InitContainers = append(mutatedPod.Spec.InitContainers, i.buildInitContainer())
	}

	volumeExists := false
	for _, v := range mutatedPod.Spec.Volumes {
		if v.Name == VolumeName {
			volumeExists = true
		}
	}
	if !volumeExists {
		mutatedPod.Spec.Volumes = append(
			mutatedPod.Spec.Volumes,
			// Installation directory for envoy-preflight binaries, used as a scratch installation path
			corev1.Volume{
				Name: VolumeName,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		)
	}

	// If we don't already have an fsGroup set, we'll need to configure it so we can read
	// the contents of the volumes we mount. Failing to do this will prevent us from reading
	// the projected service account token.
	if mutatedPod.Spec.SecurityContext == nil {
		mutatedPod.Spec.SecurityContext = &corev1.PodSecurityContext{}
	}
	if mutatedPod.Spec.SecurityContext.FSGroup == nil {
		defaultFSGroup := int64(1000)
		mutatedPod.Spec.SecurityContext.FSGroup = &defaultFSGroup
	}

	for idx, container := range mutatedPod.Spec.Containers {
		// skip the istio proxy sidecar container if it has already been injected
		if container.Name == "istio-proxy" {
			continue
		}

		// if envoy-preflight isn't the first command element we need to mutate the container
		if len(container.Command) == 0 || container.Command[0] != BinaryName {
			mutatedPod.Spec.Containers[idx] = i.configureContainer(container)
		}
	}

	return mutatedPod
}

func (i podInjector) buildInitContainer() corev1.Container {
	return corev1.Container{
		Name:            "envoy-preflight-injector",
		Image:           i.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"mv", "/usr/local/bin/envoy-preflight", i.InstallPath},
		VolumeMounts: []corev1.VolumeMount{
			corev1.VolumeMount{
				Name:      "envoy-preflight-install",
				MountPath: i.InstallPath,
				ReadOnly:  false,
			},
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("32Mi"),
				corev1.ResourceCPU:    resource.MustParse("50m"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("32Mi"),
				corev1.ResourceCPU:    resource.MustParse("50m"),
			},
		},
	}
}

// configureContainer returns a copy with the command modified to run envoy-preflight,
// along with a volume mount that will contain the envoy-preflight binary.
func (i podInjector) configureContainer(reference corev1.Container) corev1.Container {
	c := reference.DeepCopy()

	// If this webhook has already ran, remove envoy-preflight from the command
	// and args list and add it back as the command.
	command := path.Join(i.InstallPath, BinaryName)
	args := append(reference.Command, reference.Args...)

	for j := 0; j < len(args); j++ {
		if args[j] == command {
			args = append(args[:j], args[j+1:]...)
		}
	}

	c.Command = []string{command}
	c.Args = args

	envars := make(map[string]struct{})
	for _, e := range reference.Env {
		envars[e.Name] = struct{}{}
	}

	// Set the admin api to the istio pilot-agent port
	if _, ok := envars["ENVOY_ADMIN_API"]; !ok {
		c.Env = append(
			c.Env,
			corev1.EnvVar{
				Name:  "ENVOY_ADMIN_API",
				Value: "http://127.0.0.1:15000",
			},
		)
	}

	// Don't shutdown the istio sidecar proxy when the container exits
	if _, ok := envars["NEVER_KILL_ENVOY"]; !ok {
		c.Env = append(
			c.Env,
			corev1.EnvVar{
				Name:  "NEVER_KILL_ENVOY",
				Value: "true",
			},
		)
	}

	volumeMountExists := false
	for _, v := range reference.VolumeMounts {
		if v.Name == VolumeName {
			volumeMountExists = true
		}
	}
	if !volumeMountExists {
		c.VolumeMounts = append(
			c.VolumeMounts,
			// Mount the binaries from our installation, ensuring we can run the command in this
			// container
			corev1.VolumeMount{
				Name:      VolumeName,
				MountPath: i.InstallPath,
				ReadOnly:  true,
			},
		)
	}

	return *c
}
