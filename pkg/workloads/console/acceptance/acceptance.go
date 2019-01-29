package acceptance

import (
	"time"

	kitlog "github.com/go-kit/kit/log"
	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/pkg/client/clientset/versioned"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const namespace = "default"
const consoleName = "console-0"

func Run(logger kitlog.Logger) {
	Describe("Consoles", func() {
		logger.Log("msg", "starting test")

		config := config.GetConfigOrDie()
		Expect(config).NotTo(BeNil(), "could not construct kubernetes config")

		// Construct a client for the workloads API Group
		workloadsClientSet, err := versioned.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred(), "could not connect to kubernetes cluster")
		workloads := workloadsClientSet.WorkloadsV1alpha1()

		// Construct a client for the core Kubernetes API Groups
		core, err := kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred(), "could not connect to kubernetes cluster")

		// Wait for MutatingWebhookConfig to be created
		Eventually(func() bool {
			_, err := core.Admissionregistration().MutatingWebhookConfigurations().Get("theatre-workloads", metav1.GetOptions{})
			if err != nil {
				logger.Log("error", err)
				return false
			}
			return true
		}).Should(Equal(true))

		// Create a console template
		template := buildConsoleTemplate()
		template, err = workloads.ConsoleTemplates(namespace).Create(template)
		Expect(err).NotTo(HaveOccurred(), "could not create console template")

		// Create a console
		console := buildConsole()
		console, err = workloads.Consoles(namespace).Create(console)
		Expect(err).NotTo(HaveOccurred(), "could not create console")

		By("Expect a console has been created")
		_, err = workloads.Consoles(namespace).Get(consoleName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "could not find console")

		By("Expect a job has been created")
		Eventually(func() error {
			_, err = core.BatchV1().Jobs(namespace).Get(consoleName, metav1.GetOptions{})
			return err
		}).ShouldNot(HaveOccurred(), "could not find job")

		By("Expect a pod has been created")
		Eventually(func() ([]corev1.Pod, error) {
			opts := metav1.ListOptions{LabelSelector: "job-name=console-0"}
			podList, err := core.CoreV1().Pods(namespace).List(opts)
			return podList.Items, err
		}).Should(HaveLen(1), "expected to find a single pod")

		By("Expect the console phase is Running")
		Eventually(func() workloadsv1alpha1.ConsolePhase {
			console, err = workloads.Consoles(namespace).Get(consoleName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "could not find console")
			return console.Status.Phase
		}).Should(Equal(workloadsv1alpha1.ConsoleRunning))

		By("Expect the console phase eventually changes to Stopped")
		timeout := 7 * time.Second
		pollInterval := time.Second
		Eventually(func() workloadsv1alpha1.ConsolePhase {
			console, err = workloads.Consoles(namespace).Get(consoleName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "could not find console")
			return console.Status.Phase
		}, timeout, pollInterval).Should(Equal(workloadsv1alpha1.ConsoleStopped))

		// TODO: attach to pod

		By("Expect that the pod eventually gets terminated")
		Eventually(func() int {
			opts := metav1.ListOptions{LabelSelector: "job-name=console-0"}
			podList, _ := core.CoreV1().Pods(namespace).List(opts)
			return len(podList.Items)
		}).Should(Equal(0), "pod did not get deleted")

		By("Expect that the job still exists")
		_, err = core.BatchV1().Jobs(namespace).Get(consoleName, metav1.GetOptions{})
		Expect(err).To(BeNil(), "could not find job")
	})
}

func buildConsoleTemplate() *workloadsv1alpha1.ConsoleTemplate {
	return &workloadsv1alpha1.ConsoleTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "console-template-0",
			Namespace: namespace,
		},
		Spec: workloadsv1alpha1.ConsoleTemplateSpec{
			AdditionalAttachSubjects: []rbacv1.Subject{rbacv1.Subject{Kind: "User", Name: "add-user@example.com"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						corev1.Container{
							Image:   "alpine:latest",
							Name:    "console-container-0",
							Command: []string{"sleep", "30"},
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
			ConsoleTemplateRef: corev1.LocalObjectReference{Name: "console-template-0"},
			TimeoutSeconds:     5,
		},
	}
}
