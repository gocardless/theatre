package runner

import (
	"context"
	"time"

	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	theatreFake "github.com/gocardless/theatre/pkg/client/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
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

	BeforeEach(func() {
		// Remove test order dependency by resetting state between tests
		fakeConsoles = []runtime.Object{}
		fakeKubeObjects = []runtime.Object{}
	})

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
			reason := "reason for console"
			createOptions := Options{Cmd: cmd, Reason: reason}

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

			It("Sets the specified reason in the spec", func() {
				Expect(createdCsl.Spec.Reason).To(Equal(reason))
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

	Describe("FindConsoleByName", func() {
		createConsole := func(namespace, name string) runtime.Object {
			return &workloadsv1alpha1.Console{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
			}
		}

		consoles := []runtime.Object{
			createConsole("n1", "c1"),
			createConsole("n1", "c2"),
			createConsole("n1", "c3"),
			createConsole("n1", "nameclash"),
			createConsole("n2", "nameclash"),
		}

		BeforeEach(func() {
			fakeConsoles = consoles
		})

		It("returns console with unique name across namespaces", func() {
			csl, err := runner.FindConsoleByName(metav1.NamespaceAll, "c2")
			Expect(err).NotTo(HaveOccurred())
			Expect(csl).To(Equal(consoles[1]))
		})

		It("when namespace is specified, returns console with name that is only unique in that namespace", func() {
			csl, err := runner.FindConsoleByName("n2", "nameclash")
			Expect(err).NotTo(HaveOccurred())
			Expect(csl).To(Equal(consoles[4]))
		})

		It("when namespace is not specified and name is not globally unique, returns error", func() {
			_, err := runner.FindConsoleByName(metav1.NamespaceAll, "nameclash")
			Expect(err).To(MatchError(ContainSubstring("too many consoles")))
		})

		It("when no console with specified name exists, returns error", func() {
			_, err := runner.FindConsoleByName(metav1.NamespaceAll, "idontexist")
			Expect(err).To(MatchError(ContainSubstring("no consoles")))
		})

		It("when no console with specified name exists in the specified namespace, returns error", func() {
			_, err := runner.FindConsoleByName("anothernamespace", "c1")
			Expect(err).To(MatchError(ContainSubstring("no consoles")))
		})
	})

	Describe("ListConsolesByLabelsAndUser", func() {
		createConsole := func(namespace, name, username string, labels map[string]string) runtime.Object {
			return &workloadsv1alpha1.Console{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
					Labels:    labels,
				},
				Spec: workloadsv1alpha1.ConsoleSpec{
					User: username,
				},
			}
		}

		raw := func(cslPtr runtime.Object) workloadsv1alpha1.Console {
			return *(cslPtr.(*workloadsv1alpha1.Console))
		}

		consoles := []runtime.Object{
			createConsole("n1", "c1", "alice", map[string]string{"foo": "bar"}),
			createConsole("n1", "c2", "alice", map[string]string{"foo": "bar"}),
			createConsole("n1", "c3", "bob", map[string]string{"foo": "bar"}),
			createConsole("n1", "c4", "bob", map[string]string{"baz": "barry"}),
		}

		BeforeEach(func() {
			fakeConsoles = consoles
		})

		It("when username not specified, returns all consoles matching label selector", func() {
			csls, err := runner.ListConsolesByLabelsAndUser(metav1.NamespaceAll, "", "foo=bar")
			Expect(err).NotTo(HaveOccurred())
			Expect(csls).To(ConsistOf(raw(consoles[0]), raw(consoles[1]), raw(consoles[2])))
		})

		It("when username specified, returns all consoles matching label selector", func() {
			csls, err := runner.ListConsolesByLabelsAndUser(metav1.NamespaceAll, "alice", "foo=bar")
			Expect(err).NotTo(HaveOccurred())
			Expect(csls).To(ConsistOf(raw(consoles[0]), raw(consoles[1])))
		})
	})

	Describe("WaitUntilReady", func() {
		addSubjectsToRoleBinding := func(rb rbacv1.RoleBinding, subjects []rbacv1.Subject) {
			rb.Subjects = subjects
			_, err := kubeClient.RbacV1().RoleBindings(rb.Namespace).Update(&rb)
			Expect(err).NotTo(HaveOccurred())
		}

		updateConsolePhase := func(in workloadsv1alpha1.Console, phase workloadsv1alpha1.ConsolePhase) {
			// Ensure we recover, as this is being run in a goroutine
			defer GinkgoRecover()

			cslInterface := theatreClient.WorkloadsV1alpha1().Consoles(in.Namespace)
			csl, err := cslInterface.Get(in.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred(), "error while retrieving console")

			csl.Status.Phase = phase
			_, err = cslInterface.Update(csl)
			Expect(err).ToNot(HaveOccurred(), "error while updating console status")
		}

		var (
			timeout time.Duration
			csl     workloadsv1alpha1.Console
			readyRb rbacv1.RoleBinding
		)

		BeforeEach(func() {
			timeout = 200 * time.Millisecond

			csl = workloadsv1alpha1.Console{
				ObjectMeta: metav1.ObjectMeta{Name: "test-console"},
				Spec:       workloadsv1alpha1.ConsoleSpec{User: "test-user"},
			}

			readyRb = rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: csl.Name},
				Subjects:   []rbacv1.Subject{{Name: csl.Spec.User}},
			}
		})

		Describe("waiting for the console to be ready", func() {
			BeforeEach(func() {
				// For all the tests exercising blocking on console readiness, ensure
				// that the rolebinding is already ready.
				fakeKubeObjects = []runtime.Object{&readyRb}
			})

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
							func() { updateConsolePhase(csl, workloadsv1alpha1.ConsoleRunning) },
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
							func() { updateConsolePhase(csl, workloadsv1alpha1.ConsoleStopped) },
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

		Describe("Waiting for the rolebinding to be ready", func() {
			BeforeEach(func() {
				// For all the tests exercising blocking on console readiness, ensure
				// that the rolebinding is already ready.
				csl.Status.Phase = workloadsv1alpha1.ConsoleRunning
				fakeConsoles = []runtime.Object{&csl}
			})

			Context("When the rolebinding does not exist yet", func() {
				BeforeEach(func() {
					fakeKubeObjects = []runtime.Object{}
				})

				It("Fails with a timeout", func() {
					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					_, err := runner.WaitUntilReady(ctx, csl)

					Expect(err).To(MatchError(ContainSubstring("waiting for rolebinding interrupted")))
					Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "context should have timed out")
				})

				Context("When it is subsequently created then updated", func() {
					createRoleBinding := func(timeout time.Duration) {
						defer GinkgoRecover()

						unreadyRb := readyRb // hear me out
						subjects := readyRb.Subjects
						unreadyRb.Subjects = nil

						rbClient := kubeClient.RbacV1().RoleBindings(csl.Namespace)
						rb, err := rbClient.Create(&unreadyRb)
						Expect(err).NotTo(HaveOccurred())

						// Try to exercise code that requires the RoleBinding to contain the
						// expcted subjects, not just that it exists
						time.Sleep(timeout / 4)

						addSubjectsToRoleBinding(*rb, subjects)
					}

					It("Returns success", func() {
						time.AfterFunc(timeout/4, func() {
							defer GinkgoRecover()
							createRoleBinding(timeout)
						})

						ctx, cancel := context.WithTimeout(context.Background(), timeout)
						defer cancel()
						_, err := runner.WaitUntilReady(ctx, csl)

						Expect(err).ToNot(HaveOccurred())
					})
				})
			})

			Context("When the rolebinding exists but has no subjects", func() {
				var rb rbacv1.RoleBinding

				BeforeEach(func() {
					rb = rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: csl.Name}}
					fakeKubeObjects = []runtime.Object{&rb}
				})

				It("Fails with a timeout", func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
					defer cancel()
					_, err := runner.WaitUntilReady(ctx, csl)

					Expect(err).To(MatchError(ContainSubstring("waiting for rolebinding interrupted")))
					Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "context should have timed out")
				})

				Context("When it is subsequently updated with the desired subjects", func() {
					It("Returns success", func() {
						done := make(chan struct{})
						time.AfterFunc(timeout/2,
							func() {
								defer GinkgoRecover()
								addSubjectsToRoleBinding(rb, []rbacv1.Subject{{Name: csl.Spec.User}})
								close(done)
							},
						)

						ctx, cancel := context.WithTimeout(context.Background(), timeout)
						defer cancel()
						_, err := runner.WaitUntilReady(ctx, csl)

						Expect(err).ToNot(HaveOccurred())

						// Allow modification of global test state to finish to avoid race
						<-done
					})
				})

				Context("When it is subsequently updated with undesired subjects", func() {
					It("Fails with a timeout", func() {
						done := make(chan struct{})
						time.AfterFunc(timeout/2,
							func() {
								defer GinkgoRecover()
								addSubjectsToRoleBinding(rb, []rbacv1.Subject{{Name: "rando"}})
								close(done)
							},
						)

						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
						defer cancel()
						_, err := runner.WaitUntilReady(ctx, csl)

						Expect(err).To(MatchError(ContainSubstring("waiting for rolebinding interrupted")))
						Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "context should have timed out")

						// Allow modification of global test state to finish to avoid race
						<-done
					})
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
