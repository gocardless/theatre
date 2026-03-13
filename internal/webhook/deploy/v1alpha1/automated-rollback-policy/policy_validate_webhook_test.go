package automatedrollbackpolicy

import (
	"context"
	"encoding/json"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	deploy "github.com/gocardless/theatre/v5/internal/controller/deploy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("AutomatedRollbackPolicyValidateWebhook", func() {
	var (
		ctx        context.Context
		cancel     context.CancelFunc
		scheme     *runtime.Scheme
		fakeClient client.Client
		webhook    *AutomatedRollbackPolicyValidateWebhook
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())

		scheme = runtime.NewScheme()
		Expect(deployv1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	setupFakeClientWithIndex := func(objects ...client.Object) client.Client {
		return fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objects...).
			WithIndex(&deployv1alpha1.AutomatedRollbackPolicy{}, deploy.IndexFieldPolicyTargetName, func(obj client.Object) []string {
				policy := obj.(*deployv1alpha1.AutomatedRollbackPolicy)
				return []string{policy.Spec.TargetName}
			}).
			Build()
	}

	AfterEach(func() {
		cancel()
	})

	It("should allow new policies to be created, if no policies with the same target exist", func() {
		differentTargetPolicy := newAutomatedRollbackPolicy("different-policy", "different-target")
		fakeClient = setupFakeClientWithIndex(differentTargetPolicy)

		webhook = NewAutomatedRollbackPolicyValidateWebhook(logr.New(logr.Discard().GetSink()), scheme, fakeClient)
		req := reqWithObj(newAutomatedRollbackPolicy("test", "test-target"))

		resp := webhook.Handle(ctx, req)
		Expect(resp.Allowed).To(BeTrue())
	})

	It("should deny new policies if a policy with the same target already exists", func() {
		existingPolicy := newAutomatedRollbackPolicy("existing-policy", "existing-target")
		fakeClient = setupFakeClientWithIndex(existingPolicy)

		webhook = NewAutomatedRollbackPolicyValidateWebhook(logr.New(logr.Discard().GetSink()), scheme, fakeClient)
		req := reqWithObj(newAutomatedRollbackPolicy("test", "existing-target"))

		resp := webhook.Handle(ctx, req)
		Expect(resp.Allowed).To(BeFalse())
	})
})

func newAutomatedRollbackPolicy(
	name string,
	target string,
) *deployv1alpha1.AutomatedRollbackPolicy {
	return &deployv1alpha1.AutomatedRollbackPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: deployv1alpha1.AutomatedRollbackPolicySpec{
			TargetName: target,
		},
	}
}

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
