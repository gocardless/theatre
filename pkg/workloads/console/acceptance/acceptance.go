package acceptance

import (
	"context"
	"time"

	kitlog "github.com/go-kit/kit/log"
	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	theatre "github.com/gocardless/theatre/pkg/client/clientset/versioned"
	workloadsclient "github.com/gocardless/theatre/pkg/client/clientset/versioned/typed/workloads/v1alpha1"
	"github.com/gocardless/theatre/pkg/workloads/console/runner"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	namespace    = "default"
	consoleName  = "console-0"
	templateName = "console-template-0"
	jobName      = "console-0-console"
	user         = "kubernetes-admin"
)

// This clientset is a union of the default kubernetes clientset and the workloads client.
type clientset struct {
	*kubernetes.Clientset
	workloadsV1alpha1 *workloadsclient.WorkloadsV1alpha1Client
}

func (c *clientset) WorkloadsV1Alpha1() *workloadsclient.WorkloadsV1alpha1Client {
	return c.workloadsV1alpha1
}

func newClient(config *rest.Config) clientset {
	// Construct a client for the workloads API Group
	workloadsClient, err := workloadsclient.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred(), "could not connect to kubernetes cluster")

	// Construct a client for the core Kubernetes API Groups
	core, err := kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred(), "could not connect to kubernetes cluster")

	return clientset{Clientset: core, workloadsV1alpha1: workloadsClient}
}

func newTheatreClient(config *rest.Config) theatre.Interface {
	// Construct a client for the workloads API Group
	client, err := theatre.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred(), "could not connect to kubernetes cluster")
	return client
}

type Runner struct{}

func (r *Runner) Name() string {
	return "pkg/workloads/console/acceptance"
}

func (r *Runner) Prepare(logger kitlog.Logger, config *rest.Config) error {
	return nil
}

func (r *Runner) Run(logger kitlog.Logger, config *rest.Config) {
	Describe("Consoles", func() {
		var (
			client clientset
		)

		BeforeEach(func() {
			logger.Log("msg", "starting test")

			client = newClient(config)

			// Wait for MutatingWebhookConfig to be created
			Eventually(func() bool {
				_, err := client.Admissionregistration().MutatingWebhookConfigurations().Get("theatre-workloads", metav1.GetOptions{})
				if err != nil {
					logger.Log("error", err)
					return false
				}
				return true
			}).Should(Equal(true))
		})

		Specify("Happy path", func() {
			By("Create a console template")
			var ttl int32 = 10
			template := buildConsoleTemplate(&ttl, true)
			template, err := client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).Create(template)
			Expect(err).NotTo(HaveOccurred(), "could not create console template")

			By("Create a console")
			console := buildConsole()
			console.Spec.Command = []string{"sleep", "666"}
			console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Create(console)
			Expect(err).NotTo(HaveOccurred(), "could not create console")

			defer func() {
				By("(cleanup) Delete the console template")
				policy := metav1.DeletePropagationForeground
				err = client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).
					Delete(templateName, &metav1.DeleteOptions{PropagationPolicy: &policy})
				Expect(err).NotTo(HaveOccurred(), "could not delete console template")

				Eventually(func() error {
					_, err = client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).Get(templateName, metav1.GetOptions{})
					return err
				}).Should(HaveOccurred(), "expected console template to be deleted, it still exists")
			}()

			By("Expect an authorisation has been created")
			var consoleAuthorisation *workloadsv1alpha1.ConsoleAuthorisation
			Eventually(func() error {
				consoleAuthorisation, err = client.WorkloadsV1Alpha1().ConsoleAuthorisations(namespace).Get(consoleName, metav1.GetOptions{})
				return err
			}).ShouldNot(HaveOccurred(), "could not find authorisation")

			By("Expect the console phase is pending authorisation")
			Eventually(func() workloadsv1alpha1.ConsolePhase {
				console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(consoleName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "could not find console")
				return console.Status.Phase
			}).Should(Equal(workloadsv1alpha1.ConsolePendingAuthorisation))

			By("Expect that the job has not been created")
			Eventually(func() error {
				_, err = client.BatchV1().Jobs(namespace).Get(consoleName, metav1.GetOptions{})
				return err
			}).Should(HaveOccurred(), "expected not to find job, but did")

			// Change the console user to another user as a user cannot authorise their own console
			By("Update the console user")
			console.Spec.User = "another-user@example.com"
			console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Update(console)
			Expect(err).NotTo(HaveOccurred(), "could not update console user")

			By("Authorise a console")
			consoleAuthorisation.Spec.Authorisations = []rbacv1.Subject{{Kind: "User", Name: user}}
			_, err = client.WorkloadsV1Alpha1().ConsoleAuthorisations(namespace).Update(consoleAuthorisation)
			Expect(err).NotTo(HaveOccurred(), "could not authorise console")

			By("Expect a job has been created")
			Eventually(func() error {
				_, err = client.BatchV1().Jobs(namespace).Get(jobName, metav1.GetOptions{})
				return err
			}).ShouldNot(HaveOccurred(), "could not find job")

			By("Expect a pod has been created")
			Eventually(func() ([]corev1.Pod, error) {
				opts := metav1.ListOptions{LabelSelector: "job-name=" + jobName}
				podList, err := client.CoreV1().Pods(namespace).List(opts)
				return podList.Items, err
			}).Should(HaveLen(1), "expected to find a single pod")

			By("Expect the console phase is Running")
			Eventually(func() workloadsv1alpha1.ConsolePhase {
				console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(consoleName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "could not find console")
				return console.Status.Phase
			}).Should(Equal(workloadsv1alpha1.ConsoleRunning))

			By("Expect the console phase eventually changes to Stopped")
			Eventually(func() workloadsv1alpha1.ConsolePhase {
				console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(consoleName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "could not find console")
				return console.Status.Phase
			}).Should(Equal(workloadsv1alpha1.ConsoleStopped))

			// TODO: attach to pod

			By("Expect that the job still exists")
			_, err = client.BatchV1().Jobs(namespace).Get(jobName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "could not find job")

			By("Expect that the console is deleted shortly after stopping, due to its TTL")
			Eventually(func() error {
				_, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(consoleName, metav1.GetOptions{})
				return err
			}, 12*time.Second).Should(HaveOccurred(), "expected not to find console, but did")

			By("Expect that the pod eventually terminates")
			Eventually(func() int {
				opts := metav1.ListOptions{LabelSelector: "job-name=" + jobName}
				podList, _ := client.CoreV1().Pods(namespace).List(opts)
				return len(podList.Items)
			}).Should(Equal(0), "pod did not get deleted")
		})

		Describe("Runner interface", func() {
			var (
				consoleRunner *runner.Runner
				console       *workloadsv1alpha1.Console
				createError   error
			)

			BeforeEach(func() {
				consoleRunner = runner.New(client, newTheatreClient(config))
			})
			Specify("Happy path", func() {
				By("Create a console template")
				var ttl int32 = 10
				template := buildConsoleTemplate(&ttl, true)
				template, err := client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).Create(template)
				Expect(err).NotTo(HaveOccurred(), "could not create console template")

				By("Create a console")
				go func() {
					_, createError = consoleRunner.Create(context.TODO(), runner.CreateOptions{
						Namespace: namespace,
						Selector:  "",
						Timeout:   6 * time.Second,
						Reason:    "",
						Command:   []string{"sleep", "666"},
						Attach:    false,
					})

					Expect(createError).To(BeNil())
				}()

				By("Wait for the console to be created")
				Eventually(func() *workloadsv1alpha1.Console {
					consoles, err := client.WorkloadsV1Alpha1().Consoles(namespace).List(metav1.ListOptions{})
					Expect(err).To(BeNil(), "Failed to list consoles")

					if len(consoles.Items) == 1 {
						console = &consoles.Items[0]
						return &consoles.Items[0]
					}

					return nil
				}).ShouldNot(BeNil())

				defer func() {
					By("(cleanup) Delete the console template")
					policy := metav1.DeletePropagationForeground
					err = client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).
						Delete(templateName, &metav1.DeleteOptions{PropagationPolicy: &policy})
					Expect(err).NotTo(HaveOccurred(), "could not delete console template")

					Eventually(func() error {
						_, err = client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).Get(templateName, metav1.GetOptions{})
						return err
					}).Should(HaveOccurred(), "expected console template to be deleted, it still exists")
				}()

				By("Expect an authorisation has been created")
				Eventually(func() error {
					_, err = client.WorkloadsV1Alpha1().ConsoleAuthorisations(namespace).Get(console.Name, metav1.GetOptions{})
					return err
				}).ShouldNot(HaveOccurred(), "could not find authorisation")

				By("Expect the console phase is pending authorisation")
				Eventually(func() workloadsv1alpha1.ConsolePhase {
					console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(console.Name, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred(), "could not find console")
					return console.Status.Phase
				}).Should(Equal(workloadsv1alpha1.ConsolePendingAuthorisation))

				By("Expect that the job has not been created")
				Eventually(func() error {
					_, err = client.BatchV1().Jobs(namespace).Get(console.Name, metav1.GetOptions{})
					return err
				}).Should(HaveOccurred(), "expected not to find job, but did")

				// Change the console user to another user as a user cannot authorise their own console
				By("Update the console user")
				console.Spec.User = "another-user@example.com"
				console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Update(console)
				Expect(err).NotTo(HaveOccurred(), "could not update console user")

				By("Authorise a console")
				err = consoleRunner.Authorise(context.TODO(), runner.AuthoriseOptions{
					Namespace:   namespace,
					ConsoleName: console.Name,
					Username:    user,
				})
				Expect(err).NotTo(HaveOccurred(), "could not authorise console")

				By("Expect a pod has been created")
				Eventually(func() ([]corev1.Pod, error) {
					opts := metav1.ListOptions{LabelSelector: "console-name=" + console.Name}
					podList, err := client.CoreV1().Pods(namespace).List(opts)
					return podList.Items, err
				}).Should(HaveLen(1), "expected to find a single pod")

				By("Expect the console phase is Running")
				Eventually(func() workloadsv1alpha1.ConsolePhase {
					console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(console.Name, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred(), "could not find console")
					return console.Status.Phase
				}).Should(Equal(workloadsv1alpha1.ConsoleRunning))

				By("Expect the console phase eventually changes to Stopped")
				Eventually(func() workloadsv1alpha1.ConsolePhase {
					console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(console.Name, metav1.GetOptions{})
					Expect(err).NotTo(HaveOccurred(), "could not find console")
					return console.Status.Phase
				}).Should(Equal(workloadsv1alpha1.ConsoleStopped))

				// TODO: attach to pod

				By("Expect that the console is deleted shortly after stopping, due to its TTL")
				Eventually(func() error {
					_, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(console.Name, metav1.GetOptions{})
					return err
				}, 12*time.Second).Should(HaveOccurred(), "expected not to find console, but did")

				By("Expect that the pod eventually terminates")
				Eventually(func() int {
					opts := metav1.ListOptions{LabelSelector: "console-name=" + console.Name}
					podList, _ := client.CoreV1().Pods(namespace).List(opts)
					return len(podList.Items)
				}).Should(Equal(0), "pod did not get deleted")
			})
		})

		Specify("Deleting a console template", func() {
			By("Create a console template")
			template := buildConsoleTemplate(nil, false)
			template, err := client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).Create(template)
			Expect(err).NotTo(HaveOccurred(), "could not create console template")

			By("Create a console")
			console := buildConsole()
			console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Create(console)
			Expect(err).NotTo(HaveOccurred(), "could not create console")

			By("Expect a console has been created")
			_, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(console.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "could not find console")

			By("Delete the console template")
			policy := metav1.DeletePropagationForeground
			err = client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).
				Delete(templateName, &metav1.DeleteOptions{PropagationPolicy: &policy})
			Expect(err).NotTo(HaveOccurred(), "could not delete console template")

			Eventually(func() error {
				_, err = client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).Get(templateName, metav1.GetOptions{})
				return err
			}).Should(HaveOccurred(), "expected console template to be deleted, it still exists")

			By("Expect that the console no longer exists")
			Eventually(func() error {
				_, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(console.Name, metav1.GetOptions{})
				return err
			}).Should(HaveOccurred(), "expected not to find console, but did")
		})

		Specify("Deleting a job", func() {
			By("Create a console template")
			template := buildConsoleTemplate(nil, false)
			template, err := client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).Create(template)
			Expect(err).NotTo(HaveOccurred(), "could not create console template")

			defer func() {
				By("(cleanup) Delete the console template")
				policy := metav1.DeletePropagationForeground
				err = client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).
					Delete(templateName, &metav1.DeleteOptions{PropagationPolicy: &policy})
				Expect(err).NotTo(HaveOccurred(), "could not delete console template")

				Eventually(func() error {
					_, err = client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).Get(templateName, metav1.GetOptions{})
					return err
				}).Should(HaveOccurred(), "expected console template to be deleted, it still exists")
			}()

			By("Create a console")
			console := buildConsole()
			console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Create(console)
			Expect(err).NotTo(HaveOccurred(), "could not create console")

			By("Expect a job has been created")
			Eventually(func() error {
				_, err = client.BatchV1().Jobs(namespace).Get(jobName, metav1.GetOptions{})
				return err
			}).ShouldNot(HaveOccurred(), "could not find job")

			By("Delete the job")
			policy := metav1.DeletePropagationForeground
			err = client.BatchV1().Jobs(namespace).Delete(jobName, &metav1.DeleteOptions{PropagationPolicy: &policy})
			Expect(err).NotTo(HaveOccurred(), "could not delete console job")

			By("Expect that the job no longer exists")
			Eventually(func() error {
				_, err = client.BatchV1().Jobs(namespace).Get(console.Name, metav1.GetOptions{})
				return err
			}).Should(HaveOccurred(), "expected not to find job, but did")

			By("Expect the console phase is Destroyed")
			Eventually(func() workloadsv1alpha1.ConsolePhase {
				console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(console.Name, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "could not find console")
				return console.Status.Phase
			}).Should(Equal(workloadsv1alpha1.ConsoleDestroyed))
		})
	})
}

func buildConsoleTemplate(ttl *int32, authorised bool) *workloadsv1alpha1.ConsoleTemplate {
	var (
		defaultAuthorisation *workloadsv1alpha1.ConsoleAuthorisers
		authorisationRules   []workloadsv1alpha1.ConsoleAuthorisationRule
	)

	if authorised {
		defaultAuthorisation = &workloadsv1alpha1.ConsoleAuthorisers{
			AuthorisationsRequired: 1,
			Subjects: []rbacv1.Subject{
				{Kind: "User", Name: user},
			},
		}
		authorisationRules = []workloadsv1alpha1.ConsoleAuthorisationRule{
			{
				Name:                 "no-review",
				MatchCommandElements: []string{"sleep", "1"},
				ConsoleAuthorisers: workloadsv1alpha1.ConsoleAuthorisers{
					AuthorisationsRequired: 0,
					Subjects:               []rbacv1.Subject{},
				},
			},
			{
				Name:                 "bad-command",
				MatchCommandElements: []string{"sleep", "666"},
				ConsoleAuthorisers: workloadsv1alpha1.ConsoleAuthorisers{
					AuthorisationsRequired: 1,
					Subjects: []rbacv1.Subject{
						{Kind: "User", Name: user},
					},
				},
			},
		}
	}

	return &workloadsv1alpha1.ConsoleTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      templateName,
			Namespace: namespace,
		},
		Spec: workloadsv1alpha1.ConsoleTemplateSpec{
			MaxTimeoutSeconds:              60,
			DefaultTTLSecondsAfterFinished: ttl,
			AdditionalAttachSubjects:       []rbacv1.Subject{rbacv1.Subject{Kind: "User", Name: "add-user@example.com"}},
			AuthorisationRules:             authorisationRules,
			DefaultAuthorisationRule:       defaultAuthorisation,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					// Set the grace period to 0, to ensure quick cleanup.
					TerminationGracePeriodSeconds: new(int64),
					Containers: []corev1.Container{
						corev1.Container{
							Image:   "alpine:latest",
							Name:    "console-container-0",
							Command: []string{"false", "false", "false"},
						},
					},
					RestartPolicy: "Never",
				},
			},
		},
	}
}

func buildConsole() *workloadsv1alpha1.Console {
	return &workloadsv1alpha1.Console{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consoleName,
			Namespace: namespace,
		},
		Spec: workloadsv1alpha1.ConsoleSpec{
			Command:            []string{"sleep", "30"},
			ConsoleTemplateRef: corev1.LocalObjectReference{Name: templateName},
			TimeoutSeconds:     6,
		},
	}
}
