package integration

import (
	"context"
	"time"

	kitlog "github.com/go-kit/kit/log"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/pkg/integration"
	"github.com/gocardless/theatre/pkg/workloads/console"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	timeout = 5 * time.Second
	logger  = kitlog.NewLogfmtLogger(GinkgoWriter)
)

var _ = Describe("Console", func() {
	var (
		ctx               context.Context
		cancel            func()
		namespace         string
		teardown          func()
		mgr               manager.Manager
		consoleController controller.Controller
		calls             chan integration.ReconcileCall
		whcalls           chan integration.HandleCall
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), time.Minute)
		namespace, teardown = integration.CreateNamespace(clientset)
		mgr = integration.StartTestManager(ctx, cfg)

		consoleController = integration.MustController(
			console.Add(ctx, logger, mgr,
				func(opt *controller.Options) {
					opt.Reconciler, calls = integration.CaptureReconcile(
						opt.Reconciler,
					)
				},
			),
		)

		integration.NewServer(mgr, integration.MustWebhook(
			console.NewWebhook(logger, mgr,
				func(handler *admission.Handler) {
					*handler, whcalls = integration.CaptureWebhook(mgr, *handler)
				},
			),
		))
	})

	AfterEach(func() {
		cancel()
		teardown()
	})

	Describe("Creating resources", func() {
		It("Sets console.spec.user from rbac", func() {
			consoleTemplate := &workloadsv1alpha1.ConsoleTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-first-console-template",
					Namespace: namespace,
				},
				Spec: workloadsv1alpha1.ConsoleTemplateSpec{
					AdditionalAttachSubjects: []rbacv1.Subject{},
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								corev1.Container{
									Image:   "alpine:latest",
									Name:    "console-container-0",
									Command: []string{"sleep", "100"},
								},
							},
							RestartPolicy: "Never",
						},
					},
				},
			}

			By("Creating console template")
			Expect(mgr.GetClient().Create(context.TODO(), consoleTemplate)).NotTo(
				HaveOccurred(), "failed to create Console Template",
			)

			csl := &workloadsv1alpha1.Console{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-first-console",
					Namespace: namespace,
				},
				Spec: workloadsv1alpha1.ConsoleSpec{
					ConsoleTemplateRef: corev1.LocalObjectReference{Name: "my-first-console-template"},
					User:               "", // deliberately not configured, this should be set by the webhook
				},
			}

			By("Creating console")
			Expect(mgr.GetClient().Create(context.TODO(), csl)).NotTo(
				HaveOccurred(), "failed to create Console",
			)

			By("Expect webhook was invoked")
			Eventually(whcalls, timeout).Should(
				Receive(
					integration.HandleResource(namespace, "my-first-console"),
				),
			)

			By("Expect reconcile succeeded")
			Eventually(calls, timeout).Should(
				Receive(
					integration.ReconcileResourceSuccess(namespace, "my-first-console"),
				),
			)

			By("Expect console.spec.user to be set")
			Expect(csl.Spec.User).To(Equal("system:unsecured"))
		})

		It("Creates a job", func() {
			consoleTemplate := &workloadsv1alpha1.ConsoleTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-first-console-template",
					Namespace: namespace,
				},
				Spec: workloadsv1alpha1.ConsoleTemplateSpec{
					AdditionalAttachSubjects: []rbacv1.Subject{},
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								corev1.Container{
									Image:   "alpine:latest",
									Name:    "console-container-0",
									Command: []string{"sleep", "100"},
								},
							},
							RestartPolicy: "Never",
						},
					},
				},
			}

			By("Creating console template")
			Expect(mgr.GetClient().Create(context.TODO(), consoleTemplate)).NotTo(
				HaveOccurred(), "failed to create Console Template",
			)

			csl := &workloadsv1alpha1.Console{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-first-console",
					Namespace: namespace,
				},
				Spec: workloadsv1alpha1.ConsoleSpec{
					ConsoleTemplateRef: corev1.LocalObjectReference{Name: "my-first-console-template"},
				},
			}

			By("Creating console")
			Expect(mgr.GetClient().Create(context.TODO(), csl)).NotTo(
				HaveOccurred(), "failed to create Console",
			)

			By("Expect reconcile succeeded")
			Eventually(calls, timeout).Should(
				Receive(
					integration.ReconcileResourceSuccess(namespace, "my-first-console"),
				),
			)

			By("Expect job was created")
			job := &batchv1.Job{}
			identifier, _ := client.ObjectKeyFromObject(csl)
			err := mgr.GetClient().Get(context.TODO(), identifier, job)

			Expect(err).NotTo(HaveOccurred(), "failed to find associated Job for Console")
			// Expect(job.Spec.Template).To(Equal(consoleTemplate.Spec.Template), "the job's pod spec should match the console template")
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal("alpine:latest"), "the job's pod runs the same container as specified in the console template")
			// TODO: Test for correct logs
		})

		It("Only creates one job when reconciling twice", func() {
			consoleTemplate := &workloadsv1alpha1.ConsoleTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-first-console-template",
					Namespace: namespace,
				},
				Spec: workloadsv1alpha1.ConsoleTemplateSpec{
					AdditionalAttachSubjects: []rbacv1.Subject{},
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								corev1.Container{
									Image:   "alpine:latest",
									Name:    "console-container-0",
									Command: []string{"sleep", "100"},
								},
							},
							RestartPolicy: "Never",
						},
					},
				},
			}

			By("Creating console template")
			Expect(mgr.GetClient().Create(context.TODO(), consoleTemplate)).NotTo(
				HaveOccurred(), "failed to create Console Template",
			)
			csl := &workloadsv1alpha1.Console{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-first-pod",
					Namespace: namespace,
				},
				Spec: workloadsv1alpha1.ConsoleSpec{
					ConsoleTemplateRef: corev1.LocalObjectReference{Name: "my-first-console-template"},
				},
			}

			By("Creating console")
			Expect(mgr.GetClient().Create(context.TODO(), csl)).NotTo(
				HaveOccurred(), "failed to create Console",
			)

			By("Expect reconcile succeeded")
			Eventually(calls, timeout).Should(
				Receive(
					integration.ReconcileResourceSuccess(namespace, "my-first-pod"),
				),
			)

			By("Reconciling again")
			identifier, _ := client.ObjectKeyFromObject(csl)
			go func() { consoleController.Reconcile(reconcile.Request{identifier}) }()
			Eventually(calls, timeout).Should(
				Receive(
					integration.ReconcileResourceSuccess(namespace, "my-first-pod"),
				),
			)
			// TODO: check that the 'already exists' event was logged
		})
	})
})
