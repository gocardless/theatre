package rollback

import (
	"context"
	"encoding/json"

	logr "github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	admission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("RollbackTargetWebhook", func() {
	var (
		ctx        context.Context
		cancel     context.CancelFunc
		scheme     *runtime.Scheme
		fakeClient client.Client
		webhook    *RollbackTargetWebhook
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())

		scheme = runtime.NewScheme()
		Expect(deployv1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	AfterEach(func() {
		cancel()
	})

	Context("when ToReleaseRef is already set", func() {
		It("should allow the request without mutation", func() {
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
			webhook = NewRollbackTargetWebhook(
				logr.New(logr.Discard().GetSink()),
				scheme,
				fakeClient,
			)

			rollback := &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollback",
					Namespace: "default",
				},
				Spec: deployv1alpha1.RollbackSpec{
					ToReleaseRef: deployv1alpha1.ReleaseReference{Name: "my-release-v1"},
					Reason:       "Testing",
				},
			}

			req := reqWithObj(rollback)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Result.Message).To(Equal("ToReleaseRef already set"))
			Expect(len(resp.Patches)).To(Equal(0))
		})
	})

	Context("when ToReleaseRef is not set", func() {
		It("should deny if no active release exists", func() {
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
			webhook = NewRollbackTargetWebhook(
				logr.New(logr.Discard().GetSink()),
				scheme,
				fakeClient,
			)

			rollback := &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollback",
					Namespace: "default",
				},
				Spec: deployv1alpha1.RollbackSpec{
					Reason: "Testing",
				},
			}

			req := reqWithObj(rollback)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).To(ContainSubstring("no active release found"))
		})

		It("should deny if no healthy release exists in the chain", func() {
			// Create an active release with no previous release
			activeRelease := &deployv1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-service-v2",
					Namespace: "default",
				},
				ReleaseConfig: deployv1alpha1.ReleaseConfig{
					TargetName: "my-service",
					Revisions:  []deployv1alpha1.Revision{{Name: "app", ID: "abc123"}},
				},
				Status: deployv1alpha1.ReleaseStatus{
					Conditions: []metav1.Condition{
						{
							Type:   deployv1alpha1.ReleaseConditionActive,
							Status: metav1.ConditionTrue,
						},
					},
					PreviousRelease: deployv1alpha1.ReleaseTransition{},
				},
			}

			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(activeRelease).
				Build()

			webhook = NewRollbackTargetWebhook(
				logr.New(logr.Discard().GetSink()),
				scheme,
				fakeClient,
			)

			rollback := &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollback",
					Namespace: "default",
				},
				Spec: deployv1alpha1.RollbackSpec{
					Reason: "Testing",
				},
			}

			req := reqWithObj(rollback)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).To(ContainSubstring("no healthy release found"))
		})

		It("should set ToReleaseRef to the last healthy release", func() {
			// Create a chain: v1 (healthy) <- v2 (unhealthy) <- v3 (active)
			releaseV1 := &deployv1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-service-v1",
					Namespace: "default",
				},
				ReleaseConfig: deployv1alpha1.ReleaseConfig{
					TargetName: "my-service",
					Revisions:  []deployv1alpha1.Revision{{Name: "app", ID: "abc111"}},
				},
				Status: deployv1alpha1.ReleaseStatus{
					Conditions: []metav1.Condition{
						{
							Type:   deployv1alpha1.ReleaseConditionHealthy,
							Status: metav1.ConditionTrue,
						},
					},
					PreviousRelease: deployv1alpha1.ReleaseTransition{},
				},
			}

			releaseV2 := &deployv1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-service-v2",
					Namespace: "default",
				},
				ReleaseConfig: deployv1alpha1.ReleaseConfig{
					TargetName: "my-service",
					Revisions:  []deployv1alpha1.Revision{{Name: "app", ID: "abc222"}},
				},
				Status: deployv1alpha1.ReleaseStatus{
					Conditions: []metav1.Condition{
						{
							Type:   deployv1alpha1.ReleaseConditionHealthy,
							Status: metav1.ConditionFalse,
						},
					},
					PreviousRelease: deployv1alpha1.ReleaseTransition{
						ReleaseRef: "my-service-v1",
					},
				},
			}

			releaseV3 := &deployv1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-service-v3",
					Namespace: "default",
				},
				ReleaseConfig: deployv1alpha1.ReleaseConfig{
					TargetName: "my-service",
					Revisions:  []deployv1alpha1.Revision{{Name: "app", ID: "abc333"}},
				},
				Status: deployv1alpha1.ReleaseStatus{
					Conditions: []metav1.Condition{
						{
							Type:   deployv1alpha1.ReleaseConditionActive,
							Status: metav1.ConditionTrue,
						},
					},
					PreviousRelease: deployv1alpha1.ReleaseTransition{
						ReleaseRef: "my-service-v2",
					},
				},
			}

			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(releaseV1, releaseV2, releaseV3).
				Build()

			webhook = NewRollbackTargetWebhook(
				logr.New(logr.Discard().GetSink()),
				scheme,
				fakeClient,
			)

			rollback := &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollback",
					Namespace: "default",
				},
				Spec: deployv1alpha1.RollbackSpec{
					Reason: "Testing",
				},
			}

			req := reqWithObj(rollback)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue(), "response: %+v", resp)
			Expect(len(resp.Patches)).To(BeNumerically(">=", 1), "patches: %+v", resp.Patches)

			// Verify the patch sets the correct target release
			var foundTargetRelease bool
			for _, patch := range resp.Patches {
				if patch.Path == "/spec/toReleaseRef/name" {
					Expect(patch.Value).To(Equal("my-service-v1"))
					foundTargetRelease = true
				}
			}
			Expect(foundTargetRelease).To(BeTrue(), "expected patch for /spec/toReleaseRef/name, got patches: %+v", resp.Patches)
		})

		It("should select the immediate previous release if it is healthy", func() {
			// Create a chain: v1 (healthy) <- v2 (healthy) <- v3 (active)
			// Should select v2 as it's the most recent healthy release
			releaseV1 := &deployv1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-service-v1",
					Namespace: "default",
				},
				ReleaseConfig: deployv1alpha1.ReleaseConfig{
					TargetName: "my-service",
					Revisions:  []deployv1alpha1.Revision{{Name: "app", ID: "abc111"}},
				},
				Status: deployv1alpha1.ReleaseStatus{
					Conditions: []metav1.Condition{
						{
							Type:   deployv1alpha1.ReleaseConditionHealthy,
							Status: metav1.ConditionTrue,
						},
					},
					PreviousRelease: deployv1alpha1.ReleaseTransition{},
				},
			}

			releaseV2 := &deployv1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-service-v2",
					Namespace: "default",
				},
				ReleaseConfig: deployv1alpha1.ReleaseConfig{
					TargetName: "my-service",
					Revisions:  []deployv1alpha1.Revision{{Name: "app", ID: "abc222"}},
				},
				Status: deployv1alpha1.ReleaseStatus{
					Conditions: []metav1.Condition{
						{
							Type:   deployv1alpha1.ReleaseConditionHealthy,
							Status: metav1.ConditionTrue,
						},
					},
					PreviousRelease: deployv1alpha1.ReleaseTransition{
						ReleaseRef: "my-service-v1",
					},
				},
			}

			releaseV3 := &deployv1alpha1.Release{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-service-v3",
					Namespace: "default",
				},
				ReleaseConfig: deployv1alpha1.ReleaseConfig{
					TargetName: "my-service",
					Revisions:  []deployv1alpha1.Revision{{Name: "app", ID: "abc333"}},
				},
				Status: deployv1alpha1.ReleaseStatus{
					Conditions: []metav1.Condition{
						{
							Type:   deployv1alpha1.ReleaseConditionActive,
							Status: metav1.ConditionTrue,
						},
					},
					PreviousRelease: deployv1alpha1.ReleaseTransition{
						ReleaseRef: "my-service-v2",
					},
				},
			}

			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(releaseV1, releaseV2, releaseV3).
				Build()

			webhook = NewRollbackTargetWebhook(
				logr.New(logr.Discard().GetSink()),
				scheme,
				fakeClient,
			)

			rollback := &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollback",
					Namespace: "default",
				},
				Spec: deployv1alpha1.RollbackSpec{
					Reason: "Testing",
				},
			}

			req := reqWithObj(rollback)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue())

			// Verify the patch sets v2 as the target (most recent healthy)
			var foundTargetRelease bool
			for _, patch := range resp.Patches {
				if patch.Path == "/spec/toReleaseRef/name" {
					Expect(patch.Value).To(Equal("my-service-v2"))
					foundTargetRelease = true
				}
			}
			Expect(foundTargetRelease).To(BeTrue(), "expected patch for /spec/toReleaseRef/name, got patches: %+v", resp.Patches)
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
