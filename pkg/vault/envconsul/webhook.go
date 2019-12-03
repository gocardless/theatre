package envconsul

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"path"
	"strings"
	"time"

	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	typedCorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/types"
)

const EnvconsulInjectorFQDN = "envconsul-injector.vault.crd.gocardless.com"

func NewWebhook(logger kitlog.Logger, injectorOpts InjectorOptions) (*Injector, error) {
	scheme := runtime.NewScheme()
	codecs := serializer.NewCodecFactory(scheme)

	admissionDecoder, _ := admission.NewDecoder(scheme)

	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster config")
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create kubernetes client")
	}

	return &Injector{
		logger:           kitlog.With(logger, "component", "EnvconsulInjector"),
		requestDecoder:   codecs.UniversalDeserializer(),
		admissionDecoder: admissionDecoder,
		client:           client.CoreV1(),
		opts:             injectorOpts,
	}, nil
}

type Injector struct {
	logger           kitlog.Logger
	requestDecoder   runtime.Decoder
	admissionDecoder types.Decoder
	client           typedCorev1.CoreV1Interface
	opts             InjectorOptions
}

type InjectorOptions struct {
	Image                   string // image of theatre to use when constructing pod
	InstallPath             string // location of vault installation directory
	VaultConfigMapNamespace string
	VaultConfigMapName      string
}

func (i *Injector) Handle(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if data, err := ioutil.ReadAll(r.Body); err == nil {
		body = data
	}

	var admissionResponse *v1beta1.AdmissionResponse
	ar := v1beta1.AdmissionReview{}
	if _, _, err := i.requestDecoder.Decode(body, nil, &ar); err != nil {
		level.Error(i.logger).Log("failed to decode webhook request body: %v", err)
		admissionResponse = &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		admissionResponse = i.mutate(r.Context(), types.Request{AdmissionRequest: ar.Request}).Response
	}

	admissionReview := v1beta1.AdmissionReview{}
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
		}
	}

	response, err := json.Marshal(admissionReview)
	if err != nil {
		level.Error(i.logger).Log("failed to encode webhook response: %v", err)
		http.Error(w, fmt.Sprintf("failed to encode webhook response: %v", err), http.StatusInternalServerError)
	}
	if _, err := w.Write(response); err != nil {
		level.Error(i.logger).Log("failed to write webhook response: %v", err)
		http.Error(w, fmt.Sprintf("failed to write webhook response: %v", err), http.StatusInternalServerError)
	}
}

func (i *Injector) mutate(ctx context.Context, req types.Request) types.Response {
	logger := kitlog.With(i.logger, "uuid", string(req.AdmissionRequest.UID))
	logger.Log("event", "request.start")
	defer func(start time.Time) {
		logger.Log("event", "request.end", "duration", time.Since(start).Seconds())
	}(time.Now())

	pod := &corev1.Pod{}
	if err := i.admissionDecoder.Decode(req, pod); err != nil {
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

	vaultConfigMap, err := i.client.ConfigMaps(i.opts.VaultConfigMapNamespace).Get(i.opts.VaultConfigMapName, metav1.GetOptions{})
	if err != nil {
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
