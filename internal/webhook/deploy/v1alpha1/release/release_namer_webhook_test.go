package release

import (
	"context"
	"net/http"

	logr "github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	consolev1alpha1 "github.com/gocardless/theatre/v5/api/workloads/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	admission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("ReleaseNamerWebhook", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		obj    *deployv1alpha1.Release
		// oldObj *deployv1alpha1.Release
		releaseNamerWebhook *ReleaseNamerWebhook
		scheme              *runtime.Scheme
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())

		scheme = runtime.NewScheme()
		Expect(deployv1alpha1.AddToScheme(scheme)).To(Succeed())

		obj = &deployv1alpha1.Release{
			Spec: deployv1alpha1.ReleaseSpec{
				UtopiaServiceTargetRelease: "default",
				InfrastructureRevision:     deployv1alpha1.Revision{ID: "e083a2cfa34a19f27c4e790ec8eadec49381c288"},
				ApplicationRevision:        deployv1alpha1.Revision{ID: "b7717db77fa8f84a87c928033753b8fba38dad0d"},
			},
		}

		// oldObj = &deployv1alpha1.Release{}
		releaseNamerWebhook = NewReleaseNamerWebhook(
			logr.New(logr.Discard().GetSink()),
			scheme,
		)
	})

	AfterEach(func() {
		cancel()
	})

	Context("When creating Release under ReleaseNamer Webhook", func() {
		Context("Testing generateName function", func() {
			It("Should fail when required when spec is empty", func() {
				obj.Spec = deployv1alpha1.ReleaseSpec{}
				_, err := getReleaseName(*obj)
				Expect(err).To(HaveOccurred())
			})

			It("Should fail if utopiaServiceTargetRelease is empty", func() {
				obj.Spec.UtopiaServiceTargetRelease = ""
				_, err := getReleaseName(*obj)
				Expect(err).To(HaveOccurred())
			})

			It("Should fail if infrastructureRevision is empty", func() {
				obj.Spec.InfrastructureRevision = deployv1alpha1.Revision{}
				_, err := getReleaseName(*obj)
				Expect(err).To(HaveOccurred())
			})

			It("Should fail if applicationRevision is empty", func() {
				obj.Spec.ApplicationRevision = deployv1alpha1.Revision{}
				_, err := getReleaseName(*obj)
				Expect(err).To(HaveOccurred())
			})

			It("Should generate a name if all required fields are present", func() {
				name, err := getReleaseName(*obj)
				Expect(err).ToNot(HaveOccurred())
				Expect(name).To(Equal("default-e083a2c-b7717db"))
			})
		})

		Context("Testing Handle function", func() {
			It("Should successfully alter the object name if all fields are provided", func() {
				resp := releaseNamerWebhook.Handle(ctx, admission.Request{
					AdmissionRequest: v1.AdmissionRequest{
						Object: runtime.RawExtension{
							Object: obj,
						},
					},
				})
				Expect(resp.Allowed).To(BeTrue())
				Expect(resp.Patches).To(HaveLen(1))
				Expect(resp.Patches[0].Path).To(Equal("/metadata/name"))
				Expect(resp.Patches[0].Value).To(Equal("default-e083a2c-b7717db"))
			})

			It("Should fail if required fields are not provided", func() {
				// obj = &deployv1alpha1.Release{}
				resp := releaseNamerWebhook.Handle(ctx, admission.Request{
					AdmissionRequest: v1.AdmissionRequest{
						Object: runtime.RawExtension{
							Object: obj,
						},
					},
				})
				Expect(resp.Allowed).To(BeFalse())
				Expect(resp.Result.Message).To(Equal("missing required fields: utopiaServiceTargetRelease, applicationRevision, infrastructureRevision"))
				Expect(resp.Result.Code).To(Equal(int32(http.StatusBadRequest)))
			})

			It("Should fail if object in request is not a Release", func() {
				resp := releaseNamerWebhook.Handle(ctx, admission.Request{
					AdmissionRequest: v1.AdmissionRequest{
						Object: runtime.RawExtension{
							Object: &consolev1alpha1.Console{},
						},
					},
				})
				Expect(resp.Allowed).To(BeFalse())
				Expect(resp.Result.Code).To(Equal(int32(http.StatusBadRequest)))
			})
		})
	})
})
