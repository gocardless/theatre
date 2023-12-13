package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/mitchellh/mapstructure"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const SecretsInjectorFQDN = "secrets-injector.vault.crd.gocardless.com"
const EnvconsulInjectorFQDN = "envconsul-injector.vault.crd.gocardless.com"

var FQDNArray = []string{SecretsInjectorFQDN, EnvconsulInjectorFQDN}

type SecretsInjector struct {
	client  client.Client
	logger  logr.Logger
	decoder *admission.Decoder
	opts    SecretsInjectorOptions
}

func NewSecretsInjector(c client.Client, logger logr.Logger, opts SecretsInjectorOptions, scheme *runtime.Scheme) *SecretsInjector {
	return &SecretsInjector{
		client:  c,
		logger:  logger,
		opts:    opts,
		decoder: admission.NewDecoder(scheme),
	}
}

func (e *SecretsInjector) InjectDecoder(d *admission.Decoder) error {
	e.decoder = d
	return nil
}

type SecretsInjectorOptions struct {
	Image                       string           // image of theatre to use when constructing pod
	InstallPath                 string           // location of vault installation directory
	NamespaceLabel              string           // namespace label that enables webhook to operate on
	VaultConfigMapKey           client.ObjectKey // reference to the vault config configMap
	ServiceAccountTokenFile     string           // mount path of our projected service account token
	ServiceAccountTokenExpiry   time.Duration    // Kubelet expiry for the service account token
	ServiceAccountTokenAudience string           // optional token audience
	Timeout                     time.Duration    // timeout to use when reading secrets from Vault
	Debug                       bool             // whether to enable debug mode for verbose loggging
}

var (
	podLabels   = []string{"pod_namespace"}
	handleTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_vault_secrets_injector_handle_total",
			Help: "Count of requests handled by the webhook",
		},
		podLabels,
	)
	mutateTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_vault_secrets_injector_mutate_total",
			Help: "Count of pods mutated by the webhook",
		},
		podLabels,
	)
	skipTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_vault_secrets_injector_skip_total",
			Help: "Count of pods skipped by the webhook, as they lack annotations",
		},
		podLabels,
	)
	errorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_vault_secrets_injector_errors_total",
			Help: "Count of not-allowed responses from webhook",
		},
		podLabels,
	)
)

func init() {
	// Register custom metrics with the global controller runtime prometheus registry
	metrics.Registry.MustRegister(handleTotal, mutateTotal, skipTotal, errorsTotal)
}

func (i *SecretsInjector) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	labels := prometheus.Labels{"pod_namespace": req.Namespace}
	logger := i.logger.WithValues("uuid", string(req.UID))
	logger.Info("starting request", "event", "request.start")
	defer func(start time.Time) {
		logger.Info("request completed", "event", "request.end", "duration", time.Since(start).Seconds())

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
	// annotation on them here so they can start uninterrupted in the event
	// code futher along returns an error.
	if _, ok := getFQDNConfig(pod.Annotations, FQDNArray); !ok {
		logger.Info("skipping pod with no annotation", "event", "pod.skipped", "msg", "no annotation found")
		skipTotal.With(labels).Inc()
		return admission.Allowed("no annotation found")
	}

	// ensure the pod has a namespace if it has one as we use it in the secretMountPathPrefix
	pod.Namespace = req.AdmissionRequest.Namespace

	// if the request object (pod) has a name use it
	if req.AdmissionRequest.Name != "" {
		pod.Name = req.AdmissionRequest.Name
	}

	logger = logger.WithValues(
		"pod_namespace", pod.Namespace,
		"pod_name", pod.Name,
	)

	mutateTotal.With(labels).Inc() // we're committed to mutating this pod now

	vaultConfigMap := &corev1.ConfigMap{}
	if err := i.client.Get(ctx, i.opts.VaultConfigMapKey, vaultConfigMap); err != nil {
		logger.Info("vault config error", "event", "vault.config", "error", err)
		return admission.Errored(http.StatusInternalServerError, err)
	}
	vaultConfig, err := newVaultConfig(vaultConfigMap)
	if err != nil {
		logger.Info("vault config error", "event", "vault.config", "error", err)
		return admission.Errored(http.StatusInternalServerError, err)
	}

	mutatedPod := podInjector{SecretsInjectorOptions: i.opts, vaultConfig: vaultConfig}.Inject(*pod)
	if mutatedPod == nil {
		logger.Info("no annotation found during inject - this should never occur", "event", "pod.skipped", "msg")
		return admission.Allowed("no annotation found")
	}

	mutatedPodBytes, err := json.Marshal(mutatedPod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, mutatedPodBytes)
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

// podInjector isolates the logic around injecting theatre-secrets away from anything to
// do with mutating webhooks. This makes it easy to unit test without getting tangled in
// webhook noise.
type podInjector struct {
	SecretsInjectorOptions
	vaultConfig
}

// Inject configures the given pod to use theatre-secrets. If it returns nil, it's
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
			Name: "theatre-secrets-install",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		// Projected service account tokens that are automatically rotated, unlike the default
		// service account tokens Kubernetes normally mounts.
		corev1.Volume{
			Name: "theatre-secrets-serviceaccount",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					// Ensure this token is readable by whatever user the container might run in, as
					// your application might run with a non-root user but must be able to access
					// its secrets.
					DefaultMode: func() *int32 { mode := int32(444); return &mode }(),
					Sources: []corev1.VolumeProjection{
						{
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
//	secrets-injector.vault.crd.gocardless.com/configs: app:config.yaml,sidecar
//
// Valid values for the annotation are:
//
//	annotation ::= container_config | ',' annotation
//	container_config ::= container_name ( ':' config_file )?
//
// If no config file is specified, we inject theatre-secrets but don't load
// configuration from files, relying solely on environment variables.
func parseContainerConfigs(pod corev1.Pod) map[string]string {
	configString, ok := getFQDNConfig(pod.Annotations, FQDNArray)
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
		Name:            "theatre-secrets-injector",
		Image:           i.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"theatre-secrets", "install", "--path", i.InstallPath},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "theatre-secrets-install",
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

// configureContainer returns a copy with the command modified to run theatre-secrets,
// along with a volume mount that will contain the secrets binaries.
func (i podInjector) configureContainer(reference corev1.Container, containerConfigPath, secretMountPathPrefix string) corev1.Container {
	c := &reference

	args := []string{"exec"}
	if i.Debug {
		args = append(args, "--debug")
	}

	args = append(args, "--vault-address", i.Address)
	args = append(args, "--vault-http-timeout", i.Timeout.String())
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

	c.Command = []string{path.Join(i.InstallPath, "theatre-secrets")}
	c.Args = args

	c.VolumeMounts = append(
		c.VolumeMounts,
		// Mount the binaries from our installation, ensuring we can run the command in this
		// container
		corev1.VolumeMount{
			Name:      "theatre-secrets-install",
			MountPath: i.InstallPath,
			ReadOnly:  true,
		},
		// Explicitly mount service account tokens from the projected volume
		corev1.VolumeMount{
			Name:      "theatre-secrets-serviceaccount",
			MountPath: path.Dir(i.ServiceAccountTokenFile),
			ReadOnly:  true,
		},
	)

	return *c
}

// getFQDNConfig takes a set of pod annotations (map[string]string), and an
// array of FQDNs to check for. If any are found under key 'FQDN/configs' then
// return the config and a true bool
//
// This is a temporary measure to support two FQDNs whilst we migrate away from
// the envconsul name
func getFQDNConfig(podAnnotations map[string]string, FQDNArray []string) (string, bool) {
	for _, name := range FQDNArray {
		data, ok := podAnnotations[fmt.Sprintf("%s/configs", name)]
		if !ok {
			continue
		}
		return data, ok
	}
	return "", false
}
