package envconsul

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kitlog "github.com/go-kit/kit/log"
	"github.com/mitchellh/mapstructure"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/builder"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/types"
)

const EnvconsulInjectorFQDN = "envconsul-injector.vault.crd.gocardless.com"

func NewWebhook(logger kitlog.Logger, mgr manager.Manager, injectorOpts InjectorOptions, opts ...func(*admission.Handler)) (*admission.Webhook, error) {
	var handler admission.Handler
	handler = &Injector{
		logger:  kitlog.With(logger, "component", "EnvconsulInjector"),
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
		Name(EnvconsulInjectorFQDN).
		Mutating().
		Operations(admissionregistrationv1beta1.Create).
		ForType(&corev1.Pod{}).
		FailurePolicy(admissionregistrationv1beta1.Fail).
		NamespaceSelector(namespaceSelectors).
		Handlers(handler).
		WithManager(mgr).
		Build()
}

type Injector struct {
	logger  kitlog.Logger
	decoder types.Decoder
	client  client.Client
	opts    InjectorOptions
}

type InjectorOptions struct {
	Image             string           // image of theatre to use when constructing pod
	InstallPath       string           // location of vault installation directory
	NamespaceLabel    string           // namespace label that enables webhook to operate on
	VaultConfigMapKey client.ObjectKey // reference to the vault config configMap
}

func (i *Injector) Handle(ctx context.Context, req types.Request) types.Response {
	logger := kitlog.With(i.logger, "uuid", string(req.AdmissionRequest.UID))
	logger.Log("event", "request.start")
	defer func(start time.Time) {
		logger.Log("event", "request.end", "duration", time.Since(start).Seconds())
	}(time.Now())

	pod := &corev1.Pod{}
	if err := i.decoder.Decode(req, pod); err != nil {
		return admission.ErrorResponse(http.StatusBadRequest, err)
	}

	// This webhook receives requests on all pod creation and so is in the critical
	// path for all pod creation. We need to exit for pods that don't have the
	// annotation on them here so they cant start uninterrupted in the event
	// code futher along returns an error.
	if _, ok := pod.Annotations[fmt.Sprintf("%s/configs", EnvconsulInjectorFQDN)]; !ok {
		logger.Log("event", "pod.skipped", "msg", "no annotation found")
		return admission.PatchResponse(pod, pod)
	}

	logger = kitlog.With(logger, "pod_namespace", pod.Namespace, "pod_name", pod.Name)

	vaultConfigMap := &corev1.ConfigMap{}
	if err := i.client.Get(ctx, i.opts.VaultConfigMapKey, vaultConfigMap); err != nil {
		return admission.ErrorResponse(http.StatusInternalServerError, err)
	}
	vaultConfig, err := newVaultConfig(vaultConfigMap)
	if err != nil {
		logger.Log("event", "vault.config", "error", err)
		return admission.ErrorResponse(http.StatusInternalServerError, err)
	}

	mutatedPod := PodInjector{InjectorOptions: i.opts, VaultConfig: vaultConfig}.Inject(*pod)
	if mutatedPod == nil {
		logger.Log("event", "pod.skipped", "msg", "no annotation found during inject - this should never occur")
		return admission.PatchResponse(pod, pod)
	}

	return admission.PatchResponse(pod, mutatedPod)
}

// VaultConfig specifies the structure we expect to find in a cluster-global namespace,
// which we intend to be provisioned as part of whatever process generates the auth
// backend in Vault.
//
// If we can't parse the configmap into this structure, we should fail our webhook.
type VaultConfig struct {
	Address       string `mapstructure:"address"`
	AuthMountPath string `mapstructure:"auth_mount_path"`
	AuthRole      string `mapstructure:"auth_role"`
}

func newVaultConfig(cfgmap *corev1.ConfigMap) (VaultConfig, error) {
	var cfg VaultConfig
	return cfg, mapstructure.Decode(cfgmap.Data, &cfg)
}

// PodInjector isolates the logic around injecting theatre-envconsul away from anything to
// do with mutating webhooks. This makes it easy to unit test without getting tangled in
// webhook noise.
type PodInjector struct {
	InjectorOptions
	VaultConfig
}

// Inject configures the given pod to use theatre-envconsul. If it returns nil, it's
// because the pod isn't configured for injection.
func (i PodInjector) Inject(pod corev1.Pod) *corev1.Pod {
	containerConfigs := parseContainerConfigs(pod)
	if containerConfigs == nil {
		return nil
	}

	mutatedPod := pod.DeepCopy()

	mutatedPod.Spec.InitContainers = append(mutatedPod.Spec.InitContainers, i.buildInitContainer())
	mutatedPod.Spec.Volumes = append(mutatedPod.Spec.Volumes, corev1.Volume{
		Name: "theatre-envconsul-install",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})

	for idx, container := range mutatedPod.Spec.Containers {
		containerConfigPath, ok := containerConfigs[container.Name]
		if !ok {
			continue
		}

		mutatedPod.Spec.Containers[idx] = i.configureContainer(container, containerConfigPath)
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

func (i PodInjector) buildInitContainer() corev1.Container {
	return corev1.Container{
		Name:    "theatre-envconsul-injector",
		Image:   i.Image,
		Command: []string{"theatre-envconsul", "install", "--path", i.InstallPath},
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
func (i PodInjector) configureContainer(reference corev1.Container, containerConfigPath string) corev1.Container {
	c := &reference

	args := []string{"exec"}
	args = append(args, "--install-path", i.InstallPath)
	args = append(args, "--vault-address", i.Address)
	args = append(args, "--auth-backend-mount-path", i.AuthMountPath)
	args = append(args, "--auth-backend-role", i.AuthRole)

	if containerConfigPath != "" {
		args = append(args, "--config-file", containerConfigPath)
	}

	execCommand := []string{"--"}
	execCommand = append(execCommand, reference.Command...)
	execCommand = append(execCommand, reference.Args...)
	args = append(args, execCommand...)

	c.Command = []string{path.Join(i.InstallPath, "theatre-envconsul")}
	c.Args = args

	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
		Name:      "theatre-envconsul-install",
		MountPath: i.InstallPath,
		ReadOnly:  true,
	})

	return *c
}
