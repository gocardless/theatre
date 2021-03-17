package acceptance

import (
	"context"
	"time"

	kitlog "github.com/go-kit/kit/log"
	"k8s.io/api/admissionregistration/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rbacv1alpha1 "github.com/gocardless/theatre/v3/apis/rbac/v1alpha1"
	workloadsv1alpha1 "github.com/gocardless/theatre/v3/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/v3/pkg/workloads/console/runner"

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

var (
	scheme = runtime.NewScheme()
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = workloadsv1alpha1.AddToScheme(scheme)
	_ = rbacv1alpha1.AddToScheme(scheme)
}

func newClient(config *rest.Config) client.Client {
	kubeClient, err := client.New(config, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred(), "could not connect to kubernetes cluster")

	return kubeClient
}

// The console template is the ultimate owner of all resources created during
// this test, so by removing it we will clean up all objects.
func deleteConsoleTemplate(kubeClient client.Client) {
	By("Delete the console template")

	policy := metav1.DeletePropagationForeground
	opts := &client.DeleteOptions{PropagationPolicy: &policy}
	template := &workloadsv1alpha1.ConsoleTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: templateName}}
	_ = kubeClient.Delete(context.TODO(), template, opts)

	Eventually(func() metav1.StatusReason {
		err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: templateName}, template)
		return apierrors.ReasonForError(err)
	}).Should(Equal(metav1.StatusReasonNotFound), "expected console template to be deleted, it still exists")
}

type Runner struct{}

func (r *Runner) Name() string {
	return "pkg/workloads-manager/acceptance"
}

func (r *Runner) Prepare(logger kitlog.Logger, config *rest.Config) error {
	return nil
}

func (r *Runner) Run(logger kitlog.Logger, config *rest.Config) {
	Describe("Consoles", func() {
		var (
			kubeClient client.Client
		)

		BeforeEach(func() {
			logger.Log("msg", "starting test")

			kubeClient = newClient(config)

			// Wait for MutatingWebhookConfig to be created
			Eventually(func() bool {
				mutatingWebhookConfig := &v1beta1.MutatingWebhookConfiguration{}
				err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: "theatre-system", Name: "theatre-workloads"}, mutatingWebhookConfig)
				if err != nil {
					logger.Log("error", err)
					return false
				}
				return true
			}).Should(Equal(true))

			// Because we use the same namespace for each spec, remove any templates
			// that are left over from previous failed runs to avoid 'already exists'
			// errors.
			deleteConsoleTemplate(kubeClient)
		})

		AfterEach(func() {
			deleteConsoleTemplate(kubeClient)
		})

		Specify("Happy path", func() {
			By("Create a console template")
			var TTLBeforeRunning int32 = 60
			var TTLAfterFinished int32 = 10
			template := buildConsoleTemplate(&TTLBeforeRunning, &TTLAfterFinished, true)
			err := kubeClient.Create(context.TODO(), template)
			Expect(err).NotTo(HaveOccurred(), "could not create console template")

			By("Create a console")
			console := buildConsole()
			console.Spec.Command = []string{"sleep", "666"}
			err = kubeClient.Create(context.TODO(), console)
			Expect(err).NotTo(HaveOccurred(), "could not create console")

			By("Expect an authorisation has been created")
			consoleAuthorisation := &workloadsv1alpha1.ConsoleAuthorisation{}
			Eventually(func() error {
				err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, consoleAuthorisation)
				return err
			}).ShouldNot(HaveOccurred(), "could not find authorisation")

			By("Expect the console phase is pending authorisation")
			Eventually(func() workloadsv1alpha1.ConsolePhase {
				csl := &workloadsv1alpha1.Console{}
				err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, csl)
				Expect(err).NotTo(HaveOccurred(), "could not find console")
				return csl.Status.Phase
			}).Should(Equal(workloadsv1alpha1.ConsolePendingAuthorisation))

			By("Expect that the job has not been created")
			Eventually(func() error {
				job := &batchv1.Job{}
				err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: jobName}, job)
				return err
			}).Should(HaveOccurred(), "expected not to find job, but did")

			// Change the console user to another user as a user cannot authorise their own console
			By("Update the console user")
			err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
			Expect(err).NotTo(HaveOccurred(), "could not get console to update console user")
			console.Spec.User = "another-user@example.com"
			err = kubeClient.Update(context.TODO(), console)
			Expect(err).NotTo(HaveOccurred(), "could not update console user")

			By("Authorise a console")
			consoleAuthorisation.Spec.Authorisations = []rbacv1.Subject{{Kind: "User", Name: user}}
			err = kubeClient.Update(context.TODO(), consoleAuthorisation)
			Expect(err).NotTo(HaveOccurred(), "could not authorise console")

			By("Expect a job has been created")
			Eventually(func() error {
				job := &batchv1.Job{}
				err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: jobName}, job)
				return err
			}).ShouldNot(HaveOccurred(), "could not find job")

			By("Expect a pod has been created")
			Eventually(func() ([]corev1.Pod, error) {
				selectorSet, err := labels.ConvertSelectorToLabelsMap("job-name=" + jobName)
				if err != nil {
					return nil, err
				}
				opts := &client.ListOptions{Namespace: namespace, LabelSelector: labels.SelectorFromSet(selectorSet)}
				podList := &corev1.PodList{}
				kubeClient.List(context.TODO(), podList, opts)
				return podList.Items, err
			}).Should(HaveLen(1), "expected to find a single pod")

			By("Expect the console phase is Running")
			Eventually(func() workloadsv1alpha1.ConsolePhase {
				csl := &workloadsv1alpha1.Console{}
				err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, csl)
				Expect(err).NotTo(HaveOccurred(), "could not find console")
				return csl.Status.Phase
			}).Should(Equal(workloadsv1alpha1.ConsoleRunning))

			By("Expect the console phase eventually changes to Stopped")
			Eventually(func() workloadsv1alpha1.ConsolePhase {
				csl := &workloadsv1alpha1.Console{}
				err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, csl)
				Expect(err).NotTo(HaveOccurred(), "could not find console")
				return csl.Status.Phase
			}).Should(Equal(workloadsv1alpha1.ConsoleStopped))

			// TODO: attach to pod

			By("Expect that the job still exists")
			job := &batchv1.Job{}
			err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: jobName}, job)
			Expect(err).NotTo(HaveOccurred(), "could not find job")

			By("Expect that the console is deleted shortly after stopping, due to its TTL after running")
			Eventually(func() error {
				console := &workloadsv1alpha1.Console{}
				err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
				return err
			}, 12*time.Second).Should(HaveOccurred(), "expected not to find console, but did")

			By("Expect that the pod eventually terminates")
			Eventually(func() ([]corev1.Pod, error) {
				selectorSet, err := labels.ConvertSelectorToLabelsMap("job-name=" + jobName)
				if err != nil {
					return nil, err
				}
				opts := &client.ListOptions{Namespace: namespace, LabelSelector: labels.SelectorFromSet(selectorSet)}
				podList := &corev1.PodList{}
				kubeClient.List(context.TODO(), podList, opts)
				return podList.Items, err
			}).Should(HaveLen(0), "pod did not get deleted")
		})

		Describe("Runner interface", func() {
			var (
				consoleRunner *runner.Runner
				console       *workloadsv1alpha1.Console
				createError   error
			)

			BeforeEach(func() {
				var err error
				consoleRunner, err = runner.New(config)
				Expect(err).NotTo(HaveOccurred(), "could not create runner")
			})

			Specify("Happy path", func() {
				By("Create a console template")
				var TTLBeforeRunning int32 = 60
				var TTLAfterFinished int32 = 10
				template := buildConsoleTemplate(&TTLBeforeRunning, &TTLAfterFinished, true)
				err := kubeClient.Create(context.TODO(), template, &client.CreateOptions{})
				Expect(err).NotTo(HaveOccurred(), "could not create console template")

				By("Create a console")
				go func() {
					// https://onsi.github.io/ginkgo/#marking-specs-as-failed
					defer GinkgoRecover()

					_, createError = consoleRunner.Create(context.TODO(), runner.CreateOptions{
						Namespace: namespace,
						Selector:  "app=acceptance",
						Timeout:   6 * time.Second,
						Reason:    "",
						Command:   []string{"sleep", "666"},
						Attach:    false,
					})

					Expect(createError).To(BeNil())
				}()

				By("Wait for the console to be created")
				Eventually(func() *workloadsv1alpha1.Console {
					opts := &client.ListOptions{Namespace: namespace}
					consoleList := &workloadsv1alpha1.ConsoleList{}
					err := kubeClient.List(context.TODO(), consoleList, opts)

					Expect(err).To(BeNil(), "Failed to list consoles")

					if len(consoleList.Items) == 1 {
						console = &consoleList.Items[0]
						return &consoleList.Items[0]
					}

					return nil
				}).ShouldNot(BeNil())

				By("Expect an authorisation has been created")
				Eventually(func() error {
					consoleAuthorisation := &workloadsv1alpha1.ConsoleAuthorisation{}
					err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, consoleAuthorisation)
					return err
				}).ShouldNot(HaveOccurred(), "could not find authorisation")

				By("Expect the console phase is pending authorisation")
				Eventually(func() workloadsv1alpha1.ConsolePhase {
					err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
					Expect(err).NotTo(HaveOccurred(), "could not find console")
					return console.Status.Phase
				}).Should(Equal(workloadsv1alpha1.ConsolePendingAuthorisation))

				By("Expect that the job has not been created")
				Eventually(func() error {
					job := &batchv1.Job{}
					err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name + "-console"}, job)
					return err
				}).Should(HaveOccurred(), "expected not to find job, but did")

				// Change the console user to another user as a user cannot authorise their own console
				By("Update the console user")
				Eventually(func() error {
					err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
					console.Spec.User = "another-user@example.com"
					return kubeClient.Update(context.TODO(), console)
				}).ShouldNot(HaveOccurred(), "could not update console user")

				By("Authorise a console")
				err = consoleRunner.Authorise(context.TODO(), runner.AuthoriseOptions{
					Namespace:   namespace,
					ConsoleName: console.Name,
					Username:    user,
				})
				Expect(err).NotTo(HaveOccurred(), "could not authorise console")

				By("Expect a pod has been created")
				Eventually(func() ([]corev1.Pod, error) {
					selectorSet, err := labels.ConvertSelectorToLabelsMap("console-name=" + console.Name)
					if err != nil {
						return nil, err
					}
					opts := &client.ListOptions{Namespace: namespace, LabelSelector: labels.SelectorFromSet(selectorSet)}
					podList := &corev1.PodList{}
					kubeClient.List(context.TODO(), podList, opts)

					return podList.Items, err
				}).Should(HaveLen(1), "expected to find a single pod")

				By("Expect the console phase is Running")
				Eventually(func() workloadsv1alpha1.ConsolePhase {
					err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
					Expect(err).NotTo(HaveOccurred(), "could not find console")
					return console.Status.Phase
				}).Should(Equal(workloadsv1alpha1.ConsoleRunning))

				By("Expect the console phase eventually changes to Stopped")
				Eventually(func() workloadsv1alpha1.ConsolePhase {
					err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
					Expect(err).NotTo(HaveOccurred(), "could not find console")
					return console.Status.Phase
				}).Should(Equal(workloadsv1alpha1.ConsoleStopped))

				// TODO: attach to pod

				By("Expect that the console is deleted shortly after stopping, due to its TTL")
				Eventually(func() error {
					err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
					return err
				}, 12*time.Second).Should(HaveOccurred(), "expected not to find console, but did")

				By("Expect that the pod eventually terminates")
				Eventually(func() ([]corev1.Pod, error) {
					selectorSet, err := labels.ConvertSelectorToLabelsMap("console-name=" + console.Name)
					if err != nil {
						return nil, err
					}
					opts := &client.ListOptions{Namespace: namespace, LabelSelector: labels.SelectorFromSet(selectorSet)}
					podList := &corev1.PodList{}
					kubeClient.List(context.TODO(), podList, opts)
					return podList.Items, err
				}).Should(HaveLen(0), "pod did not get deleted")
			})
		})

		Specify("Deleting a console template", func() {
			By("Create a console template")
			template := buildConsoleTemplate(nil, nil, false)
			err := kubeClient.Create(context.TODO(), template)
			Expect(err).NotTo(HaveOccurred(), "could not create console template")

			By("Create a console")
			console := buildConsole()
			err = kubeClient.Create(context.TODO(), console)
			Expect(err).NotTo(HaveOccurred(), "could not create console")

			By("Expect a console has been created")
			err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
			Expect(err).NotTo(HaveOccurred(), "could not find console")

			By("Expect consoleTemplate becomes console owner")
			Eventually(func() []metav1.OwnerReference {
				kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
				return console.OwnerReferences
			}).Should(HaveLen(1), "console has no owners")
			Expect(console.OwnerReferences[0].Name).To(Equal(template.Name), "consoleTemplate is not set as console owner")

			// Leave this assertion rather than using the deleteConsoleTemplate
			// function, as it's useful to assert on any errors that are returned
			// from the deletion operation, which deleteConsoleTemplate deliberately
			// omits.
			By("Delete the console template")
			policy := metav1.DeletePropagationForeground
			opts := &client.DeleteOptions{PropagationPolicy: &policy}
			template = &workloadsv1alpha1.ConsoleTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: templateName}}
			err = kubeClient.Delete(context.TODO(), template, opts)
			Expect(err).NotTo(HaveOccurred(), "could not delete console template")

			Eventually(func() error {
				err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: template.Name}, template)
				return err
			}).Should(HaveOccurred(), "expected console template to be deleted, it still exists")

			By("Expect that the console no longer exists")
			Eventually(func() error {
				err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
				return err
			}).Should(HaveOccurred(), "expected not to find console, but did")
		})

		Specify("Deleting a job", func() {
			By("Create a console template")
			template := buildConsoleTemplate(nil, nil, false)
			err := kubeClient.Create(context.TODO(), template)
			Expect(err).NotTo(HaveOccurred(), "could not create console template")

			By("Create a console")
			console := buildConsole()
			err = kubeClient.Create(context.TODO(), console)
			Expect(err).NotTo(HaveOccurred(), "could not create console")

			By("Expect a job has been created")
			Eventually(func() error {
				job := &batchv1.Job{}
				err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: jobName}, job)
				return err
			}).ShouldNot(HaveOccurred(), "could not find job")

			By("Delete the job")
			policy := metav1.DeletePropagationForeground
			opts := &client.DeleteOptions{PropagationPolicy: &policy}
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: jobName}}
			err = kubeClient.Delete(context.TODO(), job, opts)

			Expect(err).NotTo(HaveOccurred(), "could not delete console job")

			By("Expect that the job no longer exists")
			Eventually(func() error {
				job := &batchv1.Job{}
				err := kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, job)
				return err
			}).Should(HaveOccurred(), "expected not to find job, but did")

			By("Expect the console phase is Destroyed")
			Eventually(func() workloadsv1alpha1.ConsolePhase {
				err = kubeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: console.Name}, console)
				Expect(err).NotTo(HaveOccurred(), "could not find console")
				return console.Status.Phase
			}).Should(Equal(workloadsv1alpha1.ConsoleDestroyed))
		})
	})
}

func buildConsoleTemplate(TTLBeforeRunning, TTLAfterFinished *int32, authorised bool) *workloadsv1alpha1.ConsoleTemplate {
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
			Labels: map[string]string{
				"app": "acceptance",
			},
		},
		Spec: workloadsv1alpha1.ConsoleTemplateSpec{
			MaxTimeoutSeconds:              60,
			DefaultTTLSecondsBeforeRunning: TTLBeforeRunning,
			DefaultTTLSecondsAfterFinished: TTLAfterFinished,
			AdditionalAttachSubjects:       []rbacv1.Subject{{Kind: "User", Name: "add-user@example.com"}},
			AuthorisationRules:             authorisationRules,
			DefaultAuthorisationRule:       defaultAuthorisation,
			Template: workloadsv1alpha1.PodTemplatePreserveMetadataSpec{
				Spec: corev1.PodSpec{
					// Set the grace period to 0, to ensure quick cleanup.
					TerminationGracePeriodSeconds: new(int64),
					Containers: []corev1.Container{
						{
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
			TimeoutSeconds:     10,
		},
	}
}
