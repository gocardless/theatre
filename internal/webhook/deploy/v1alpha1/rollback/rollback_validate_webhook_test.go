package rollback

import (
	"context"

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
	admission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("RollbackValidateWebhook", func() {
	var (
		ctx        context.Context
		cancel     context.CancelFunc
		scheme     *runtime.Scheme
		fakeClient client.Client
		webhook    *RollbackValidateWebhook
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
			WithIndex(&deployv1alpha1.Rollback{}, deploy.IndexFieldRollbackTarget, func(obj client.Object) []string {
				rollback := obj.(*deployv1alpha1.Rollback)
				return []string{rollback.Spec.ToReleaseRef.Target}
			}).
			Build()
	}

	AfterEach(func() {
		cancel()
	})

	Context("Requests with operation other than create", func() {
		DescribeTable("should bypass any non-create operations",
			func(operation v1.Operation) {
				req := admission.Request{
					AdmissionRequest: v1.AdmissionRequest{
						Operation: operation,
					},
				}

				fakeClient = setupFakeClientWithIndex()
				webhook = NewRollbackValidateWebhook(logr.New(logr.Discard().GetSink()), scheme, fakeClient)
				resp := webhook.Handle(ctx, req)
				Expect(resp.Allowed).To(BeTrue())
				Expect(resp.Result.Message).To(Equal("only create operations are validated"))
			},

			Entry("update requests", v1.Update),
			Entry("delete requests", v1.Delete),
			Entry("connect requests", v1.Connect),
		)
	})

	Context("Handle requests with create operation", func() {
		It("should allow create requests if there aren't any in progress", func() {
			rollback1 := newRollback("my-service-rollback-1", "my-service", false)
			fakeClient = setupFakeClientWithIndex(rollback1)
			webhook = NewRollbackValidateWebhook(logr.New(logr.Discard().GetSink()), scheme, fakeClient)

			newRollback := newRollback("my-service-rollback-2", "my-service", true)

			req := reqWithObj(newRollback, v1.Create)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Result.Message).To(Equal("allowed"))
		})

		It("should deny create requests if there is an in progress rollback for the target", func() {
			rollback1 := newRollback("my-service-rollback-1", "my-service", true)
			fakeClient = setupFakeClientWithIndex(rollback1)
			webhook = NewRollbackValidateWebhook(logr.New(logr.Discard().GetSink()), scheme, fakeClient)

			newRollback := newRollback("my-service-rollback-2", "my-service", true)

			req := reqWithObj(newRollback, v1.Create)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).To(Equal("another rollback in progress for target \"my-service\""))
		})
	})
})

func newRollback(
	name string,
	target string,
	inProgress bool,
) *deployv1alpha1.Rollback {
	conditions := []metav1.Condition{
		{
			Type: deployv1alpha1.RollbackConditionInProgress,
			Status: func() metav1.ConditionStatus {
				if inProgress {
					return metav1.ConditionTrue
				}
				return metav1.ConditionFalse
			}(),
		},
	}

	return &deployv1alpha1.Rollback{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: deployv1alpha1.RollbackSpec{
			ToReleaseRef: deployv1alpha1.ReleaseReference{
				Target: target,
			},
		},
		Status: deployv1alpha1.RollbackStatus{
			Conditions: conditions,
		},
	}
}
