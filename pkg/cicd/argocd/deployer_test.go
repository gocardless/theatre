package argocd

import (
	"context"
	"net/http"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/cicd"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gock "gopkg.in/h2non/gock.v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ArgoCD Deployer", func() {
	var (
		deployer  *Deployer
		ctx       context.Context
		serverURL string
	)

	BeforeEach(func() {
		ctx = context.Background()
		serverURL = "https://argocd.example.com"

		httpClient := &http.Client{Transport: http.DefaultTransport}
		gock.InterceptClient(httpClient)
		gock.DisableNetworking()

		var err error
		deployer, err = NewDeployer(httpClient, serverURL, "test-token", "compute-lab-{{.Namespace}}-{{.Target}}", logr.Discard())
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		gock.Off()
	})

	Describe("Name", func() {
		It("returns 'argocd'", func() {
			Expect(deployer.Name()).To(Equal("argocd"))
		})
	})

	Describe("findRevision", func() {
		var revisions []deployv1alpha1.Revision

		BeforeEach(func() {
			revisions = []deployv1alpha1.Revision{
				{Name: "app-revision", ID: "abc123", Source: "repo/app", Type: "git"},
				{Name: "chart-revision", ID: "v1.2.3", Source: "repo/chart", Type: "helm_chart"},
			}
		})

		It("finds a revision by name", func() {
			rev, err := deployer.findRevision(revisions, "app-revision")
			Expect(err).NotTo(HaveOccurred())
			Expect(rev.Name).To(Equal("app-revision"))
			Expect(rev.ID).To(Equal("abc123"))
		})

		It("returns error when revision name not found", func() {
			_, err := deployer.findRevision(revisions, "nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no revision found with name"))
		})

		It("returns the single revision when no name specified", func() {
			single := []deployv1alpha1.Revision{
				{Name: "only-one", ID: "def456"},
			}
			rev, err := deployer.findRevision(single, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(rev.ID).To(Equal("def456"))
		})

		It("returns error when multiple revisions and no name specified", func() {
			_, err := deployer.findRevision(revisions, "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("multiple revisions found"))
		})

		It("returns error when no revisions exist", func() {
			_, err := deployer.findRevision([]deployv1alpha1.Revision{}, "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no revisions found"))
		})
	})

	Describe("GetDeploymentStatus", func() {
		DescribeTable("When operationPhase is not Succeeded",
			func(phase string, status cicd.DeploymentStatus) {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"status": map[string]any{
							"operationState": map[string]any{
								"phase":   phase,
								"message": "Operation " + phase,
							},
							"sync":   map[string]string{"status": "Synced"},
							"health": map[string]string{"status": "Healthy"},
						},
					})

				result, err := deployer.GetDeploymentStatus(ctx, "my-app")
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(status))
				Expect(result.Message).To(Equal("Operation " + phase))
			},
			Entry("Running", "Running", cicd.DeploymentStatusInProgress),
			Entry("Error", "Error", cicd.DeploymentStatusFailed),
			Entry("Failed", "Failed", cicd.DeploymentStatusFailed),
		)

		Describe("When operationPhase is Succeeded", func() {
			It("returns Succeeded when Synced and Healthy", func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"status": map[string]any{
							"operationState": map[string]any{
								"phase": "Succeeded",
							},
							"sync":   map[string]string{"status": "Synced"},
							"health": map[string]string{"status": "Healthy"},
						},
					})

				result, err := deployer.GetDeploymentStatus(ctx, "my-app")
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusSucceeded))
				Expect(result.Message).To(Equal("Application synced and healthy"))
			})

			It("returns Succeeded when OutOfSync and Healthy", func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					MatchHeader("Authorization", "Bearer test-token").
					Reply(200).
					JSON(map[string]any{
						"status": map[string]any{
							"sync":   map[string]string{"status": "OutOfSync"},
							"health": map[string]string{"status": "Healthy"},
						},
					})

				result, err := deployer.GetDeploymentStatus(ctx, "my-app")
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusSucceeded))
				Expect(result.Message).To(Equal("Application synced and healthy"))
			})

			It("returns Pending when status is Unknown", func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"status": map[string]any{
							"sync":   map[string]string{"status": "Unknown"},
							"health": map[string]string{"status": "Missing"},
						},
					})

				result, err := deployer.GetDeploymentStatus(ctx, "my-app")
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusPending))
			})

			It("returns Pending when Synced but health is Progressing", func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"status": map[string]any{
							"sync":   map[string]string{"status": "Synced"},
							"health": map[string]string{"status": "Progressing"},
						},
					})

				result, err := deployer.GetDeploymentStatus(ctx, "my-app")
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusPending))
			})
		})

		Context("error handling", func() {
			It("returns a retryable error on 500", func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(500).
					BodyString("internal server error")

				_, err := deployer.GetDeploymentStatus(ctx, "my-app")
				Expect(err).To(HaveOccurred())

				var deployerErr *cicd.DeployerError
				Expect(err).To(BeAssignableToTypeOf(deployerErr))
				deployerErr = err.(*cicd.DeployerError)
				Expect(deployerErr.Retryable).To(BeTrue())
			})

			It("returns a retryable error on 429", func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(429).
					BodyString("rate limited")

				_, err := deployer.GetDeploymentStatus(ctx, "my-app")
				Expect(err).To(HaveOccurred())

				deployerErr := err.(*cicd.DeployerError)
				Expect(deployerErr.Retryable).To(BeTrue())
			})

			It("returns a non-retryable error on 404", func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(404).
					BodyString("application not found")

				_, err := deployer.GetDeploymentStatus(ctx, "my-app")
				Expect(err).To(HaveOccurred())

				deployerErr := err.(*cicd.DeployerError)
				Expect(deployerErr.Retryable).To(BeFalse())
			})
		})
	})

	Describe("updateApplication", func() {
		It("sends a merge-patch with revision and REVISION parameter", func() {
			gock.New(serverURL).
				Patch("/api/v1/applications/my-app").
				MatchHeader("Authorization", "Bearer test-token").
				MatchHeader("Content-Type", "application/json").
				Reply(200).
				JSON(map[string]any{})

			err := deployer.updateApplication(ctx, "my-app", "abc123", "def456")
			Expect(err).NotTo(HaveOccurred())
			Expect(gock.IsDone()).To(BeTrue())
		})

		It("returns a retryable error on 500", func() {
			gock.New(serverURL).
				Patch("/api/v1/applications/my-app").
				Reply(500).
				BodyString("internal error")

			err := deployer.updateApplication(ctx, "my-app", "abc123", "def456")
			Expect(err).To(HaveOccurred())

			deployerErr := err.(*cicd.DeployerError)
			Expect(deployerErr.Retryable).To(BeTrue())
		})
	})

	Describe("syncApplication", func() {
		It("posts a sync request with the revision", func() {
			gock.New(serverURL).
				Post("/api/v1/applications/my-app/sync").
				MatchHeader("Authorization", "Bearer test-token").
				Reply(200).
				JSON(map[string]any{})

			err := deployer.syncApplication(ctx, "my-app", "abc123")
			Expect(err).NotTo(HaveOccurred())
			Expect(gock.IsDone()).To(BeTrue())
		})

		It("returns a non-retryable error on 400", func() {
			gock.New(serverURL).
				Post("/api/v1/applications/my-app/sync").
				Reply(400).
				BodyString("bad request")

			err := deployer.syncApplication(ctx, "my-app", "abc123")
			Expect(err).To(HaveOccurred())

			deployerErr := err.(*cicd.DeployerError)
			Expect(deployerErr.Retryable).To(BeFalse())
		})
	})

	Describe("TriggerDeployment", func() {
		var req cicd.DeploymentRequest

		BeforeEach(func() {
			req = cicd.DeploymentRequest{
				Rollback: &deployv1alpha1.Rollback{
					ObjectMeta: metav1.ObjectMeta{Name: "test-rollback", Namespace: "elozev"},
					Spec: deployv1alpha1.RollbackSpec{
						ToReleaseRef: deployv1alpha1.ReleaseReference{
							Target: "my-app",
						},
					},
				},
				ToRelease: &deployv1alpha1.Release{
					ReleaseConfig: deployv1alpha1.ReleaseConfig{
						TargetName: "my-app",
						Revisions: []deployv1alpha1.Revision{
							{Name: "app", ID: "abc123"},
						},
					},
				},
				Options: map[string]any{TargetRevisionNameKey: "abc123"},
			}
		})

		It("triggers a deployment successfully", func() {
			gock.New(serverURL).
				Patch("/api/v1/applications/compute-lab-elozev-my-app").
				Reply(200).
				JSON(map[string]any{})

			gock.New(serverURL).
				Post("/api/v1/applications/compute-lab-elozev-my-app/sync").
				Reply(200).
				JSON(map[string]any{})

			result, err := deployer.TriggerDeployment(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.ID).To(Equal("compute-lab-elozev-my-app"))
			Expect(result.Status).To(Equal(cicd.DeploymentStatusPending))
			Expect(result.URL).To(Equal("https://argocd.example.com/applications/compute-lab-elozev-my-app"))
			Expect(gock.IsDone()).To(BeTrue())
		})

		It("returns error when target_revision option is missing", func() {
			req.Options = map[string]any{}

			_, err := deployer.TriggerDeployment(ctx, req)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("target_revision is a required deploymentOption"))
		})

		It("returns error when target is empty", func() {
			req.Rollback.Spec.ToReleaseRef.Target = ""

			_, err := deployer.TriggerDeployment(ctx, req)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("rollback target is empty"))
		})
	})

	Describe("resolveAppName", func() {
		It("renders the app name from the template", func() {
			req := cicd.DeploymentRequest{
				Rollback: &deployv1alpha1.Rollback{
					ObjectMeta: metav1.ObjectMeta{Namespace: "elozev"},
					Spec: deployv1alpha1.RollbackSpec{
						ToReleaseRef: deployv1alpha1.ReleaseReference{Target: "elozev-playground"},
					},
				},
			}
			name, err := deployer.resolveAppName(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(name).To(Equal("compute-lab-elozev-elozev-playground"))
		})

		It("uses argocd_app_name from options when provided", func() {
			req := cicd.DeploymentRequest{
				Rollback: &deployv1alpha1.Rollback{
					ObjectMeta: metav1.ObjectMeta{Namespace: "elozev"},
					Spec: deployv1alpha1.RollbackSpec{
						ToReleaseRef: deployv1alpha1.ReleaseReference{Target: "elozev-playground"},
					},
				},
				Options: map[string]any{AppNameKey: "custom-app-name"},
			}
			name, err := deployer.resolveAppName(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(name).To(Equal("custom-app-name"))
		})

		It("returns error when namespace is empty", func() {
			req := cicd.DeploymentRequest{
				Rollback: &deployv1alpha1.Rollback{
					ObjectMeta: metav1.ObjectMeta{Namespace: ""},
					Spec: deployv1alpha1.RollbackSpec{
						ToReleaseRef: deployv1alpha1.ReleaseReference{Target: "my-app"},
					},
				},
			}
			_, err := deployer.resolveAppName(req)
			Expect(err).To(HaveOccurred())
		})
	})
})
