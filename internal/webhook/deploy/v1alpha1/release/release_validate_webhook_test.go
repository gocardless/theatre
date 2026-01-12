package release

import (
	"context"
	"net/http"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
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
			ReleaseConfig: deployv1alpha1.ReleaseConfig{
				TargetName: "default",
				Revisions: []deployv1alpha1.Revision{
					{Name: "application", ID: "test-id"},
					{Name: "infrastructure", ID: "test-id-2"},
				},
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
		It("Should fail when modifying targetName", func() {
			obj.ReleaseConfig.TargetName = "other"
			resp := releaseValidateWebhook.Handle(ctx, admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Operation: v1.Update,
					Object:    objectToRaw(obj),
					OldObject: objectToRaw(oldObj),
				},
			})
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).To(ContainSubstring("release .config.targetName, config.revision[].name and config.revision[].id are immutable"))
			Expect(resp.Result.Code).To(Equal(int32(http.StatusBadRequest)))
		})

		It("Should fail when modifying the revisions array", func() {
			obj.ReleaseConfig.Revisions[0].ID = "750601ce98f7fc1309dc6cc060b822d8fcf32523"

			resp := releaseValidateWebhook.Handle(ctx, admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Operation: v1.Update,
					Object:    objectToRaw(obj),
					OldObject: objectToRaw(oldObj),
				},
			})
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).To(ContainSubstring("release .config.targetName, config.revision[].name and config.revision[].id are immutable"))
			Expect(resp.Result.Code).To(Equal(int32(http.StatusBadRequest)))
		})

		It("Should allow labels and annotations to be updated", func() {
			obj.Labels = map[string]string{"foo": "bar"}
			obj.Annotations = map[string]string{"foo": "bar"}
			resp := releaseValidateWebhook.Handle(ctx, admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Operation: v1.Update,
					Object:    objectToRaw(obj),
					OldObject: objectToRaw(oldObj),
				},
			})
			Expect(resp.Allowed).To(BeTrue())
		})
	})

	Context("When running a non-update operation", func() {
		It("Should allow the operation", func() {
			resp := releaseValidateWebhook.Handle(ctx, admission.Request{
				AdmissionRequest: v1.AdmissionRequest{
					Operation: v1.Create,
					Object:    objectToRaw(obj),
				},
			})
			Expect(resp.Allowed).To(BeTrue())
		})
	})
})
