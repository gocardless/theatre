package envconsul

import (
	"fmt"
	"io/ioutil"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

func mustPodFixture(path string) *corev1.Pod {
	podFixtureYAML, _ := ioutil.ReadFile(path)
	decoder := scheme.Codecs.UniversalDeserializer()
	obj, _, _ := decoder.Decode(podFixtureYAML, nil, nil)
	return obj.(*corev1.Pod)
}

var _ = Describe("PodInjector", func() {
	var (
		injector *PodInjector
		fixture  *corev1.Pod
		pod      *corev1.Pod
	)

	JustBeforeEach(func() {
		pod = injector.Inject(*fixture)
	})

	BeforeEach(func() {
		injector = &PodInjector{
			VaultConfig: VaultConfig{
				Address:       "https://vault.example.com",
				AuthMountPath: "kubernetes.gc-prd-effc.cluster",
				AuthRole:      "default",
			},
			InjectorOptions: InjectorOptions{
				Image:       "theatre:latest",
				InstallPath: "/var/run/theatre-envconsul",
				VaultConfigMapKey: client.ObjectKey{
					Namespace: "vault-system",
					Name:      "vault-config",
				},
				ServiceAccountTokenFile:     "/var/run/secrets/kubernetes.io/vault/token",
				ServiceAccountTokenExpiry:   15 * time.Minute,
				ServiceAccountTokenAudience: "",
			},
		}
	})

	Context("Pod with no annotations", func() {
		BeforeEach(func() {
			fixture = mustPodFixture("./testdata/no_annotations_pod.yaml")
		})

		It("Returns unmutated pod", func() {
			Expect(pod).To(BeNil(), "expected nil, as it isn't annotated for mutation")
		})
	})

	Context("Pod with annotation and no config path", func() {
		BeforeEach(func() {
			fixture = mustPodFixture("./testdata/app_no_config_pod.yaml")
		})

		It("Injects init container", func() {
			Expect(pod.Spec.InitContainers).To(
				ContainElement(
					MatchFields(
						IgnoreExtras, Fields{
							"Name":  Equal("theatre-envconsul-injector"),
							"Image": Equal("theatre:latest"),
							"Command": Equal([]string{
								"theatre-envconsul", "install", "--path", "/var/run/theatre-envconsul",
							}),
						},
					),
				),
			)
		})

		It("Adds service account volume", func() {
			var projection *corev1.ServiceAccountTokenProjection

			for _, volume := range pod.Spec.Volumes {
				if volume.Name != "theatre-envconsul-serviceaccount" {
					continue
				}

				projection = volume.VolumeSource.Projected.Sources[0].ServiceAccountToken
			}

			Expect(projection).To(
				PointTo(
					MatchFields(IgnoreExtras, Fields{
						"Path":              Equal("token"),
						"ExpirationSeconds": PointTo(BeEquivalentTo(900)),
						"Audience":          Equal(""),
					}),
				),
			)
		})

		It("Modifies command to prefix theatre-envconsul", func() {
			Expect(pod.Spec.Containers).To(
				ContainElement(
					MatchFields(
						IgnoreExtras, Fields{
							"Name": Equal("app"),
							"Command": Equal([]string{
								"/var/run/theatre-envconsul/theatre-envconsul",
							}),
							"Args": Equal([]string{
								"exec",
								"--install-path",
								"/var/run/theatre-envconsul",
								"--vault-address",
								"https://vault.example.com",
								"--auth-backend-mount-path",
								"kubernetes.gc-prd-effc.cluster",
								"--auth-backend-role",
								"default",
								"--service-account-token-file",
								"/var/run/secrets/kubernetes.io/vault/token",
								"--",
								"echo",
								"inject",
								"only",
							}),
						},
					),
				),
			)
		})

		It("Preserves original app volumeMount", func() {
			Expect(pod.Spec.Containers).To(
				ContainElement(
					MatchFields(
						IgnoreExtras, Fields{
							"Name": Equal("app"),
							"VolumeMounts": ContainElement(
								corev1.VolumeMount{
									Name:      "app-volume",
									MountPath: "/app/path",
								},
							),
						},
					),
				),
			)
		})

		It("Adds theatre-envconsul-install volumeMount", func() {
			Expect(pod.Spec.Containers).To(
				ContainElement(
					MatchFields(
						IgnoreExtras, Fields{
							"Name": Equal("app"),
							"VolumeMounts": ContainElement(
								corev1.VolumeMount{
									Name:      "theatre-envconsul-install",
									MountPath: "/var/run/theatre-envconsul",
									ReadOnly:  true,
								},
							),
						},
					),
				),
			)
		})

		It("Adds service account volumeMount", func() {
			Expect(pod.Spec.Containers).To(
				ContainElement(
					MatchFields(
						IgnoreExtras, Fields{
							"Name": Equal("app"),
							"VolumeMounts": ContainElement(
								corev1.VolumeMount{
									Name:      "theatre-envconsul-serviceaccount",
									MountPath: "/var/run/secrets/kubernetes.io/vault",
									ReadOnly:  true,
								},
							),
						},
					),
				),
			)
		})

		Context("With multiple containers", func() {
			var extraContainer = corev1.Container{
				Name: "extra",
				Command: []string{
					"serve",
				},
			}

			BeforeEach(func() {
				fixture.Spec.Containers = append(fixture.Spec.Containers, extraContainer)
			})

			It("Doesn't inject anything into extra container", func() {
				Expect(pod.Spec.Containers[1]).To(Equal(extraContainer))
			})
		})
	})

	Context("Pod with annotation and config path", func() {
		BeforeEach(func() {
			fixture = mustPodFixture("./testdata/app_with_config_pod.yaml")
		})

		It("Modifies command to prefix theatre-envconsul with config path", func() {
			Expect(pod.Spec.Containers).To(
				ContainElement(
					MatchFields(
						IgnoreExtras, Fields{
							"Name": Equal("app"),
							"Command": Equal([]string{
								"/var/run/theatre-envconsul/theatre-envconsul",
							}),
							"Args": Equal([]string{
								"exec",
								"--install-path",
								"/var/run/theatre-envconsul",
								"--vault-address",
								"https://vault.example.com",
								"--auth-backend-mount-path",
								"kubernetes.gc-prd-effc.cluster",
								"--auth-backend-role",
								"default",
								"--service-account-token-file",
								"/var/run/secrets/kubernetes.io/vault/token",
								"--config-file",
								"config/app.yaml",
								"--",
								"echo",
								"inject",
								"only",
							}),
						},
					),
				),
			)
		})
	})
})

var _ = Describe("parseContainerConfigs", func() {
	var (
		fixture          *corev1.Pod
		containerConfigs map[string]string
	)

	JustBeforeEach(func() {
		containerConfigs = parseContainerConfigs(*fixture)
	})

	Context("With valid config", func() {
		BeforeEach(func() {
			fixture = &corev1.Pod{}
		})

		Context("With app with no config path", func() {
			BeforeEach(func() {
				fixture.ObjectMeta.Annotations = map[string]string{fmt.Sprintf("%s/configs", EnvconsulInjectorFQDN): "app"}
			})

			It("Returns app with no config path", func() {
				Expect(containerConfigs).To(Equal(map[string]string{"app": ""}))
			})
		})

		Context("With app with config path", func() {
			BeforeEach(func() {
				fixture.ObjectMeta.Annotations = map[string]string{fmt.Sprintf("%s/configs", EnvconsulInjectorFQDN): "app:path/to/config.yaml"}
			})

			It("Returns app with config path", func() {
				Expect(containerConfigs).To(Equal(map[string]string{"app": "path/to/config.yaml"}))
			})
		})

		Context("With app with spaces in config path", func() {
			BeforeEach(func() {
				fixture.ObjectMeta.Annotations = map[string]string{fmt.Sprintf("%s/configs", EnvconsulInjectorFQDN): " app : path/to/config.yaml"}
			})

			It("Returns app with config path with spaces stripped", func() {
				Expect(containerConfigs).To(Equal(map[string]string{"app": "path/to/config.yaml"}))
			})
		})

		Context("With multiple apps with and without config", func() {
			BeforeEach(func() {
				fixture.ObjectMeta.Annotations = map[string]string{fmt.Sprintf("%s/configs", EnvconsulInjectorFQDN): "app: path/to/config.yaml, app2, app3: path/to/config3.yaml"}
			})

			It("Returns multiple apps with and without config", func() {
				Expect(containerConfigs).To(Equal(map[string]string{"app": "path/to/config.yaml", "app2": "", "app3": "path/to/config3.yaml"}))
			})
		})

	})
})
