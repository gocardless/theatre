package deploy

import (
	"context"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type cullConfigTestCase struct {
	namespaceName    string
	namespace        *corev1.Namespace
	expectedMax      int
	expectedStrategy string
	expectErr        bool
}

func createNewNamespace(name string, annotations map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations}}
}

var _ = Describe("Release controller unit tests", func() {
	ctx := context.Background()
	logger := logr.Discard()

	DescribeTable("cullConfig",
		func(tc cullConfigTestCase) {
			scheme := runtime.NewScheme()
			Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())

			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tc.namespace != nil {
				builder = builder.WithObjects(tc.namespace)
			}
			c := builder.Build()

			r := &ReleaseReconciler{Client: c}
			maxReleasesPerTarget, cullingStrategy, err := r.cullConfig(ctx, logger, tc.namespaceName)

			if tc.expectErr {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(maxReleasesPerTarget).To(Equal(tc.expectedMax))
			Expect(cullingStrategy).To(Equal(tc.expectedStrategy))
		},
		Entry("defaults", cullConfigTestCase{
			namespaceName:    "test-ns",
			namespace:        createNewNamespace("test-ns", nil),
			expectedMax:      DefaultMaxReleaseCount,
			expectedStrategy: DefaultCullingStrategy,
		}),
		Entry("max releases valid", cullConfigTestCase{
			namespaceName: "test-ns",
			namespace: createNewNamespace("test-ns", map[string]string{
				deployv1alpha1.AnnotationKeyMaxReleasesPerTarget: "5",
			}),
			expectedMax:      5,
			expectedStrategy: DefaultCullingStrategy,
		}),
		Entry("max releases invalid", cullConfigTestCase{
			namespaceName: "test-ns",
			namespace: createNewNamespace("test-ns", map[string]string{
				deployv1alpha1.AnnotationKeyMaxReleasesPerTarget: "not-an-int",
			}),
			expectedMax:      DefaultMaxReleaseCount,
			expectedStrategy: DefaultCullingStrategy,
		}),
		Entry("strategy end-time", cullConfigTestCase{
			namespaceName: "test-ns",
			namespace: createNewNamespace("test-ns", map[string]string{
				deployv1alpha1.AnnotationKeyCullingStrategy: deployv1alpha1.AnnotationValueCullingStrategyEndTime,
			}),
			expectedMax:      DefaultMaxReleaseCount,
			expectedStrategy: deployv1alpha1.AnnotationValueCullingStrategyEndTime,
		}),
		Entry("strategy signature", cullConfigTestCase{
			namespaceName: "test-ns",
			namespace: createNewNamespace("test-ns", map[string]string{
				deployv1alpha1.AnnotationKeyCullingStrategy: deployv1alpha1.AnnotationValueCullingStrategySignature,
			}),
			expectedMax:      DefaultMaxReleaseCount,
			expectedStrategy: deployv1alpha1.AnnotationValueCullingStrategySignature,
		}),
		Entry("strategy invalid", cullConfigTestCase{
			namespaceName: "test-ns",
			namespace: createNewNamespace("test-ns", map[string]string{
				deployv1alpha1.AnnotationKeyCullingStrategy: "banana",
			}),
			expectedMax:      DefaultMaxReleaseCount,
			expectedStrategy: DefaultCullingStrategy,
		}),
		Entry("namespace missing", cullConfigTestCase{
			namespaceName: "does-not-exist",
			namespace:     nil,
			expectErr:     true,
		}),
	)
})
