package integration

import (
	"context"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	scheduling_v1beta1 "k8s.io/api/scheduling/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("PriorityInjector", func() {
	var (
		ctx             context.Context
		cancel          func()
		namespace       string
		labelValue      string
		priorityClasses []*scheduling_v1beta1.PriorityClass

		c client.Client
	)

	BeforeEach(func() {
		var err error
		ctx, cancel = context.WithTimeout(context.Background(), time.Minute)

		By("Creating client")
		c, err = client.New(testEnv.Config, client.Options{})
		Expect(err).NotTo(HaveOccurred())

		namespace = uuid.New().String()
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}

		By("Creating test namespace: " + namespace)
		Expect(c.Create(ctx, ns)).To(Succeed())

		priorityClasses = []*scheduling_v1beta1.PriorityClass{
			{
				ObjectMeta:    metav1.ObjectMeta{Name: "default"},
				GlobalDefault: true,
				Value:         1000,
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "best-effort"},
				Value:      900,
			},
		}

		By("Creating priority classes")
		for _, pc := range priorityClasses {
			Expect(c.Create(ctx, pc)).To(Succeed())
		}
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
		Expect(c.Update(ctx, ns)).To(Succeed())
	})

	AfterEach(func() {
		By("Destroying priority classes")
		for _, pc := range priorityClasses {
			c.Delete(ctx, pc)
		}

		cancel()
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
					{
						Name:  "app",
						Image: "something",
					},
				},
			},
		}

		By("Creating pod")
		Expect(c.Create(ctx, pod)).To(Succeed())
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
