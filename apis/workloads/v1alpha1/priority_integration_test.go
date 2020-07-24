package integration

import (
	"context"
	"time"

	kitlog "github.com/go-kit/kit/log"
	corev1 "k8s.io/api/core/v1"
	scheduling_v1beta1 "k8s.io/api/scheduling/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/gocardless/theatre/pkg/integration"
	"github.com/gocardless/theatre/pkg/workloads/priority"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	timeout = 5 * time.Second
	logger  = kitlog.NewLogfmtLogger(GinkgoWriter)
)

var _ = Describe("PriorityInjector", func() {
	var (
		ctx             context.Context
		cancel          func()
		namespace       string
		labelValue      string
		teardown        func()
		priorityClasses []*scheduling_v1beta1.PriorityClass
		mgr             manager.Manager
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), time.Minute)
		namespace, teardown = integration.CreateNamespace(clientset)
		mgr = integration.StartTestManager(ctx, cfg)

		priorityClasses = []*scheduling_v1beta1.PriorityClass{
			&scheduling_v1beta1.PriorityClass{
				ObjectMeta:    metav1.ObjectMeta{Name: "default"},
				GlobalDefault: true,
				Value:         1000,
			},
			&scheduling_v1beta1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{Name: "best-effort"},
				Value:      900,
			},
		}

		By("Creating priority classes")
		for _, pc := range priorityClasses {
			Expect(mgr.GetClient().Create(ctx, pc)).To(Succeed())
		}

		integration.NewServer(mgr, integration.MustWebhook(
			priority.NewWebhook(logger, mgr, priority.InjectorOptions{}),
		))
	})

	JustBeforeEach(func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
				Labels: map[string]string{
					"theatre-priority-injector": labelValue,
				},
			},
		}

		By("Updating namespace labels")
		Expect(mgr.GetClient().Update(ctx, ns)).To(Succeed())
	})

	AfterEach(func() {
		By("Destroying priority classes")
		for _, pc := range priorityClasses {
			mgr.GetClient().Delete(ctx, pc)
		}

		cancel()
		teardown()
	})

	createPod := func(priorityClassName string) *corev1.Pod {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sample",
				Namespace: namespace,
			},
			Spec: corev1.PodSpec{
				PriorityClassName: priorityClassName,
				Containers: []corev1.Container{
					corev1.Container{
						Name:  "app",
						Image: "something",
					},
				},
			},
		}

		By("Creating pod")
		Expect(mgr.GetClient().Create(ctx, pod)).To(Succeed())
		return pod
	}

	Describe("Creating pods", func() {
		var (
			pod *corev1.Pod
		)

		JustBeforeEach(func() {
			pod = createPod("")
		})

		BeforeEach(func() {
			labelValue = "best-effort"
		})

		It("Sets priority class name from namespace label", func() {
			Expect(pod.Spec.PriorityClassName).To(Equal(labelValue))
		})

		Context("With no namespace label", func() {
			BeforeEach(func() {
				labelValue = ""
			})

			It("Leaves pod untouched", func() {
				Expect(pod.Spec.PriorityClassName).To(Equal("default"))
			})
		})
	})
})
