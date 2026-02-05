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
	namespaceName string
	namespace     *corev1.Namespace
	expectedLimit int
	expectErr     bool
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
			limit, err := r.cullConfig(ctx, logger, tc.namespaceName)

			if tc.expectErr {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(limit).To(Equal(tc.expectedLimit))
		},
		Entry("defaults", cullConfigTestCase{
			namespaceName: "test-ns",
			namespace:     createNewNamespace("test-ns", nil),
			expectedLimit: DefaultReleaseLimit,
		}),
		Entry("max releases valid", cullConfigTestCase{
			namespaceName: "test-ns",
			namespace: createNewNamespace("test-ns", map[string]string{
				deployv1alpha1.AnnotationKeyReleaseLimit: "5",
			}),
			expectedLimit: 5,
		}),
		Entry("max releases invalid", cullConfigTestCase{
			namespaceName: "test-ns",
			namespace: createNewNamespace("test-ns", map[string]string{
				deployv1alpha1.AnnotationKeyReleaseLimit: "not-an-int",
			}),
			expectedLimit: DefaultReleaseLimit,
		}),
		Entry("namespace missing", cullConfigTestCase{
			namespaceName: "does-not-exist",
			namespace:     nil,
			expectErr:     true,
		}),
	)
})
