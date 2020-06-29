package envconsul

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kitlog "github.com/go-kit/kit/log"
	"github.com/mitchellh/mapstructure"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const EnvconsulInjectorFQDN = "envconsul-injector.vault.crd.gocardless.com"

func NewWebhook(logger kitlog.Logger, mgr manager.Manager, injectorOpts InjectorOptions, opts ...func(*admission.Handler)) (*admission.Webhook, error) {
	var handler admission.Handler
	handler = &injector{
		logger: kitlog.With(logger, "component", "EnvconsulInjector"),
		// decoder: mgr.Decoder(),
		client: mgr.GetClient(),
		opts:   injectorOpts,
	}

	for _, opt := range opts {
		opt(&handler)
	}

	_ = &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			metav1.LabelSelectorRequirement{
				Key:      injectorOpts.NamespaceLabel,
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{"enabled"},
			},
		},
	}

	return nil, nil
	// return builder.NewWebhookBuilder().
	// 	Name(EnvconsulInjectorFQDN).
	// 	Mutating().
	// 	Operations(admissionregistrationv1beta1.Create).
	// 	ForType(&corev1.Pod{}).
	// 	FailurePolicy(admissionregistrationv1beta1.Fail).
	// 	NamespaceSelector(namespaceSelectors).
	// 	Handlers(handler).
	// 	WithManager(mgr).
	// 	Build()
}

type injector struct {
	logger  kitlog.Logger
	decoder admission.Decoder
	client  client.Client
	opts    InjectorOptions
}

type InjectorOptions struct {
	Image                       string           // image of theatre to use when constructing pod
	InstallPath                 string           // location of vault installation directory
	NamespaceLabel              string           // namespace label that enables webhook to operate on
	VaultConfigMapKey           client.ObjectKey // reference to the vault config configMap
	ServiceAccountTokenFile     string           // mount path of our projected service account token
	ServiceAccountTokenExpiry   time.Duration    // Kubelet expiry for the service account token
	ServiceAccountTokenAudience string           // optional token audience
}

var (
	podLabels   = []string{"pod_namespace"}
	handleTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_vault_envconsul_injector_handle_total",
			Help: "Count of requests handled by the webhook",
		},
		podLabels,
	)
	mutateTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_vault_envconsul_injector_mutate_total",
			Help: "Count of pods mutated by the webhook",
		},
		podLabels,
	)
	skipTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_vault_envconsul_injector_skip_total",
			Help: "Count of pods skipped by the webhook, as they lack annotations",
		},
		podLabels,
	)
	errorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_vault_envconsul_injector_errors_total",
			Help: "Count of not-allowed responses from webhook",
		},
		podLabels,
	)
)

func (i *injector) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	labels := prometheus.Labels{"pod_namespace": req.Namespace}
	logger := kitlog.With(i.logger, "uuid", string(req.UID))
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
		if !resp.Allowed {
			errorsTotal.With(labels).Inc()
		}
	}(time.Now())

	pod := &corev1.Pod{}
	if err := i.decoder.Decode(req, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// This webhook receives requests on all pod creation and so is in the critical
	// path for all pod creation. We need to exit for pods that don't have the
	// annotation on them here so they cant start uninterrupted in the event
	// code futher along returns an error.
	if _, ok := pod.Annotations[fmt.Sprintf("%s/configs", EnvconsulInjectorFQDN)]; !ok {
		logger.Log("event", "pod.skipped", "msg", "no annotation found")
		skipTotal.With(labels).Inc()
		return admission.Allowed("no annotation found")
	}

	// ensure the pod has a namespace if it has one as we use it in the secretMountPathPrefix
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

	vaultConfigMap := &corev1.ConfigMap{}
	if err := i.client.Get(ctx, i.opts.VaultConfigMapKey, vaultConfigMap); err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	vaultConfig, err := newVaultConfig(vaultConfigMap)
	if err != nil {
		logger.Log("event", "vault.config", "error", err)
		return admission.Errored(http.StatusInternalServerError, err)
	}

	mutatedPod := podInjector{InjectorOptions: i.opts, vaultConfig: vaultConfig}.Inject(*pod)
	if mutatedPod == nil {
		logger.Log("event", "pod.skipped", "msg", "no annotation found during inject - this should never occur")
		return admission.Allowed("no annotation found")
	}

	return admission.Patched("TODO")
}

// vaultConfig specifies the structure we expect to find in a cluster-global namespace,
// which we intend to be provisioned as part of whatever process generates the auth
// backend in Vault.
//
// If we can't parse the configmap into this structure, we should fail our webhook.
type vaultConfig struct {
	Address               string `mapstructure:"address"`
	AuthMountPath         string `mapstructure:"auth_mount_path"`
	AuthRole              string `mapstructure:"auth_role"`
	SecretMountPathPrefix string `mapstructure:"secret_mount_path_prefix"`
}

func newVaultConfig(cfgmap *corev1.ConfigMap) (vaultConfig, error) {
	var cfg vaultConfig
	return cfg, mapstructure.Decode(cfgmap.Data, &cfg)
}

// podInjector isolates the logic around injecting theatre-envconsul away from anything to
// do with mutating webhooks. This makes it easy to unit test without getting tangled in
// webhook noise.
type podInjector struct {
	InjectorOptions
	vaultConfig
}

// Inject configures the given pod to use theatre-envconsul. If it returns nil, it's
// because the pod isn't configured for injection.
func (i podInjector) Inject(pod corev1.Pod) *corev1.Pod {
	containerConfigs := parseContainerConfigs(pod)
	if containerConfigs == nil {
		return nil
	}

	mutatedPod := pod.DeepCopy()
	expirySeconds := int64(i.ServiceAccountTokenExpiry / time.Second)

	mutatedPod.Spec.InitContainers = append(mutatedPod.Spec.InitContainers, i.buildInitContainer())
	mutatedPod.Spec.Volumes = append(
		mutatedPod.Spec.Volumes,
		// Installation directory for theatre binaries, used as a scratch installation path
		corev1.Volume{
			Name: "theatre-envconsul-install",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		// Projected service account tokens that are automatically rotated, unlike the default
		// service account tokens Kubernetes normally mounts.
		corev1.Volume{
			Name: "theatre-envconsul-serviceaccount",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					// Ensure this token is readable by whatever user the container might run in, as
					// your application might run with a non-root user but must be able to access
					// its secrets.
					DefaultMode: func() *int32 { mode := int32(444); return &mode }(),
					Sources: []corev1.VolumeProjection{
						corev1.VolumeProjection{
							ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
								Path:              path.Base(i.ServiceAccountTokenFile),
								ExpirationSeconds: &expirySeconds,
								Audience:          i.ServiceAccountTokenAudience,
							},
						},
					},
				},
			},
		},
	)

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

	secretMountPathPrefix := path.Join(i.vaultConfig.SecretMountPathPrefix, pod.Namespace, pod.Spec.ServiceAccountName)

	for idx, container := range mutatedPod.Spec.Containers {
		containerConfigPath, ok := containerConfigs[container.Name]
		if !ok {
			continue
		}

		mutatedPod.Spec.Containers[idx] = i.configureContainer(container, containerConfigPath, secretMountPathPrefix)
	}

	return mutatedPod
}

// parseContainerConfigs extracts the pod annotation and parses that configuration
// required for this container.
//
//   envconsul-injector.vault.crd.gocardless.com/configs: app:config.yaml,sidecar
//
// Valid values for the annotation are:
//
//   annotation ::= container_config | ',' annotation
//   container_config ::= container_name ( ':' config_file )?
//
// If no config file is specified, we inject theatre-envconsul but don't load
// configuration from files, relying solely on environment variables.
func parseContainerConfigs(pod corev1.Pod) map[string]string {
	configString, ok := pod.Annotations[fmt.Sprintf("%s/configs", EnvconsulInjectorFQDN)]
	if !ok {
		return nil
	}

	containerConfigs := map[string]string{}
	for _, containerConfig := range strings.Split(configString, ",") {
		elems := strings.SplitN(containerConfig, ":", 2)
		if len(elems) == 1 {
			containerConfigs[strings.TrimSpace(elems[0])] = "" // no config file means just inject
		} else {
			containerConfigs[strings.TrimSpace(elems[0])] = strings.TrimSpace(elems[1]) // otherwise use specified config file
		}
	}

	return containerConfigs
}

func (i podInjector) buildInitContainer() corev1.Container {
	return corev1.Container{
		Name:            "theatre-envconsul-injector",
		Image:           i.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"theatre-envconsul", "install", "--path", i.InstallPath},
		VolumeMounts: []corev1.VolumeMount{
			corev1.VolumeMount{
				Name:      "theatre-envconsul-install",
				MountPath: i.InstallPath,
				ReadOnly:  false,
			},
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("64Mi"),
				corev1.ResourceCPU:    resource.MustParse("50m"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("64Mi"),
				corev1.ResourceCPU:    resource.MustParse("50m"),
			},
		},
	}
}

// configureContainer returns a copy with the command modified to run theatre-envconsul,
// along with a volume mount that will contain the envconsul binaries.
func (i podInjector) configureContainer(reference corev1.Container, containerConfigPath, secretMountPathPrefix string) corev1.Container {
	c := &reference

	args := []string{"exec"}
	args = append(args, "--install-path", i.InstallPath)
	args = append(args, "--vault-address", i.Address)
	args = append(args, "--vault-path-prefix", secretMountPathPrefix)
	args = append(args, "--auth-backend-mount-path", i.AuthMountPath)
	args = append(args, "--auth-backend-role", i.AuthRole)
	args = append(args, "--service-account-token-file", i.ServiceAccountTokenFile)

	if containerConfigPath != "" {
		args = append(args, "--config-file", containerConfigPath)
	}

	execCommand := []string{"--"}
	execCommand = append(execCommand, reference.Command...)
	execCommand = append(execCommand, reference.Args...)
	args = append(args, execCommand...)

	c.Command = []string{path.Join(i.InstallPath, "theatre-envconsul")}
	c.Args = args

	c.VolumeMounts = append(
		c.VolumeMounts,
		// Mount the binaries from our installation, ensuring we can run the command in this
		// container
		corev1.VolumeMount{
			Name:      "theatre-envconsul-install",
			MountPath: i.InstallPath,
			ReadOnly:  true,
		},
		// Explicitly mount service account tokens from the projected volume
		corev1.VolumeMount{
			Name:      "theatre-envconsul-serviceaccount",
			MountPath: path.Dir(i.ServiceAccountTokenFile),
			ReadOnly:  true,
		},
	)

	return *c
}
