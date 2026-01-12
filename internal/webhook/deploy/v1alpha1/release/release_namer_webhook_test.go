package release

import (
	"context"
	"encoding/json"
	"net/http"

	logr "github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/deploy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	admission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("ReleaseNamerWebhook", func() {
	var (
		ctx                 context.Context
		cancel              context.CancelFunc
		obj                 *deployv1alpha1.Release
		releaseNamerWebhook *ReleaseNamerWebhook
		scheme              *runtime.Scheme
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())

		scheme = runtime.NewScheme()
		Expect(deployv1alpha1.AddToScheme(scheme)).To(Succeed())

		obj = &deployv1alpha1.Release{
			ReleaseConfig: deployv1alpha1.ReleaseConfig{
				TargetName: "test-target",
				Revisions: []deployv1alpha1.Revision{
					{
						Name:   "infrastructure-revision",
						ID:     "sha123",
						Source: "https://github.com/example/repo",
						Type:   "git",
					},
					{
						Name:   "application-revision",
						ID:     "sha256:abc123",
						Source: "registry.example.com/image:v1.0.0",
						Type:   "container_image",
					},
				},
			},
		}

		releaseNamerWebhook = NewReleaseNamerWebhook(
			logr.New(logr.Discard().GetSink()),
			scheme,
		)
	})

	AfterEach(func() {
		cancel()
	})

	Context("Handle Admission Request", func() {
		It("Should process a valid release request", func() {
			req := reqWithObj(obj)
			resp := releaseNamerWebhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(len(resp.Patches)).To(BeNumerically(">=", 1))
			// Check that the patch includes the metadata field
			Expect(resp.Patches[0].Path).To(Equal("/metadata"))
			// Check that the patch modifies the name field
			Expect(resp.Patches[0].Value).To(HaveKey("name"))
			// Check that the name is set
			nameValue, ok := resp.Patches[0].Value.(map[string]interface{})["name"]
			Expect(ok).To(BeTrue())
			Expect(nameValue).NotTo(BeEmpty())
		})

		It("Should exit early if release name is already set", func() {
			name, err := deploy.GenerateReleaseName(*obj)
			Expect(err).ToNot(HaveOccurred())
			obj.Name = name

			req := reqWithObj(obj)
			resp := releaseNamerWebhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Result.Message).To(Equal("Release name already set"))
			// Should not add any patches since name is already set
			Expect(len(resp.Patches)).To(Equal(0))
			Expect(resp.Result.Code).To(Equal(int32(http.StatusOK)))
		})

		It("Should rename release when name is already set but invalid", func() {
			obj.Name = "invalid-name-with-special-chars"

			generatedName, err := deploy.GenerateReleaseName(*obj)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(generatedName)).To(BeNumerically(">", 0))

			req := reqWithObj(obj)
			resp := releaseNamerWebhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(len(resp.Patches)).To(BeNumerically(">=", 1))
			Expect(resp.Patches[0].Path).To(Equal("/metadata/name"))
			Expect(resp.Patches[0].Value).To(Equal(generatedName))
		})

		It("Should error out if revisions or targetName are invalid", func() {
			invalidRevisionObj := obj.DeepCopy()
			invalidRevisionObj.Revisions = []deployv1alpha1.Revision{}
			req := reqWithObj(invalidRevisionObj)
			resp := releaseNamerWebhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Code).To(Equal(int32(http.StatusBadRequest)))

			invalidTargetName := obj.DeepCopy()
			invalidTargetName.TargetName = ""
			req2 := reqWithObj(invalidTargetName)
			resp2 := releaseNamerWebhook.Handle(ctx, req2)
			Expect(resp2.Allowed).To(BeFalse())
			Expect(resp2.Result.Code).To(Equal(int32(http.StatusBadRequest)))
		})
	})
})

func reqWithObj(obj runtime.Object) admission.Request {
	return admission.Request{AdmissionRequest: v1.AdmissionRequest{
		Object: objectToRaw(obj),
	}}
}

func objectToRaw(obj runtime.Object) runtime.RawExtension {
	objRaw, err := json.Marshal(obj)
	Expect(err).ToNot(HaveOccurred())
	return runtime.RawExtension{
		Raw: objRaw,
	}
}
