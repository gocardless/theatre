package integration

import (
	"context"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha1 "github.com/gocardless/theatre/v5/api/workloads/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/workloads/console/runner"
)

func newNamespace(name string) corev1.Namespace {
	if name == "" {
		name = uuid.New().String()
	}
	return corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

const (
	shortTimeout time.Duration = 500 * time.Millisecond
)

func newConsoleTemplate(namespace, name string, labels map[string]string) workloadsv1alpha1.ConsoleTemplate {
	return workloadsv1alpha1.ConsoleTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: workloadsv1alpha1.ConsoleTemplateSpec{
			Template: workloadsv1alpha1.PodTemplatePreserveMetadataSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Image: "alpine:latest",
							Name:  "console-container-0",
						},
					},
				},
			},
		},
	}
}

func newConsole(namespace, name, consoleTemplateName, username string, labels map[string]string) workloadsv1alpha1.Console {
	return workloadsv1alpha1.Console{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: workloadsv1alpha1.ConsoleSpec{
			User: username,
			ConsoleTemplateRef: corev1.LocalObjectReference{
				Name: consoleTemplateName,
			},
		},
	}
}

func newRoleBinding(namespace, name, username string) rbacv1.RoleBinding {
	return rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind: "User",
				Name: username,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind: "Role",
			Name: "console-test",
		},
	}

}

func mustCreateNamespace(namespace corev1.Namespace) {
	By("Creating test namespace: " + namespace.Name)
	Expect(kubeClient.Create(context.TODO(), &namespace)).NotTo(
		HaveOccurred(), "failed to create test namespace",
	)
}

func mustCreateConsoleTemplate(consoleTemplate workloadsv1alpha1.ConsoleTemplate) {
	By("Creating console template: " + consoleTemplate.Name)
	Expect(kubeClient.Create(context.TODO(), &consoleTemplate)).NotTo(
		HaveOccurred(), "failed to create console template",
	)
}

func mustCreateConsole(console workloadsv1alpha1.Console) {
	By("Creating console: " + console.Name)
	Expect(kubeClient.Create(context.TODO(), &console)).NotTo(
		HaveOccurred(), "failed to create console ",
	)
}

func mustCreateRoleBinding(roleBinding rbacv1.RoleBinding) {
	By("Creating role binding: " + roleBinding.Name)
	Expect(kubeClient.Create(context.TODO(), &roleBinding)).NotTo(
		HaveOccurred(), "failed to create role binding",
	)
}

func mustAddSubjectsToRoleBinding(rb rbacv1.RoleBinding, subjects []rbacv1.Subject) {
	rb.Subjects = subjects
	Eventually(func() error {
		return kubeClient.Update(context.TODO(), &rb)
	}, 2*time.Second).ShouldNot(HaveOccurred())
}

func mustUpdateConsolePhase(in workloadsv1alpha1.Console, phase workloadsv1alpha1.ConsolePhase) {
	csl := &workloadsv1alpha1.Console{}
	err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: in.Namespace, Name: in.Name}, csl)
	Expect(err).ToNot(HaveOccurred(), "error while retrieving console")

	csl.Status.Phase = phase
	err = kubeClient.Update(context.TODO(), csl)
	Expect(err).ToNot(HaveOccurred(), "error while updating console status")
}

var _ = Describe("Runner", func() {
	var (
		consoleRunner runner.Runner
		namespace     corev1.Namespace
	)

	JustBeforeEach(func() {
		cr, err := runner.New(cfg)
		Expect(err).To(BeNil(), "failed to create console runner")
		consoleRunner = *cr
	})

	Describe("CreateResource", func() {
		var (
			console         workloadsv1alpha1.Console
			consoleTemplate workloadsv1alpha1.ConsoleTemplate
			err             error
			consoleLabels   labels.Set
		)

		cmd := []string{"/bin/rails", "console"}
		reason := "reason for console"

		BeforeEach(func() {
			namespace = newNamespace("")
			mustCreateNamespace(namespace)

			consoleTemplate = newConsoleTemplate(namespace.Name, "test", map[string]string{"release": "test"})
		})

		JustBeforeEach(func() {
			var csl *workloadsv1alpha1.Console

			createOptions := runner.Options{Cmd: cmd, Reason: reason, Labels: consoleLabels}
			csl, err = consoleRunner.CreateResource(namespace.Name, consoleTemplate, createOptions)

			if err == nil {
				Expect(csl).NotTo(BeNil(), "a console was not returned")
				console = *csl
			}
		})

		Context("Successfully creates a new console", func() {
			BeforeEach(func() {
				consoleLabels = labels.Set(map[string]string{
					"custom_key": "custom_value",
				})
			})

			It("Creates a new console", func() {
				By("Returning a console")
				Expect(err).NotTo(HaveOccurred())

				By("Referencing the template in the returned console spec")
				Expect(console.Spec.ConsoleTemplateRef.Name).To(Equal(consoleTemplate.Name))

				By("Setting the specified command in the spec")
				Expect(console.Spec.Command).To(Equal(cmd))

				By("Setting the specified reason in the spec")
				Expect(console.Spec.Reason).To(Equal(reason))

				By("Inheriting labels from console template")
				Expect(console.Labels).To(HaveKeyWithValue("release", "test"))

				By("Inheriting custom labels")
				Expect(console.Labels).To(HaveKeyWithValue("custom_key", "custom_value"))

				By("Creating the console")
				Eventually(func() error {
					err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace.Name, Name: console.Name}, &console)
					return err
				}).ShouldNot(HaveOccurred(), "failed to get console")

				By("Creating only one console")
				Eventually(func() []workloadsv1alpha1.Console {
					opts := &client.ListOptions{Namespace: namespace.Name}
					consoleList := &workloadsv1alpha1.ConsoleList{}
					kubeClient.List(context.TODO(), consoleList, opts)
					return consoleList.Items
				}).Should(HaveLen(1), "only one console should be present")
			})
		})

		Context("When a new console can't be created", func() {
			BeforeEach(func() {
				consoleLabels = labels.Set(map[string]string{
					"this is an invalid label": "this is an invalid value",
				})
			})

			It("Fails to create a new console", func() {
				By("Returning an error")
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("FindTemplateBySelector", func() {
		var (
			namespace       corev1.Namespace
			consoleTemplate workloadsv1alpha1.ConsoleTemplate
		)

		BeforeEach(func() {
			namespace = newNamespace("")
			mustCreateNamespace(namespace)

			consoleTemplate = newConsoleTemplate(namespace.Name, "test", map[string]string{"release": "test"})
		})

		JustBeforeEach(func() {
			mustCreateConsoleTemplate(consoleTemplate)
		})

		Context("Successfully finds template", func() {
			It("Finds a template in the namespace", func() {
				foundTmpl, err := consoleRunner.FindTemplateBySelector(namespace.Name, "release=test")
				Expect(err).NotTo(HaveOccurred(), "unable to find template")
				Expect(foundTmpl.Name).To(Equal(consoleTemplate.Name))
			})
		})

		Context("Unsuccessfully finds a template", func() {
			JustBeforeEach(func() {
				consoleTemplate = newConsoleTemplate(namespace.Name, "test-2", map[string]string{"release": "test"})
				mustCreateConsoleTemplate(consoleTemplate)
			})

			It("Fails to find non-existent template", func() {
				By("Returning an error when not found")
				foundTmpl, err := consoleRunner.FindTemplateBySelector(namespace.Name, "release=not-here")
				Expect(err).To(HaveOccurred(), "should be unable to find template")
				Expect(foundTmpl).To(BeNil(), "result template should be nil")
			})

			It("Fails when targeting all namespaces", func() {
				_, err := consoleRunner.FindTemplateBySelector(metav1.NamespaceAll, "release=test")
				Expect(err).To(HaveOccurred(), "expected template collision error")
				Expect(err.Error()).To(
					ContainSubstring("expected to discover 1 console template"),
					"error should list conflicting templates",
				)
			})
		})
	})

	Describe("WaitUntilReady", func() {
		var (
			namespace       corev1.Namespace
			console         workloadsv1alpha1.Console
			consoleTemplate workloadsv1alpha1.ConsoleTemplate
			roleBinding     rbacv1.RoleBinding
		)

		timeout := 400 * time.Millisecond

		BeforeEach(func() {
			namespace = newNamespace("")
			consoleTemplate = newConsoleTemplate(namespace.Name, "test", map[string]string{})
			console = newConsole(namespace.Name, "test", consoleTemplate.Name, "test-user", map[string]string{})
			console.Status.Phase = workloadsv1alpha1.ConsolePending
			roleBinding = newRoleBinding(namespace.Name, console.Name, console.Spec.User)
		})

		JustBeforeEach(func() {
			mustCreateNamespace(namespace)
			mustCreateConsoleTemplate(consoleTemplate)
			mustCreateConsole(console)
			mustCreateRoleBinding(roleBinding)
		})

		Context("When console phase is Pending", func() {
			It("Fails with a timeout waiting", func() {
				ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
				defer cancel()
				_, err := consoleRunner.WaitUntilReady(ctx, console, true)

				Expect(err.Error()).To(ContainSubstring("last phase was: Pending"))
				Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "context should have timed out")
			})
		})

		Context("When console phase is updated to Running", func() {
			It("Returns successfully", func() {
				// Give some time for the watch to be set up, by waiting until
				// half-way through the timeout period before updating the object.
				time.AfterFunc(timeout/2,
					func() { mustUpdateConsolePhase(console, workloadsv1alpha1.ConsoleRunning) },
				)

				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				upToDateCsl, err := consoleRunner.WaitUntilReady(ctx, console, true)

				Expect(err).ToNot(HaveOccurred())
				Expect(upToDateCsl.Status.Phase).To(Equal(workloadsv1alpha1.ConsoleRunning))
			})
		})

		Context("When console phase is updated to non-Running", func() {
			It("Returns with a failure before the timeout", func() {
				time.AfterFunc(timeout/2,
					func() {
						defer GinkgoRecover()
						mustUpdateConsolePhase(console, workloadsv1alpha1.ConsoleStopped)
					},
				)

				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				upToDateCsl, err := consoleRunner.WaitUntilReady(ctx, console, true)

				Expect(err).ToNot(HaveOccurred())
				Expect(upToDateCsl.Status.Phase).To(Equal(workloadsv1alpha1.ConsoleStopped))
			})
		})

		Context("When console is already running", func() {
			BeforeEach(func() {
				console.Status.Phase = workloadsv1alpha1.ConsoleRunning
			})

			It("Returns successfully", func() {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				upToDateCsl, err := consoleRunner.WaitUntilReady(ctx, console, true)

				Expect(err).ToNot(HaveOccurred())
				Expect(upToDateCsl.Status.Phase).To(Equal(workloadsv1alpha1.ConsoleRunning))
			})
		})

		Context("When console is already stopped", func() {
			BeforeEach(func() {
				console.Status.Phase = workloadsv1alpha1.ConsoleStopped
			})

			It("Returns successfully", func() {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				upToDateCsl, err := consoleRunner.WaitUntilReady(ctx, console, true)

				Expect(err).ToNot(HaveOccurred())
				Expect(upToDateCsl.Status.Phase).To(Equal(workloadsv1alpha1.ConsoleStopped))
			})
		})

		Context("When console does not exist", func() {
			It("Fails with a timeout", func() {
				console.Name = "idontexist"
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				_, err := consoleRunner.WaitUntilReady(ctx, console, true)

				Expect(err.Error()).To(ContainSubstring("context deadline exceeded"))
				Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "context should have timed out")
			})
		})

		Context("When waiting for console to exist", func() {
			It("Returns successfully", func() {
				csl := newConsole(namespace.Name, "idontexistyet", consoleTemplate.Name, "test-user", map[string]string{})
				csl.Status.Phase = workloadsv1alpha1.ConsoleRunning
				time.AfterFunc(timeout/2,
					func() {
						defer GinkgoRecover()
						mustCreateConsole(csl)
					})

				rb := newRoleBinding(namespace.Name, csl.Name, csl.Spec.User)
				mustCreateRoleBinding(rb)

				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				upToDateCsl, err := consoleRunner.WaitUntilReady(ctx, csl, true)

				Expect(err).ToNot(HaveOccurred())
				Expect(upToDateCsl.Status.Phase).To(Equal(workloadsv1alpha1.ConsoleRunning))
			})
		})

		Describe("When waiting for the rolebinding to be ready", func() {
			Context("When the rolebinding does not exist yet", func() {
				It("Fails with a timeout", func() {
					csl := newConsole(namespace.Name, "consolewithoutrolebinding", consoleTemplate.Name, "test-user", map[string]string{})
					csl.Status.Phase = workloadsv1alpha1.ConsoleRunning
					mustCreateConsole(csl)

					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					_, err := consoleRunner.WaitUntilReady(ctx, csl, true)

					Expect(err).To(MatchError(ContainSubstring("waiting for rolebinding interrupted")))
					Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "context should have timed out")
				})
			})

			Context("When the rolebinding is created", func() {
				It("Returns success", func() {
					csl := newConsole(namespace.Name, "consolewithoutrolebinding", consoleTemplate.Name, "test-user", map[string]string{})
					csl.Status.Phase = workloadsv1alpha1.ConsoleRunning
					mustCreateConsole(csl)

					rb := newRoleBinding(namespace.Name, csl.Name, csl.Spec.User)
					time.AfterFunc(timeout/2,
						func() {
							defer GinkgoRecover()
							mustCreateRoleBinding(rb)
						})

					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					_, err := consoleRunner.WaitUntilReady(ctx, csl, true)

					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("When the rolebinding exists but has no subjects", func() {
				It("Fails with a timeout", func() {
					csl := newConsole(namespace.Name, "norolebindingsubjects", consoleTemplate.Name, "test-user", map[string]string{})
					csl.Status.Phase = workloadsv1alpha1.ConsoleRunning
					mustCreateConsole(csl)

					rb := newRoleBinding(namespace.Name, csl.Name, csl.Spec.User)
					rb.Subjects = nil
					mustCreateRoleBinding(rb)

					ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
					defer cancel()
					_, err := consoleRunner.WaitUntilReady(ctx, csl, true)

					Expect(err).To(MatchError(ContainSubstring("waiting for rolebinding interrupted")))
					Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "context should have timed out")
				})
			})

			Context("When the rolebinding is subsequently updated with the desired subjects", func() {
				It("Returns success", func() {
					csl := newConsole(namespace.Name, "norolebindingsubjects", consoleTemplate.Name, "test-user", map[string]string{})
					csl.Status.Phase = workloadsv1alpha1.ConsoleRunning
					mustCreateConsole(csl)

					rb := newRoleBinding(namespace.Name, csl.Name, csl.Spec.User)
					rb.Subjects = nil
					mustCreateRoleBinding(rb)

					time.AfterFunc(timeout/2,
						func() {
							defer GinkgoRecover()
							mustAddSubjectsToRoleBinding(rb, []rbacv1.Subject{{Kind: "User", Name: csl.Spec.User}})
						},
					)

					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					_, err := consoleRunner.WaitUntilReady(ctx, csl, true)

					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("When the rolebinding subsequently updated with undesired subjects", func() {
				It("Fails with a timeout", func() {
					csl := newConsole(namespace.Name, "norolebindingsubjects", consoleTemplate.Name, "test-user", map[string]string{})
					csl.Status.Phase = workloadsv1alpha1.ConsoleRunning
					mustCreateConsole(csl)

					rb := newRoleBinding(namespace.Name, csl.Name, csl.Spec.User)
					rb.Subjects = nil
					mustCreateRoleBinding(rb)

					time.AfterFunc(timeout/2,
						func() {
							defer GinkgoRecover()
							mustAddSubjectsToRoleBinding(rb, []rbacv1.Subject{{Kind: "User", Name: "rando"}})
						},
					)

					ctx, cancel := context.WithTimeout(context.Background(), shortTimeout)
					defer cancel()
					_, err := consoleRunner.WaitUntilReady(ctx, csl, true)

					Expect(err).To(MatchError(ContainSubstring("waiting for rolebinding interrupted")))
					Expect(ctx.Err()).To(MatchError(context.DeadlineExceeded), "context should have timed out")
				})
			})
		})
	})
})
