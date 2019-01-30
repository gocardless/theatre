package integration

import (
	"context"
	"fmt"
	"time"

	kitlog "github.com/go-kit/kit/log"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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
		ctx                        context.Context
		cancel                     func()
		namespace                  string
		teardown                   func()
		mgr                        manager.Manager
		calls                      chan integration.ReconcileCall
		whcalls                    chan integration.HandleCall
		csl                        *workloadsv1alpha1.Console
		waitForSuccessfulReconcile func(int)
		consoleTemplate            *workloadsv1alpha1.ConsoleTemplate
		createConsole              func(int)
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), time.Minute)
		namespace, teardown = integration.CreateNamespace(clientset)
		mgr = integration.StartTestManager(ctx, cfg)

		integration.MustController(
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

		By("Creating console template")
		consoleTemplate = buildConsoleTemplate(namespace)
		Expect(mgr.GetClient().Create(context.TODO(), consoleTemplate)).NotTo(
			HaveOccurred(), "failed to create Console Template",
		)

		createConsole = func(timeout int) {
			csl = &workloadsv1alpha1.Console{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "console-0",
					Namespace: namespace,
				},
				Spec: workloadsv1alpha1.ConsoleSpec{
					ConsoleTemplateRef: corev1.LocalObjectReference{Name: "console-template-0"},
					TimeoutSeconds:     timeout,
					User:               "", // deliberately blank: this should be set by the webhook
				},
			}

			By("Creating console")
			Expect(mgr.GetClient().Create(context.TODO(), csl)).NotTo(
				HaveOccurred(), "failed to create Console",
			)
		}

		waitForSuccessfulReconcile = func(times int) {
			// Wait twice for reconcile: the second reconciliation is triggered due to
			// the update of the status field with an expiry time
			for i := 1; i <= times; i++ {
				By(fmt.Sprintf("Expect reconcile succeeded (%d of %d)", i, times))
				Eventually(calls, timeout).Should(
					Receive(
						integration.ReconcileResourceSuccess(namespace, "console-0"),
					),
				)
			}
			By("Reconcile done")
		}
	})

	AfterEach(func() {
		cancel()
		teardown()
	})

	Describe("enforcing valid timeout values", func() {
		It("Enforces the console template's MaxTimeoutSeconds", func() {
			By("Creating a console with a timeout > MaxTimeoutSeconds")
			createConsole(7201)

			waitForSuccessfulReconcile(2)

			By("Expect console has timeout == MaxTimeoutSeconds")
			identifier, _ := client.ObjectKeyFromObject(csl)
			err := mgr.GetClient().Get(context.TODO(), identifier, csl)
			Expect(err).NotTo(HaveOccurred(), "failed to find Console")
			Expect(
				csl.Spec.TimeoutSeconds).To(BeNumerically("==", 7200),
				"console's timeout does not match template's MaxTimeoutSeconds",
			)
		})

		It("Uses the template's DefaultTimeoutSeconds if the template's timeout is zero", func() {
			By("Creating a console with a timeout of 0")
			createConsole(0)

			waitForSuccessfulReconcile(2)

			By("Expect console has timeout == MaxTimeoutSeconds")
			identifier, _ := client.ObjectKeyFromObject(csl)
			err := mgr.GetClient().Get(context.TODO(), identifier, csl)
			Expect(err).NotTo(HaveOccurred(), "failed to find Console")
			Expect(
				csl.Spec.TimeoutSeconds).To(BeNumerically("==", 600),
				"console's timeout does not match template's DefaultTimeoutSeconds",
			)
		})

		It("Keeps the console's timeout if it is valid", func() {
			By("Creating a console with a timeout > MaxTimeoutSeconds")
			createConsole(7199)

			waitForSuccessfulReconcile(2)

			By("Expect console has timeout == MaxTimeoutSeconds")
			identifier, _ := client.ObjectKeyFromObject(csl)
			err := mgr.GetClient().Get(context.TODO(), identifier, csl)
			Expect(err).NotTo(HaveOccurred(), "failed to find Console")
			Expect(
				csl.Spec.TimeoutSeconds).To(BeNumerically("==", 7199),
				"console's timeout does not match template's MaxTimeoutSeconds",
			)
		})

		// Negative timeouts are not permitted by the openapi validations, so we don't need to
		// test that here.
	})

	Describe("Creating resources", func() {
		BeforeEach(func() {
			createConsole(3600)
			waitForSuccessfulReconcile(2)
		})

		It("Sets console.spec.user from rbac", func() {
			By("Expect webhook was invoked")
			Eventually(whcalls, timeout).Should(
				Receive(
					integration.HandleResource(namespace, "console-0"),
				),
			)

			By("Expect console.spec.user to be set")
			Expect(csl.Spec.User).To(Equal("system:unsecured"))
		})

		It("Creates a job", func() {
			By("Expect job was created")
			job := &batchv1.Job{}
			identifier, _ := client.ObjectKeyFromObject(csl)
			err := mgr.GetClient().Get(context.TODO(), identifier, job)

			Expect(err).NotTo(HaveOccurred(), "failed to find associated Job for Console")
			Expect(
				job.Spec.Template.Spec.Containers[0].Image).To(Equal("alpine:latest"),
				"the job's pod runs the same container as specified in the console template",
			)
			Expect(
				job.ObjectMeta.Labels["user"]).To(Equal("system-unsecured"))
			Expect(
				*job.Spec.ActiveDeadlineSeconds).To(BeNumerically("==", csl.Spec.TimeoutSeconds),
				"job's ActiveDeadlineSeconds does not match console's timeout",
			)
		})

		It("Only creates one job when reconciling twice", func() {
			By("Retrieving latest console object")
			updatedCsl := &workloadsv1alpha1.Console{}
			identifier, _ := client.ObjectKeyFromObject(csl)
			err := mgr.GetClient().Get(context.TODO(), identifier, updatedCsl)
			Expect(err).NotTo(HaveOccurred(), "failed to retrieve console")

			By("Reconciling again")
			updatedCsl.Spec.Reason = "a different reason"
			err = mgr.GetClient().Update(context.TODO(), updatedCsl)
			Expect(err).NotTo(HaveOccurred(), "failed to update console")

			waitForSuccessfulReconcile(1)
			// TODO: check that the 'already exists' event was logged
		})

		It("Creates a pods/exec rolebinding for the user", func() {
			By("Expect role was created")
			role := &rbacv1.Role{}
			identifier, _ := client.ObjectKeyFromObject(csl)
			err := mgr.GetClient().Get(context.TODO(), identifier, role)

			Expect(err).NotTo(HaveOccurred(), "failed to find role")
			Expect(role.Rules).To(
				Equal(
					[]rbacv1.PolicyRule{
						rbacv1.PolicyRule{
							Verbs:         []string{"*"},
							APIGroups:     []string{"core"},
							Resources:     []string{"pods/exec"},
							ResourceNames: []string{"console-0"},
						},
					},
				),
				"role rule did not match expectation",
			)

			By("Expect role is owned by console")
			Expect(role.ObjectMeta.OwnerReferences).To(HaveLen(1))
			Expect(role.ObjectMeta.OwnerReferences[0].Name).To(Equal(csl.ObjectMeta.Name))

			By("Expect rolebinding was created for user and AdditionalAttachSubjects")
			rb := &rbacv1.RoleBinding{}
			identifier, _ = client.ObjectKeyFromObject(csl)
			err = mgr.GetClient().Get(context.TODO(), identifier, rb)

			Expect(err).NotTo(HaveOccurred(), "failed to find associated RoleBinding")
			Expect(rb.RoleRef).To(
				Equal(
					rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "console-0"},
				),
			)
			Expect(rb.Subjects).To(
				ConsistOf([]rbacv1.Subject{
					rbacv1.Subject{Kind: "User", APIGroup: "rbac.authorization.k8s.io", Name: csl.Spec.User},
					rbacv1.Subject{Kind: "User", APIGroup: "rbac.authorization.k8s.io", Name: "add-user@example.com"},
				}),
			)

			By("Expect rolebinding is owned by console")
			Expect(rb.ObjectMeta.OwnerReferences).To(HaveLen(1))
			Expect(rb.ObjectMeta.OwnerReferences[0].Name).To(Equal(csl.ObjectMeta.Name))
		})

		It("Updates the status with expiry time", func() {
			updatedCsl := &workloadsv1alpha1.Console{}
			identifier, _ := client.ObjectKeyFromObject(csl)
			err := mgr.GetClient().Get(context.TODO(), identifier, updatedCsl)
			Expect(err).NotTo(HaveOccurred(), "failed to retrieve updated console")

			Expect(updatedCsl.Status).NotTo(BeNil(), "the console status should be defined")
			Expect(updatedCsl.Status.ExpiryTime).NotTo(BeNil(), "the console expiry time should be set")
			Expect(
				updatedCsl.Status.ExpiryTime.Time.After(time.Now())).To(BeTrue(),
				"the console expiry time should be after now()",
			)
		})

		It("Sets the owner of the console to be the template", func() {
			By("Retrieving latest console object")
			identifier, _ := client.ObjectKeyFromObject(csl)
			err := mgr.GetClient().Get(context.TODO(), identifier, csl)
			Expect(err).NotTo(HaveOccurred(), "failed to retrieve console")

			By("Expect console is owned by console template")
			Expect(csl.ObjectMeta.OwnerReferences).To(HaveLen(1))
			Expect(csl.ObjectMeta.OwnerReferences[0].Name).To(Equal(consoleTemplate.ObjectMeta.Name))
		})
	})
})

func buildConsoleTemplate(namespace string) *workloadsv1alpha1.ConsoleTemplate {
	return &workloadsv1alpha1.ConsoleTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "console-template-0",
			Namespace: namespace,
		},
		Spec: workloadsv1alpha1.ConsoleTemplateSpec{
			DefaultTimeoutSeconds:    600,
			MaxTimeoutSeconds:        7200,
			AdditionalAttachSubjects: []rbacv1.Subject{rbacv1.Subject{Kind: "User", Name: "add-user@example.com"}},
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
}
