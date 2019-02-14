package runner

import (
	"context"
	"time"

	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	theatreFake "github.com/gocardless/theatre/pkg/client/clientset/versioned/fake"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("Runner", func() {
	var (
		kubeClient      *fake.Clientset
		theatreClient   *theatreFake.Clientset
		runner          *Runner
		fakeConsoles    []runtime.Object
		fakeKubeObjects []runtime.Object
		namespace       = "testns"
	)

	JustBeforeEach(func() {
		theatreClient = theatreFake.NewSimpleClientset(fakeConsoles...)
		kubeClient = fake.NewSimpleClientset(fakeKubeObjects...)
		runner = New(kubeClient, theatreClient)
	})

	Describe("Create", func() {

		Context("When creating a new console", func() {

			var (
				createdCsl   *workloadsv1alpha1.Console
				createCslErr error
			)

			cslTmplFixture := &workloadsv1alpha1.ConsoleTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"test": "test-value",
					},
				},
			}

			cmd := []string{"/bin/rails", "console"}
			createOptions := Options{}
			createOptions.Cmd = cmd

			JustBeforeEach(func() {
				createdCsl, createCslErr = runner.Create(namespace, *cslTmplFixture, createOptions)
			})

			It("Successfully creates a console", func() {
				Expect(createCslErr).NotTo(HaveOccurred())
				Expect(createdCsl).NotTo(BeNil(), "a console was not returned")
			})

			It("References the template in the returned console spec", func() {
				Expect(createdCsl.Spec.ConsoleTemplateRef.Name).To(Equal("test"))
			})

			It("Sets the specified command in the spec", func() {
				Expect(createdCsl.Spec.Command).To(Equal(cmd))
			})

			It("Creates the console via the clientset", func() {
				list, err := theatreClient.WorkloadsV1alpha1().Consoles("").List(metav1.ListOptions{})
				Expect(err).NotTo(HaveOccurred(), "failed to list consoles")
				Expect(list.Items).To(HaveLen(1), "only one console should be present")
			})

			It("Inherits labels from console template", func() {
				Expect(createdCsl.Labels).To(HaveKeyWithValue("test", "test-value"))
			})

			It("Creates the console in the namespace specified", func() {
				fetchedCsl, err := theatreClient.WorkloadsV1alpha1().Consoles("").Get("", metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "failed to get console")
				Expect(fetchedCsl.Namespace).To(Equal(namespace), "namespace should match the creation namespace")
			})

			It("Returns an error when the console already exists", func() {
				// We're creating a console with an empty name here (only GenerateName
				// is set). Therefore because we're only using a fake object store,
				// which doesn't mutate GenerateName into a random Name, we'll get
				// duplicate objects.
				_, err := runner.Create(namespace, *cslTmplFixture, Options{})
				Expect(err).To(MatchError(
					errors.NewAlreadyExists(workloadsv1alpha1.Resource("consoles"), ""),
				))
			})

		})

	})

	Describe("FindTemplateBySelector", func() {

		cslTmpl := &workloadsv1alpha1.ConsoleTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-template",
				Namespace: namespace,
				Labels: map[string]string{
					"release": "hello-world",
				},
			},
		}

		Context("With an existing template", func() {
			BeforeEach(func() {
				fakeConsoles = []runtime.Object{cslTmpl}
			})

			It("Finds a template across all namespaces", func() {
				foundTmpl, err := runner.FindTemplateBySelector(namespace, "release=hello-world")
				Expect(err).NotTo(HaveOccurred(), "unable to find template")
				Expect(foundTmpl.Name).To(Equal("test-template"), "template should exist")
			})

			It("Finds a template in a single namespace", func() {
				foundTmpl, err := runner.FindTemplateBySelector(metav1.NamespaceAll, "release=hello-world")
				Expect(err).NotTo(HaveOccurred(), "unable to find template")
				Expect(foundTmpl.Name).To(Equal("test-template"), "template should exist")
			})
		})

		Context("When searching for a non-existent template", func() {
			It("Returns an error", func() {
				foundTmpl, err := runner.FindTemplateBySelector(namespace, "release=not-here")
				Expect(err).To(HaveOccurred(), "should be unable to find template")
				Expect(foundTmpl).To(BeNil(), "result template should be nil")
			})
		})

		Context("With multiple colliding templates", func() {
			BeforeEach(func() {
				cslTmpl2 := cslTmpl.DeepCopy()
				cslTmpl2.Name = "test-template-2"
				cslTmpl2.Namespace = "other-ns"
				fakeConsoles = []runtime.Object{cslTmpl, cslTmpl2}
			})

			It("Succeeds when targeting a single namespace", func() {
				_, err := runner.FindTemplateBySelector(namespace, "release=hello-world")
				Expect(err).NotTo(HaveOccurred(), "unable to find template")
			})

			It("Fails when targeting all namespaces", func() {
				_, err := runner.FindTemplateBySelector(metav1.NamespaceAll, "release=hello-world")
				Expect(err).To(HaveOccurred(), "expected template collision error")
				Expect(err.Error()).To(
					ContainSubstring("found: [testns/test-template other-ns/test-template-2]"),
					"error should list conflicting templates",
				)
			})
		})
	})

	Describe("WaitUntilReady", func() {

		timeout := 200 * time.Millisecond

		csl := workloadsv1alpha1.Console{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-console",
			},
		}

		updateToPhase := func(in workloadsv1alpha1.Console, phase workloadsv1alpha1.ConsolePhase) {
			// Ensure we recover, as this is being run in a goroutine
			defer GinkgoRecover()

			cslInterface := theatreClient.WorkloadsV1alpha1().Consoles(in.Namespace)
			csl, err := cslInterface.Get(in.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "error while retrieving console")

			csl.Status.Phase = phase
			_, err = cslInterface.Update(csl)
			Expect(err).ToNot(HaveOccurred(), "error while updating console status")
		}

		Context("When the console is pending", func() {

			BeforeEach(func() {
				csl.Status.Phase = workloadsv1alpha1.ConsolePending
				fakeConsoles = []runtime.Object{&csl}
			})

			It("Fails with a timeout", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				defer cancel()
				_, err := runner.WaitUntilReady(ctx, csl)

				Expect(err.Error()).To(ContainSubstring("last phase was: 'Pending'"))
				Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "context should have timed out")
			})

			Context("When phase is updated to Running", func() {
				It("Returns successfully", func() {
					// Give some time for the watch to be set up, by waiting until
					// half-way through the timeout period before updating the object.
					time.AfterFunc(timeout/2,
						func() { updateToPhase(csl, workloadsv1alpha1.ConsoleRunning) },
					)

					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					upToDateCsl, err := runner.WaitUntilReady(ctx, csl)

					Expect(err).ToNot(HaveOccurred())
					Expect(upToDateCsl.Status.Phase).To(Equal(workloadsv1alpha1.ConsoleRunning))
				})
			})

			Context("When phase is updated to non-Running", func() {
				It("Returns with a failure before the timeout", func() {
					time.AfterFunc(timeout/2,
						func() { updateToPhase(csl, workloadsv1alpha1.ConsoleStopped) },
					)

					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					_, err := runner.WaitUntilReady(ctx, csl)

					Expect(err.Error()).To(ContainSubstring("console is Stopped"))
					Expect(ctx.Err()).To(BeNil(), "context should not have timed out")
				})
			})
		})

		Context("When console is already running", func() {
			BeforeEach(func() {
				csl.Status.Phase = workloadsv1alpha1.ConsoleRunning
				fakeConsoles = []runtime.Object{&csl}
			})

			It("Returns successfully", func() {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				upToDateCsl, err := runner.WaitUntilReady(ctx, csl)

				Expect(err).ToNot(HaveOccurred())
				Expect(upToDateCsl.Status.Phase).To(Equal(workloadsv1alpha1.ConsoleRunning))
			})

		})

		Context("When console is already stopped", func() {
			BeforeEach(func() {
				csl.Status.Phase = workloadsv1alpha1.ConsoleStopped
				fakeConsoles = []runtime.Object{&csl}
			})

			// TODO - return a proper error
			It("Returns an error immediately", func() {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				_, err := runner.WaitUntilReady(ctx, csl)

				Expect(ctx.Err()).To(BeNil(), "context should not have timed out")
				Expect(err.Error()).To(ContainSubstring("console is Stopped"))
			})

		})

		Context("When console does not exist", func() {
			BeforeEach(func() {
				fakeConsoles = []runtime.Object{}
			})

			It("Fails with a timeout", func() {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				_, err := runner.WaitUntilReady(ctx, csl)

				Expect(err.Error()).To(ContainSubstring("console not found"))
				Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "context should have timed out")
			})

			Context("But it is later created", func() {
				createCsl := func() {
					defer GinkgoRecover()

					cslInterface := theatreClient.WorkloadsV1alpha1().Consoles(csl.Namespace)
					createCsl := csl.DeepCopy()
					createCsl.Status.Phase = workloadsv1alpha1.ConsoleRunning
					_, err := cslInterface.Create(createCsl)

					Expect(err).ToNot(HaveOccurred(), "error while updating console status")
				}

				It("Returns successfully", func() {
					time.AfterFunc(timeout/2, createCsl)

					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					upToDateCsl, err := runner.WaitUntilReady(ctx, csl)

					Expect(err).ToNot(HaveOccurred())
					Expect(upToDateCsl.Status.Phase).To(Equal(workloadsv1alpha1.ConsoleRunning))
				})
			})
		})
	})

	Describe("GetAttachablePod", func() {
		var (
			csl          *workloadsv1alpha1.Console
			consolePod   *corev1.Pod
			unrelatedPod *corev1.Pod
		)

		BeforeEach(func() {
			csl = &workloadsv1alpha1.Console{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-console",
					Namespace: "test-namespace",
				},
				Status: workloadsv1alpha1.ConsoleStatus{PodName: "some-pod"},
			}
			consolePod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-pod",
					Namespace: csl.ObjectMeta.Namespace,
				},
			}
			unrelatedPod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-other-pod",
					Namespace: csl.ObjectMeta.Namespace,
				},
			}

			fakeConsoles = []runtime.Object{csl}
		})

		Context("When there is no matching pod", func() {
			BeforeEach(func() {
				fakeKubeObjects = []runtime.Object{unrelatedPod}
			})

			It("Returns an error", func() {
				_, err := runner.GetAttachablePod(csl)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("When the pod is not attachable", func() {
			BeforeEach(func() {
				fakeKubeObjects = []runtime.Object{consolePod}
			})

			It("Returns an error", func() {
				_, err := runner.GetAttachablePod(csl)
				Expect(err).To(MatchError("no attachable pod found"))
			})
		})

		Context("When the pod is attachable", func() {
			BeforeEach(func() {
				consolePod.Spec = corev1.PodSpec{
					Containers: []corev1.Container{{TTY: true}},
				}

				fakeKubeObjects = []runtime.Object{consolePod}
			})

			It("Returns the pod", func() {
				returnedPod, err := runner.GetAttachablePod(csl)
				Expect(err).NotTo(HaveOccurred())
				Expect(returnedPod).To(Equal(consolePod))
			})
		})
	})
})
