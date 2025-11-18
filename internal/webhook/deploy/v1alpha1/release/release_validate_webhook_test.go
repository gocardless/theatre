package release

import (
	"context"
	"net/http"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	admission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("ReleaseValidateWebhook", func() {

	var (
		ctx                    context.Context
		cancel                 context.CancelFunc
		obj                    *deployv1alpha1.Release
		oldObj                 *deployv1alpha1.Release
		releaseValidateWebhook *ReleaseValidateWebhook
		scheme                 *runtime.Scheme
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
		oldObj = obj.DeepCopy()
		releaseValidateWebhook = NewReleaseValidateWebhook(
			logr.New(logr.Discard().GetSink()),
			scheme,
		)
	})

	AfterEach(func() {
		cancel()
	})

	Context("When updating Release under ReleaseValidate Webhook", func() {
		It("Should fail when modifying spec.utopiaServiceTargetRelease", func() {
			obj.Spec.UtopiaServiceTargetRelease = "other"
			resp := releaseValidateWebhook.Handle(ctx, admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Operation: v1.Update,
					Object:    ObjectToRaw(obj),
					OldObject: ObjectToRaw(oldObj),
				},
			})
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).To(Equal("spec.utopiaServiceTargetRelease is immutable; cannot change from \"default\" to \"other\""))
			Expect(resp.Result.Code).To(Equal(int32(http.StatusBadRequest)))
		})

		It("Should fail when modifying spec.ApplicationRevision.ID", func() {
			obj.Spec.ApplicationRevision.ID = "750601ce98f7fc1309dc6cc060b822d8fcf32523"
			resp := releaseValidateWebhook.Handle(ctx, admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Operation: v1.Update,
					Object:    ObjectToRaw(obj),
					OldObject: ObjectToRaw(oldObj),
				},
			})
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).To(Equal("spec.applicationRevision.id is immutable; cannot change from \"b7717db77fa8f84a87c928033753b8fba38dad0d\" to \"750601ce98f7fc1309dc6cc060b822d8fcf32523\""))
			Expect(resp.Result.Code).To(Equal(int32(http.StatusBadRequest)))
		})

		It("Should fail when modifying spec.InfrastructureRevision.ID", func() {
			obj.Spec.InfrastructureRevision.ID = "750601ce98f7fc1309dc6cc060b822d8fcf32523"
			resp := releaseValidateWebhook.Handle(ctx, admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Operation: v1.Update,
					Object:    ObjectToRaw(obj),
					OldObject: ObjectToRaw(oldObj),
				},
			})
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).To(Equal("spec.InfrastructureRevision.id is immutable; cannot change from \"e083a2cfa34a19f27c4e790ec8eadec49381c288\" to \"750601ce98f7fc1309dc6cc060b822d8fcf32523\""))
			Expect(resp.Result.Code).To(Equal(int32(http.StatusBadRequest)))
		})
	})

	Context("When running a non-update operation", func() {
		It("Should allow the operation", func() {
			resp := releaseValidateWebhook.Handle(ctx, admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Operation: v1.Create,
					Object:    ObjectToRaw(obj),
				},
			})
			Expect(resp.Allowed).To(BeTrue())
		})
	})

	Context("When running any operation", func() {
		It("Should allow labels and annotations to be updated", func() {
			obj.Labels = map[string]string{"foo": "bar"}
			obj.Annotations = map[string]string{"foo": "bar"}
			resp := releaseValidateWebhook.Handle(ctx, admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Operation: v1.Update,
					Object:    ObjectToRaw(obj),
					OldObject: ObjectToRaw(oldObj),
				},
			})
			Expect(resp.Allowed).To(BeTrue())
		})
	})
})
