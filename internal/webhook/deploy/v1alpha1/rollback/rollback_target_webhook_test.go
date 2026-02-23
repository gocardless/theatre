package rollback

import (
	"context"
	"encoding/json"

	logr "github.com/go-logr/logr"
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

const namespace = "default"

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

	setupFakeClientWithIndex := func(objects ...client.Object) client.Client {
		return fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objects...).
			WithIndex(&deployv1alpha1.Release{}, deploy.IndexFieldReleaseTarget, func(obj client.Object) []string {
				release := obj.(*deployv1alpha1.Release)
				return []string{release.ReleaseConfig.TargetName}
			}).
			Build()
	}

	AfterEach(func() {
		cancel()
	})

	Context("when ToReleaseRef is already set", func() {
		It("should set owner reference to the active release", func() {
			// Seed a release matching the ToReleaseRef so validation passes
			activeRelease := newRelease(
				"my-release-v2",
				true, // active
				true, // healthy
				"",
				nil,
			)

			targetRelease := newRelease(
				"my-release-v1",
				false, // active
				true,  // healthy
				"",
				nil,
			)

			fakeClient = setupFakeClientWithIndex(activeRelease, targetRelease)

			webhook = NewRollbackTargetWebhook(
				logr.New(logr.Discard().GetSink()),
				scheme,
				fakeClient,
			)

			rollback := &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollback",
					Namespace: namespace,
				},
				Spec: deployv1alpha1.RollbackSpec{
					ToReleaseRef: deployv1alpha1.ReleaseReference{
						Target: "my-service",
						Name:   "my-release-v1",
					},
					Reason: "Testing",
				},
			}

			req := reqWithObj(rollback)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(len(resp.Patches)).To(BeNumerically(">=", 1), "expected patches for owner reference")

			// Verify owner reference is set in the patches
			var foundOwnerRef bool
			for _, patch := range resp.Patches {
				if patch.Path == "/metadata/ownerReferences" {
					foundOwnerRef = true
					// Verify it's an array with at least one owner reference
					Expect(patch.Value).ToNot(BeNil())
					Expect(patch.Value).To(ContainElement(HaveKeyWithValue("name", activeRelease.Name)))
				}
			}
			Expect(foundOwnerRef).To(BeTrue(), "expected patch for /metadata/ownerReferences, got patches: %+v", resp.Patches)
		})
	})

	Context("when ToReleaseRef is not set", func() {
		It("should deny if no active release exists", func() {
			fakeClient = setupFakeClientWithIndex()
			webhook = NewRollbackTargetWebhook(
				logr.New(logr.Discard().GetSink()),
				scheme,
				fakeClient,
			)

			rollback := &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollback",
					Namespace: namespace,
				},
				Spec: deployv1alpha1.RollbackSpec{
					ToReleaseRef: deployv1alpha1.ReleaseReference{
						Target: "my-service",
					},
					Reason: "Testing",
				},
			}

			req := reqWithObj(rollback)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Message).To(ContainSubstring("no releases found for target"))
		})

		It("should deny if no healthy release exists in the chain", func() {
			// Create an active release with no previous release
			activeRelease := newRelease(
				"my-service-v2",
				true,  // active
				false, // healthy
				"",
				[]deployv1alpha1.Revision{{Name: "app", ID: "abc123"}},
			)

			fakeClient = setupFakeClientWithIndex(activeRelease)

			webhook = NewRollbackTargetWebhook(
				logr.New(logr.Discard().GetSink()),
				scheme,
				fakeClient,
			)

			rollback := &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollback",
					Namespace: namespace,
				},
				Spec: deployv1alpha1.RollbackSpec{
					ToReleaseRef: deployv1alpha1.ReleaseReference{
						Target: "my-service",
					},
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
			releaseV1 := newRelease(
				"my-service-v1",
				false, // active
				true,  // healthy
				"",
				[]deployv1alpha1.Revision{{Name: "app", ID: "abc111"}},
			)

			releaseV2 := newRelease(
				"my-service-v2",
				false, // active
				false, // healthy
				"my-service-v1",
				[]deployv1alpha1.Revision{{Name: "app", ID: "abc222"}},
			)

			releaseV3 := newRelease(
				"my-service-v3",
				true,  // active
				false, // healthy
				"my-service-v2",
				[]deployv1alpha1.Revision{{Name: "app", ID: "abc333"}},
			)

			fakeClient = setupFakeClientWithIndex(releaseV1, releaseV2, releaseV3)

			webhook = NewRollbackTargetWebhook(
				logr.New(logr.Discard().GetSink()),
				scheme,
				fakeClient,
			)

			rollback := &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollback",
					Namespace: namespace,
				},
				Spec: deployv1alpha1.RollbackSpec{
					ToReleaseRef: deployv1alpha1.ReleaseReference{
						Target: "my-service",
					},
					Reason: "Testing",
				},
			}

			req := reqWithObj(rollback)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue(), "response: %+v", resp)
			Expect(len(resp.Patches)).To(BeNumerically(">=", 1), "patches: %+v", resp.Patches)

			// Verify the patch sets the correct target release
			var foundTargetRelease bool
			var foundOwnerRef bool
			for _, patch := range resp.Patches {
				if patch.Path == "/spec/toReleaseRef/name" {
					Expect(patch.Value).To(Equal("my-service-v1"))
					foundTargetRelease = true
				}
				if patch.Path == "/metadata/ownerReferences" {
					foundOwnerRef = true
					Expect(patch.Value).ToNot(BeNil())
				}
			}
			Expect(foundTargetRelease).To(BeTrue(), "expected patch for /spec/toReleaseRef/name, got patches: %+v", resp.Patches)
			Expect(foundOwnerRef).To(BeTrue(), "expected patch for /metadata/ownerReferences, got patches: %+v", resp.Patches)
		})

		It("should select the immediate previous release if it is healthy", func() {
			// Create a chain: v1 (healthy) <- v2 (healthy) <- v3 (active)
			// Should select v2 as it's the most recent healthy release
			releaseV1 := newRelease(
				"my-service-v1",
				false, // active
				true,  // healthy
				"",
				[]deployv1alpha1.Revision{{Name: "app", ID: "abc111"}},
			)

			releaseV2 := newRelease(
				"my-service-v2",
				false, // active
				true,  // healthy
				"my-service-v1",
				[]deployv1alpha1.Revision{{Name: "app", ID: "abc222"}},
			)

			releaseV3 := newRelease(
				"my-service-v3",
				true,  // active
				false, // healthy
				"my-service-v2",
				[]deployv1alpha1.Revision{{Name: "app", ID: "abc333"}},
			)

			fakeClient = setupFakeClientWithIndex(releaseV1, releaseV2, releaseV3)

			webhook = NewRollbackTargetWebhook(
				logr.New(logr.Discard().GetSink()),
				scheme,
				fakeClient,
			)

			rollback := &deployv1alpha1.Rollback{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollback",
					Namespace: namespace,
				},
				Spec: deployv1alpha1.RollbackSpec{
					ToReleaseRef: deployv1alpha1.ReleaseReference{
						Target: "my-service",
					},
					Reason: "Testing",
				},
			}

			req := reqWithObj(rollback)
			resp := webhook.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue())

			// Verify the patch sets v2 as the target (most recent healthy)
			var foundTargetRelease bool
			var foundOwnerRef bool
			for _, patch := range resp.Patches {
				if patch.Path == "/spec/toReleaseRef/name" {
					Expect(patch.Value).To(Equal("my-service-v2"))
					foundTargetRelease = true
				}
				if patch.Path == "/metadata/ownerReferences" {
					foundOwnerRef = true
					Expect(patch.Value).ToNot(BeNil())
				}
			}
			Expect(foundTargetRelease).To(BeTrue(), "expected patch for /spec/toReleaseRef/name, got patches: %+v", resp.Patches)
			Expect(foundOwnerRef).To(BeTrue(), "expected patch for /metadata/ownerReferences, got patches: %+v", resp.Patches)
		})
	})
})

func reqWithObj(obj runtime.Object) admission.Request {
	return admission.Request{AdmissionRequest: v1.AdmissionRequest{
		Namespace: namespace,
		Object:    objectToRaw(obj),
	}}
}

func objectToRaw(obj runtime.Object) runtime.RawExtension {
	objRaw, err := json.Marshal(obj)
	Expect(err).ToNot(HaveOccurred())
	return runtime.RawExtension{
		Raw: objRaw,
	}
}

func newRelease(
	name string,
	isActive bool,
	isHealthy bool,
	previousRef string,
	revisions []deployv1alpha1.Revision,
) *deployv1alpha1.Release {
	conditions := []metav1.Condition{
		{
			Type: deployv1alpha1.ReleaseConditionActive,
			Status: func() metav1.ConditionStatus {
				if isActive {
					return metav1.ConditionTrue
				}
				return metav1.ConditionFalse
			}(),
		},
		{
			Type: deployv1alpha1.ReleaseConditionHealthy,
			Status: func() metav1.ConditionStatus {
				if isHealthy {
					return metav1.ConditionTrue
				}
				return metav1.ConditionFalse
			}(),
		},
	}

	return &deployv1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		ReleaseConfig: deployv1alpha1.ReleaseConfig{
			TargetName: "my-service",
			Revisions:  revisions,
		},
		Status: deployv1alpha1.ReleaseStatus{
			Conditions:      conditions,
			PreviousRelease: deployv1alpha1.ReleaseTransition{ReleaseRef: previousRef},
		},
	}
}
