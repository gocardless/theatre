package integration

import (
	"context"
	"fmt"
	"time"

	kitlog "github.com/go-kit/kit/log"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	rbacv1alpha1 "github.com/gocardless/theatre/pkg/apis/rbac/v1alpha1"
	workloadsv1alpha1 "github.com/gocardless/theatre/pkg/apis/workloads/v1alpha1"
	"github.com/gocardless/theatre/pkg/integration"
	"github.com/gocardless/theatre/pkg/workloads/console"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	// Expect 2 reconcile events:
	//   #1: In response to the creation of the console itself.
	//   #2: In response to the creation of the job.
	ReconcilesAfterCreate = 2
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
		consoleName                string
		csl                        *workloadsv1alpha1.Console
		consoleTemplate            *workloadsv1alpha1.ConsoleTemplate
		waitForSuccessfulReconcile func(int)
		mustCreateResources        func()
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

		integration.NewServer(
			mgr,
			integration.MustWebhook(
				console.NewAuthenticatorWebhook(logger, mgr,
					func(handler *admission.Handler) {
						*handler, whcalls = integration.CaptureWebhook(mgr, *handler)
					},
				),
			),
			integration.MustWebhook(console.NewTemplateValidationWebhook(logger, mgr)),
		)

		// Set a high default in case the tests are slow to execute
		defaultTTLSecondsBeforeRunning := int32(10)

		consoleTemplate = &workloadsv1alpha1.ConsoleTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "console-template-0",
				Namespace: namespace,
				Labels: map[string]string{
					"repo":        "myapp-owner-myapp-repo",
					"environment": "myapp-env",
				},
			},
			Spec: workloadsv1alpha1.ConsoleTemplateSpec{
				DefaultTTLSecondsBeforeRunning: &defaultTTLSecondsBeforeRunning,
				DefaultTimeoutSeconds:          600,
				MaxTimeoutSeconds:              7200,
				AdditionalAttachSubjects: []rbacv1.Subject{
					{Kind: "User", Name: "add-user@example.com"},
					{Kind: "GoogleGroup", Name: "group@example.com"},
				},
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							corev1.Container{
								Image:   "alpine:latest",
								Name:    "console-container-0",
								Command: []string{"/bin/sh", "-c", "sleep 100"},
							},
						},
						RestartPolicy: "OnFailure",
					},
				},
			},
		}

		consoleName = "console-0"
		csl = &workloadsv1alpha1.Console{
			ObjectMeta: metav1.ObjectMeta{
				Name:      consoleName,
				Namespace: namespace,
				Labels: map[string]string{
					"repo":        "no-app",
					"other-label": "other-value",
				},
			},
			Spec: workloadsv1alpha1.ConsoleSpec{
				Command:            []string{"bin/rails", "console", "--help"},
				ConsoleTemplateRef: corev1.LocalObjectReference{Name: "console-template-0"},
				TimeoutSeconds:     3600,
				User:               "", // deliberately blank: this should be set by the webhook
			},
		}

		waitForSuccessfulReconcile = func(times int) {
			// Wait twice for reconcile: the second reconciliation is triggered due to
			// the update of the status field with an expiry time
			for i := 1; i <= times; i++ {
				By(fmt.Sprintf("Expect reconcile succeeded (%d of %d)", i, times))
				Eventually(calls, timeout).Should(
					Receive(
						integration.ReconcileResourceSuccess(namespace, consoleName),
					),
				)
			}
			By("Reconcile done")
		}

		mustCreateResources = func() {
			By("Creating console template")
			Expect(mgr.GetClient().Create(context.TODO(), consoleTemplate)).NotTo(
				HaveOccurred(), "failed to create Console Template",
			)

			By("Creating console")
			Expect(mgr.GetClient().Create(context.TODO(), csl)).NotTo(
				HaveOccurred(), "failed to create Console",
			)
		}
	})

	AfterEach(func() {
		cancel()
		teardown()
	})

	Describe("Enforcing valid timeout values", func() {
		JustBeforeEach(func() {
			mustCreateResources()
		})

		Describe("Timeout > MaxTimeoutSeconds", func() {
			BeforeEach(func() {
				csl.Spec.TimeoutSeconds = 7201
			})

			It("Enforces the console template's MaxTimeoutSeconds", func() {
				By("Creating a console with a timeout > MaxTimeoutSeconds")

				waitForSuccessfulReconcile(ReconcilesAfterCreate)

				By("Expect console has timeout == MaxTimeoutSeconds")
				identifier, _ := client.ObjectKeyFromObject(csl)
				err := mgr.GetClient().Get(context.TODO(), identifier, csl)
				Expect(err).NotTo(HaveOccurred(), "failed to find Console")
				Expect(
					csl.Spec.TimeoutSeconds).To(BeNumerically("==", 7200),
					"console's timeout does not match template's MaxTimeoutSeconds",
				)
			})
		})

		Describe("Timeout = 0", func() {
			BeforeEach(func() {
				csl.Spec.TimeoutSeconds = 0
			})

			It("Uses the template's DefaultTimeoutSeconds", func() {
				By("Creating a console with a timeout of 0")

				waitForSuccessfulReconcile(ReconcilesAfterCreate)

				By("Expect console has timeout == MaxTimeoutSeconds")
				identifier, _ := client.ObjectKeyFromObject(csl)
				err := mgr.GetClient().Get(context.TODO(), identifier, csl)
				Expect(err).NotTo(HaveOccurred(), "failed to find Console")
				Expect(
					csl.Spec.TimeoutSeconds).To(BeNumerically("==", 600),
					"console's timeout does not match template's DefaultTimeoutSeconds",
				)
			})
		})

		Describe("Timeout < MaxTimeoutSeconds", func() {
			BeforeEach(func() {
				csl.Spec.TimeoutSeconds = 7199
			})

			It("Keeps the console's timeout", func() {
				waitForSuccessfulReconcile(ReconcilesAfterCreate)

				By("Expect console has timeout == MaxTimeoutSeconds")
				identifier, _ := client.ObjectKeyFromObject(csl)
				err := mgr.GetClient().Get(context.TODO(), identifier, csl)
				Expect(err).NotTo(HaveOccurred(), "failed to find Console")
				Expect(
					csl.Spec.TimeoutSeconds).To(BeNumerically("==", 7199),
					"console's timeout does not match template's MaxTimeoutSeconds",
				)
			})
		})

		// Negative timeouts are not permitted by the openapi validations, so we don't need to
		// test that here.
	})

	Describe("Creating resources", func() {
		JustBeforeEach(func() {
			mustCreateResources()
			waitForSuccessfulReconcile(ReconcilesAfterCreate)
		})

		It("Sets console.spec.user from rbac", func() {
			By("Expect webhook was invoked")
			Eventually(whcalls, timeout).Should(
				Receive(
					integration.HandleResource(namespace, consoleName),
				),
			)

			By("Expect console.spec.user to be set")
			Expect(csl.Spec.User).To(Equal("system:unsecured"))
		})

		It("Creates a job", func() {
			By("Expect job was created")
			job := &batchv1.Job{}
			identifier, _ := client.ObjectKeyFromObject(csl)
			identifier.Name += "-console"
			err := mgr.GetClient().Get(context.TODO(), identifier, job)

			Expect(err).NotTo(HaveOccurred(), "failed to find associated Job for Console")

			By("Expect job and pod labels are correctly populated from the console template and console")
			Expect(
				job.ObjectMeta.Labels["user"]).To(Equal("system-unsecured"),
				"job should have a user label matching the console owner",
			)
			Expect(
				job.ObjectMeta.Labels["console-name"]).To(Equal(csl.ObjectMeta.Name),
				"job should have a label console-name matching the console name",
			)
			Expect(
				job.ObjectMeta.Labels["repo"]).To(Equal(consoleTemplate.ObjectMeta.Labels["repo"]),
				"job has the same labels as the console template (preference over console labels)",
			)
			Expect(
				job.ObjectMeta.Labels["other-label"]).To(Equal(csl.ObjectMeta.Labels["other-label"]),
				"job has the same labels as the console",
			)
			Expect(
				job.Spec.Template.Labels["console-name"]).To(Equal(csl.ObjectMeta.Name),
				"pod's has labels inherited from console and console template",
			)

			By("Expect job spec is correct")
			Expect(
				job.Spec.Template.Spec.Containers[0].Image).To(Equal("alpine:latest"),
				"the job's pod runs the same container as specified in the console template",
			)
			Expect(
				*job.Spec.ActiveDeadlineSeconds).To(BeNumerically("==", csl.Spec.TimeoutSeconds),
				"job's ActiveDeadlineSeconds does not match console's timeout",
			)
			Expect(
				job.Spec.Template.Spec.Containers[0].Command).To(Equal([]string{"bin/rails"}),
				"job's command does not match the first command element in the spec",
			)
			Expect(
				job.Spec.Template.Spec.Containers[0].Args).To(Equal([]string{"console", "--help"}),
				"job's args does not match the other command elements in the spec",
			)
			Expect(
				*job.Spec.BackoffLimit).To(BeNumerically("==", 0),
				"job's BackoffLimit is not 0",
			)
			Expect(
				job.Spec.Template.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever),
				"job's pod restartPolicy should always be set to 'Never'",
			)
			Expect(
				job.Spec.Template.Spec.Containers[0].Stdin).To(BeTrue(),
				"job's first container should have stdin true",
			)
			Expect(
				job.Spec.Template.Spec.Containers[0].TTY).To(BeTrue(),
				"job's first container should have tty true",
			)
		})

		It("Triggers a reconcile when updating a job", func() {
			parallelism := int32(20)
			defaultParallelism := int32(1)

			By("Expect job was created")
			job := &batchv1.Job{}
			identifier, _ := client.ObjectKeyFromObject(csl)
			identifier.Name += "-console"
			err := mgr.GetClient().Get(context.TODO(), identifier, job)
			Expect(err).NotTo(HaveOccurred(), "failed to find associated Job for Console")

			By("Modifying job")
			job.Spec.Parallelism = &parallelism
			err = mgr.GetClient().Update(context.TODO(), job)
			Expect(err).NotTo(HaveOccurred(), "failed to update Job")
			Expect(*job.Spec.Parallelism).To(Equal(parallelism))

			By("Expect job has properties restored")
			waitForSuccessfulReconcile(1)
			err = mgr.GetClient().Get(context.TODO(), identifier, job)
			Expect(err).NotTo(HaveOccurred(), "failed to retrieve Job")
			Expect(
				*job.Spec.Parallelism).To(Equal(defaultParallelism),
				"job Spec Parallelism is restored to original value",
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

		It("Creates a role and directory role binding for the user", func() {
			podName := fmt.Sprintf("%s-console-abcde", consoleName)
			jobName := fmt.Sprintf("%s-console", consoleName)

			By("Create a fake pod (to simulate a real job controller)")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: namespace,
					Labels:    labels.Set{"job-name": jobName},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Image: "alpine:latest",
							Name:  "console-container-0",
						},
					},
				},
			}
			err := mgr.GetClient().Create(context.TODO(), pod)
			Expect(err).NotTo(HaveOccurred(), "failed to create fake pod")

			// integration tests don't run controller manager. It's required to
			// fake the pod status as we wait for this Pod Phase in our
			// controller
			pod.Status.Phase = corev1.PodRunning

			err = mgr.GetClient().Status().Update(context.TODO(), pod)
			Expect(err).NotTo(HaveOccurred(), "failed to update fake pod status")

			By("Reconciling again")
			waitForSuccessfulReconcile(1)

			By("Expect role was created")
			role := &rbacv1.Role{}
			identifier, _ := client.ObjectKeyFromObject(csl)
			err = mgr.GetClient().Get(context.TODO(), identifier, role)

			Expect(err).NotTo(HaveOccurred(), "failed to find role")
			Expect(role.Rules).To(
				Equal(
					[]rbacv1.PolicyRule{
						rbacv1.PolicyRule{
							Verbs:         []string{"create"},
							APIGroups:     []string{""},
							Resources:     []string{"pods/exec", "pods/attach"},
							ResourceNames: []string{podName},
						},
						rbacv1.PolicyRule{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"pods/log"},
							ResourceNames: []string{podName},
						},
						rbacv1.PolicyRule{
							Verbs:         []string{"get", "delete"},
							APIGroups:     []string{""},
							Resources:     []string{"pods"},
							ResourceNames: []string{podName},
						},
					},
				),
				"role rule did not match expectation",
			)

			By("Expect role is owned by console")
			Expect(role.ObjectMeta.OwnerReferences).To(HaveLen(1))
			Expect(role.ObjectMeta.OwnerReferences[0].Name).To(Equal(csl.ObjectMeta.Name))

			By("Expect directory role binding was created for user and AdditionalAttachSubjects")
			drb := &rbacv1alpha1.DirectoryRoleBinding{}
			identifier, _ = client.ObjectKeyFromObject(csl)
			err = mgr.GetClient().Get(context.TODO(), identifier, drb)

			Expect(err).NotTo(HaveOccurred(), "failed to find associated DirectoryRoleBinding")
			Expect(drb.Spec.RoleRef).To(
				Equal(
					rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: consoleName},
				),
			)
			Expect(drb.Spec.Subjects).To(
				ConsistOf([]rbacv1.Subject{
					rbacv1.Subject{Kind: "User", Name: csl.Spec.User},
					rbacv1.Subject{Kind: "User", Name: "add-user@example.com"},
					rbacv1.Subject{Kind: "GoogleGroup", Name: "group@example.com"},
				}),
			)

			By("Expect rolebinding is owned by console")
			Expect(drb.ObjectMeta.OwnerReferences).To(HaveLen(1))
			Expect(drb.ObjectMeta.OwnerReferences[0].Name).To(Equal(csl.ObjectMeta.Name))
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

		It("Updates the status with completion time", func() {
			updatedCsl := &workloadsv1alpha1.Console{}
			identifier, _ := client.ObjectKeyFromObject(csl)
			err := mgr.GetClient().Get(context.TODO(), identifier, updatedCsl)
			Expect(err).NotTo(HaveOccurred(), "failed to retrieve updated console")

			Expect(updatedCsl.Status).NotTo(BeNil(), "the console status should be defined")
			Expect(updatedCsl.Status.CompletionTime).To(BeNil(), "the console completion time shouldn't be set")

			By("Expect job was created")
			job := &batchv1.Job{}
			identifier, _ = client.ObjectKeyFromObject(csl)
			identifier.Name += "-console"
			err = mgr.GetClient().Get(context.TODO(), identifier, job)
			Expect(err).NotTo(HaveOccurred(), "failed to find associated Job for Console")

			By("Updating job status")
			// integration tests don't run controller manager. It's required to
			// fake the Job status as we wait for this job condition in our
			// controller
			now := metav1.Now()
			job.Status = batchv1.JobStatus{
				CompletionTime: &now,
				Conditions: []batchv1.JobCondition{
					{
						Type: batchv1.JobComplete,
					},
				},
			}

			err = mgr.GetClient().Status().Update(context.TODO(), job)
			Expect(err).NotTo(HaveOccurred(), "failed to update Job")

			waitForSuccessfulReconcile(1)

			By("Expect console status updated")
			identifier, _ = client.ObjectKeyFromObject(csl)
			err = mgr.GetClient().Get(context.TODO(), identifier, updatedCsl)
			Expect(err).NotTo(HaveOccurred(), "failed to retrieve updated console")

			Expect(updatedCsl.Status).NotTo(BeNil(), "the console status should be defined")
			Expect(updatedCsl.Stopped()).To(BeTrue())
			Expect(
				updatedCsl.Status.CompletionTime.Time.Before(time.Now())).To(BeTrue(),
				"the console completion time should be before now()",
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

		Describe("With an authorised console", func() {
			BeforeEach(func() {
				consoleTemplate.Spec.DefaultAuthorisationRule = &workloadsv1alpha1.ConsoleAuthorisers{
					AuthorisationsRequired: 1,
					Subjects: []rbacv1.Subject{
						{Kind: "User", Name: "authorising-user-1@example.com"},
					},
				}

				consoleTemplate.Spec.AuthorisationRules = []workloadsv1alpha1.ConsoleAuthorisationRule{
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
								{Kind: "User", Name: "authorising-user-2@example.com"},
							},
						},
					},
				}

				csl.Spec.Command = []string{"sleep", "666"}
			})

			It("Creates an authorisation object", func() {
				By("Expect consoleauthorisation was created")
				auth := &workloadsv1alpha1.ConsoleAuthorisation{}
				identifier, _ := client.ObjectKeyFromObject(csl)
				err := mgr.GetClient().Get(context.TODO(), identifier, auth)

				Expect(err).NotTo(HaveOccurred(), "failed to find consoleauthorisation")
				Expect(auth.Spec.Authorisations).To(BeEmpty(),
					"authorisations should not yet be populated",
				)

				By("Expect authorisation is owned by console")
				Expect(auth.ObjectMeta.OwnerReferences).To(HaveLen(1))
				Expect(auth.ObjectMeta.OwnerReferences[0].Name).To(Equal(csl.ObjectMeta.Name))

				By("Expect role was created")
				role := &rbacv1.Role{}
				identifier, _ = client.ObjectKeyFromObject(csl)
				identifier.Name = fmt.Sprintf("%s-authorisation", identifier.Name)
				err = mgr.GetClient().Get(context.TODO(), identifier, role)

				Expect(err).NotTo(HaveOccurred(), "failed to find role")
				Expect(role.Rules).To(
					Equal(
						[]rbacv1.PolicyRule{
							{
								Verbs:         []string{"get", "patch", "update"},
								APIGroups:     []string{"workloads.crd.gocardless.com"},
								Resources:     []string{"consoleauthorisations"},
								ResourceNames: []string{csl.Name},
							},
						},
					),
					"role rule did not match expectation",
				)

				By("Expect role is owned by console")
				Expect(role.ObjectMeta.OwnerReferences).To(HaveLen(1))
				Expect(role.ObjectMeta.OwnerReferences[0].Name).To(Equal(csl.ObjectMeta.Name))

				By("Expect directory role binding was created for authorising user")
				drb := &rbacv1alpha1.DirectoryRoleBinding{}
				err = mgr.GetClient().Get(context.TODO(), identifier, drb)

				Expect(err).NotTo(HaveOccurred(), "failed to find associated DirectoryRoleBinding")
				Expect(drb.Spec.RoleRef).To(
					Equal(
						rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: identifier.Name},
					),
				)
				Expect(drb.Spec.Subjects).To(
					ConsistOf([]rbacv1.Subject{
						rbacv1.Subject{Kind: "User", Name: "authorising-user-2@example.com"},
					}),
				)

				By("Expect directory rolebinding is owned by console")
				Expect(drb.ObjectMeta.OwnerReferences).To(HaveLen(1))
				Expect(drb.ObjectMeta.OwnerReferences[0].Name).To(Equal(csl.ObjectMeta.Name))
			})

			Context("When the console requires authorisation", func() {
				BeforeEach(func() {
					ttl := int32(1)
					consoleTemplate.Spec.DefaultTTLSecondsBeforeRunning = &ttl
				})

				It("Deletes the console if not authorised within TTLSecondsBeforeRunning", func() {
					cslToDelete := csl.DeepCopy()
					By("Expect console to be deleted")
					Eventually(func() metav1.StatusReason {
						identifier, _ := client.ObjectKeyFromObject(cslToDelete)
						err := mgr.GetClient().Get(context.TODO(), identifier, &workloadsv1alpha1.Console{})
						return apierrors.ReasonForError(err)
					}, 10*time.Second).Should(Equal(metav1.StatusReasonNotFound), "expected not to find console, but did")
				})
			})
		})
	})

	Describe("Enforcing job name", func() {
		BeforeEach(func() {
			consoleName = "very-very-very-very-long-long-long-long-name-very-very-very-very-long-long-long-long-name"
			csl.ObjectMeta.Name = consoleName
		})

		JustBeforeEach(func() {
			mustCreateResources()
			waitForSuccessfulReconcile(ReconcilesAfterCreate)
		})

		It("Truncates long job names and adds a 'console' suffix", func() {
			expectJobName := "very-very-very-very-long-long-long-long-name-very-very--console"

			job := &batchv1.Job{}
			identifier, _ := client.ObjectKeyFromObject(csl)
			identifier.Name = expectJobName
			err := mgr.GetClient().Get(context.TODO(), identifier, job)

			Expect(err).NotTo(HaveOccurred(), "failed to retrieve job")
			Expect(job.ObjectMeta.Labels["console-name"]).To(Equal(consoleName[0:63]))
		})
	})

	Describe("Validating console templates", func() {
		var (
			createErr error
		)

		JustBeforeEach(func() {
			By("Creating console template")
			createErr = mgr.GetClient().Create(context.TODO(), consoleTemplate)
		})

		Context("when authorisation rules are defined", func() {
			BeforeEach(func() {
				consoleTemplate.Spec.AuthorisationRules = []workloadsv1alpha1.ConsoleAuthorisationRule{
					{
						Name:                 "test",
						MatchCommandElements: []string{"bash"},
						ConsoleAuthorisers: workloadsv1alpha1.ConsoleAuthorisers{
							Subjects: []rbacv1.Subject{},
						},
					},
				}
			})

			Context("and a default rule is not set", func() {
				BeforeEach(func() {
					consoleTemplate.Spec.AuthorisationRules = []workloadsv1alpha1.ConsoleAuthorisationRule{
						{
							Name:                 "test",
							MatchCommandElements: []string{"bash"},
							ConsoleAuthorisers: workloadsv1alpha1.ConsoleAuthorisers{
								Subjects: []rbacv1.Subject{},
							},
						},
					}
				})

				It("rejects the template", func() {
					Expect(createErr).To(MatchError(ContainSubstring(".spec.defaultAuthorisationRule must be set")))
				})
			})

			Context("and a default rule is set", func() {
				BeforeEach(func() {
					consoleTemplate.Spec.DefaultAuthorisationRule = &workloadsv1alpha1.ConsoleAuthorisers{
						Subjects: []rbacv1.Subject{},
					}
				})

				It("accepts the template", func() {
					Expect(createErr).NotTo(HaveOccurred())
				})
			})

		})

		Context("when invalid auth rules are set", func() {
			BeforeEach(func() {
				consoleTemplate.Spec.AuthorisationRules = []workloadsv1alpha1.ConsoleAuthorisationRule{
					{
						Name:                 "test",
						MatchCommandElements: []string{"bash", "**", "abc"},
						ConsoleAuthorisers: workloadsv1alpha1.ConsoleAuthorisers{
							Subjects: []rbacv1.Subject{},
						},
					},
				}
			})

			It("rejects the template", func() {
				Expect(createErr).To(MatchError(ContainSubstring("a double wildcard is only valid at the end of the pattern")))
			})
		})
	})
})
