package acceptance

import (
	"time"

	kitlog "github.com/go-kit/kit/log"
	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	workloadsclient "github.com/gocardless/theatre/pkg/client/clientset/versioned/typed/workloads/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	namespace    = "default"
	consoleName  = "console-0"
	templateName = "console-template-0"
	jobName      = "console-0-console"
)

// This clientset is a union of the default kubernetes clientset and the workloads client.
type clientset struct {
	*kubernetes.Clientset
	workloadsV1alpha1 *workloadsclient.WorkloadsV1alpha1Client
}

func (c *clientset) WorkloadsV1Alpha1() *workloadsclient.WorkloadsV1alpha1Client {
	return c.workloadsV1alpha1
}

func newClient(kubeConfigPath string) clientset {
	config, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	Expect(err).NotTo(HaveOccurred(), "could not construct kubernetes config")

	// Construct a client for the workloads API Group
	workloadsClient, err := workloadsclient.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred(), "could not connect to kubernetes cluster")

	// Construct a client for the core Kubernetes API Groups
	core, err := kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred(), "could not connect to kubernetes cluster")

	return clientset{Clientset: core, workloadsV1alpha1: workloadsClient}
}

func Run(logger kitlog.Logger, kubeConfigPath string) {
	Describe("Consoles", func() {
		var client clientset

		BeforeEach(func() {
			logger.Log("msg", "starting test")

			client = newClient(kubeConfigPath)

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
			template := buildConsoleTemplate(&ttl)
			template, err := client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).Create(template)
			Expect(err).NotTo(HaveOccurred(), "could not create console template")

			By("Create a console")
			console := buildConsole()
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
			}, 8*time.Second).Should(Equal(workloadsv1alpha1.ConsoleStopped))

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

		Specify("Deleting a console template", func() {
			By("Create a console template")
			template := buildConsoleTemplate(nil)
			template, err := client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).Create(template)
			Expect(err).NotTo(HaveOccurred(), "could not create console template")

			By("Create a console")
			console := buildConsole()
			console, err = client.WorkloadsV1Alpha1().Consoles(namespace).Create(console)
			Expect(err).NotTo(HaveOccurred(), "could not create console")

			By("Expect a console has been created")
			_, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(consoleName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "could not find console")

			By("Delete the console template")
			policy := metav1.DeletePropagationForeground
			err = client.WorkloadsV1Alpha1().ConsoleTemplates(namespace).
				Delete(templateName, &metav1.DeleteOptions{PropagationPolicy: &policy})
			Expect(err).NotTo(HaveOccurred(), "could not delete console template")

			By("Expect that the console no longer exists")
			Eventually(func() error {
				_, err = client.WorkloadsV1Alpha1().Consoles(namespace).Get(consoleName, metav1.GetOptions{})
				return err
			}).Should(HaveOccurred(), "expected not to find console, but did")
		})
	})
}

func buildConsoleTemplate(ttl *int32) *workloadsv1alpha1.ConsoleTemplate {
	return &workloadsv1alpha1.ConsoleTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      templateName,
			Namespace: namespace,
		},
		Spec: workloadsv1alpha1.ConsoleTemplateSpec{
			MaxTimeoutSeconds:              60,
			DefaultTTLSecondsAfterFinished: ttl,
			AdditionalAttachSubjects:       []rbacv1.Subject{rbacv1.Subject{Kind: "User", Name: "add-user@example.com"}},
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
			TimeoutSeconds:     66,
		},
	}
}
