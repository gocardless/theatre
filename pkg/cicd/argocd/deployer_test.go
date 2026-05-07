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

	Describe("encodeDeploymentID and decodeDeploymentID", func() {
		It("round-trips with target revision only", func() {
			id := encodeDeploymentID("my-app", "abc123", "")
			appName, targetRev, appRev := decodeDeploymentID(id)
			Expect(appName).To(Equal("my-app"))
			Expect(targetRev).To(Equal("abc123"))
			Expect(appRev).To(BeEmpty())
		})

		It("round-trips with both revisions", func() {
			id := encodeDeploymentID("my-app", "abc123", "def456")
			appName, targetRev, appRev := decodeDeploymentID(id)
			Expect(appName).To(Equal("my-app"))
			Expect(targetRev).To(Equal("abc123"))
			Expect(appRev).To(Equal("def456"))
		})

		It("handles plain app name (no separator)", func() {
			appName, targetRev, appRev := decodeDeploymentID("my-app")
			Expect(appName).To(Equal("my-app"))
			Expect(targetRev).To(BeEmpty())
			Expect(appRev).To(BeEmpty())
		})
	})

	Describe("revisionMatches", func() {
		It("returns true when synced to target revision without app revision", func() {
			app := &applicationResponse{
				Status: applicationStatusResponse{
					Sync: applicationSyncStatus{
						Status:   SyncStatusSynced,
						Revision: "abc123",
					},
				},
			}
			Expect(revisionMatches(app, "abc123", "")).To(BeTrue())
		})

		It("returns false when not synced", func() {
			app := &applicationResponse{
				Status: applicationStatusResponse{
					Sync: applicationSyncStatus{
						Status:   SyncStatusOutOfSync,
						Revision: "abc123",
					},
				},
			}
			Expect(revisionMatches(app, "abc123", "")).To(BeFalse())
		})

		It("returns false when revision doesn't match", func() {
			app := &applicationResponse{
				Status: applicationStatusResponse{
					Sync: applicationSyncStatus{
						Status:   SyncStatusSynced,
						Revision: "different",
					},
				},
			}
			Expect(revisionMatches(app, "abc123", "")).To(BeFalse())
		})

		It("returns true when both revisions match", func() {
			app := &applicationResponse{
				Status: applicationStatusResponse{
					Sync: applicationSyncStatus{
						Status:   SyncStatusSynced,
						Revision: "abc123",
						ComparedTo: &applicationSyncComparedTo{
							Source: applicationSource{
								Plugin: applicationSourcePlugin{
									Env: []applicationPatchEnv{
										{Name: "REVISION", Value: "def456"},
									},
								},
							},
						},
					},
				},
			}
			Expect(revisionMatches(app, "abc123", "def456")).To(BeTrue())
		})

		It("returns false when app revision doesn't match", func() {
			app := &applicationResponse{
				Status: applicationStatusResponse{
					Sync: applicationSyncStatus{
						Status:   SyncStatusSynced,
						Revision: "abc123",
						ComparedTo: &applicationSyncComparedTo{
							Source: applicationSource{
								Plugin: applicationSourcePlugin{
									Env: []applicationPatchEnv{
										{Name: "REVISION", Value: "wrong"},
									},
								},
							},
						},
					},
				},
			}
			Expect(revisionMatches(app, "abc123", "def456")).To(BeFalse())
		})

		It("returns false when app revision required but comparedTo is nil", func() {
			app := &applicationResponse{
				Status: applicationStatusResponse{
					Sync: applicationSyncStatus{
						Status:   SyncStatusSynced,
						Revision: "abc123",
					},
				},
			}
			Expect(revisionMatches(app, "abc123", "def456")).To(BeFalse())
		})

		It("returns false when app revision required but no REVISION env var", func() {
			app := &applicationResponse{
				Status: applicationStatusResponse{
					Sync: applicationSyncStatus{
						Status:   SyncStatusSynced,
						Revision: "abc123",
						ComparedTo: &applicationSyncComparedTo{
							Source: applicationSource{
								Plugin: applicationSourcePlugin{
									Env: []applicationPatchEnv{
										{Name: "OTHER", Value: "def456"},
									},
								},
							},
						},
					},
				},
			}
			Expect(revisionMatches(app, "abc123", "def456")).To(BeFalse())
		})
	})

	Describe("GetDeploymentStatus", func() {
		var (
			deploymentID string
			result       *cicd.DeploymentResult
			err          error
		)

		BeforeEach(func() {
			// Deployment ID encodes appName::targetRevision
			deploymentID = "my-app::abc123"
		})

		JustBeforeEach(func() {
			result, err = deployer.GetDeploymentStatus(ctx, deploymentID)
		})

		Context("when the application is synced to the target revision", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"spec": map[string]any{
							"source": map[string]any{"targetRevision": "abc123"},
						},
						"status": map[string]any{
							"sync": map[string]any{
								"status":   "Synced",
								"revision": "abc123",
							},
						},
					})
			})

			It("returns succeeded status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusSucceeded))
			})

			It("returns the application URL", func() {
				Expect(result.URL).To(Equal("https://argocd.example.com/applications/my-app"))
			})
		})

		Context("when the application is synced to the target revision with matching app revision", func() {
			BeforeEach(func() {
				deploymentID = "my-app::abc123::def456"

				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"spec": map[string]any{
							"source": map[string]any{"targetRevision": "abc123"},
						},
						"status": map[string]any{
							"sync": map[string]any{
								"status":   "Synced",
								"revision": "abc123",
								"comparedTo": map[string]any{
									"source": map[string]any{
										"plugin": map[string]any{
											"env": []any{
												map[string]any{"name": "REVISION", "value": "def456"},
											},
										},
									},
								},
							},
						},
					})
			})

			It("returns succeeded status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusSucceeded))
			})
		})

		Context("when the application is synced but app revision doesn't match", func() {
			BeforeEach(func() {
				deploymentID = "my-app::abc123::def456"

				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"spec": map[string]any{
							"source": map[string]any{"targetRevision": "abc123"},
						},
						"status": map[string]any{
							"sync": map[string]any{
								"status":   "Synced",
								"revision": "abc123",
								"comparedTo": map[string]any{
									"source": map[string]any{
										"plugin": map[string]any{
											"env": []any{
												map[string]any{"name": "REVISION", "value": "wrong-rev"},
											},
										},
									},
								},
							},
							"operationState": map[string]any{
								"phase":   "Running",
								"message": "syncing",
							},
						},
					})
			})

			It("returns in-progress status (revision mismatch, sync still running)", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusInProgress))
			})
		})

		Context("when the sync operation is running", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"spec": map[string]any{
							"source": map[string]any{"targetRevision": "abc123"},
						},
						"status": map[string]any{
							"sync": map[string]any{
								"status":   "OutOfSync",
								"revision": "old-rev",
							},
							"operationState": map[string]any{
								"phase":   "Running",
								"message": "Syncing resources",
							},
						},
					})
			})

			It("returns in-progress status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusInProgress))
			})

			It("returns the operation message", func() {
				Expect(result.Message).To(Equal("Syncing resources"))
			})
		})

		Context("when the sync operation failed", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"spec": map[string]any{
							"source": map[string]any{"targetRevision": "abc123"},
						},
						"status": map[string]any{
							"sync": map[string]any{
								"status":   "OutOfSync",
								"revision": "old-rev",
							},
							"operationState": map[string]any{
								"phase":   "Failed",
								"message": "sync failed: resource not found",
							},
						},
					})
			})

			It("returns failed status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusFailed))
			})

			It("returns the failure message", func() {
				Expect(result.Message).To(Equal("sync failed: resource not found"))
			})
		})

		Context("when the sync operation errored", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"spec": map[string]any{
							"source": map[string]any{"targetRevision": "abc123"},
						},
						"status": map[string]any{
							"sync": map[string]any{
								"status":   "OutOfSync",
								"revision": "old-rev",
							},
							"operationState": map[string]any{
								"phase":   "Error",
								"message": "ComparisonError",
							},
						},
					})
			})

			It("returns failed status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusFailed))
			})
		})

		Context("when the last operation succeeded but spec still targets our revision (waiting for sync cycle)", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"spec": map[string]any{
							"source": map[string]any{"targetRevision": "abc123"},
						},
						"status": map[string]any{
							"sync": map[string]any{
								"status":   "Synced",
								"revision": "old-rev",
							},
							"operationState": map[string]any{
								"phase":   "Succeeded",
								"message": "previous sync done",
							},
						},
					})
			})

			It("returns pending status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusPending))
			})

			It("indicates waiting for sync", func() {
				Expect(result.Message).To(ContainSubstring("waiting for sync"))
			})
		})

		Context("when the last operation succeeded and spec targets a different revision (superseded)", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"spec": map[string]any{
							"source": map[string]any{"targetRevision": "someone-elses-rev"},
						},
						"status": map[string]any{
							"sync": map[string]any{
								"status":   "Synced",
								"revision": "someone-elses-rev",
							},
							"operationState": map[string]any{
								"phase":   "Succeeded",
								"message": "synced to different revision",
							},
						},
					})
			})

			It("returns superseded status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusSuperseded))
			})

			It("returns a descriptive message", func() {
				Expect(result.Message).To(ContainSubstring("changed by another operation"))
			})
		})

		Context("when there is no operation state and spec targets our revision", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"spec": map[string]any{
							"source": map[string]any{"targetRevision": "abc123"},
						},
						"status": map[string]any{
							"sync": map[string]any{
								"status":   "OutOfSync",
								"revision": "old-rev",
							},
						},
					})
			})

			It("returns pending status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusPending))
			})
		})

		Context("when there is no operation state and spec targets a different revision", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(200).
					JSON(map[string]any{
						"spec": map[string]any{
							"source": map[string]any{"targetRevision": "different-rev"},
						},
						"status": map[string]any{
							"sync": map[string]any{
								"status":   "OutOfSync",
								"revision": "old-rev",
							},
						},
					})
			})

			It("returns superseded status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusSuperseded))
			})
		})

		Context("when ArgoCD API returns 500", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(500).
					BodyString("internal server error")
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
			})

			It("is retryable", func() {
				deployerErr, ok := err.(*cicd.DeployerError)
				Expect(ok).To(BeTrue())
				Expect(deployerErr.Retryable).To(BeTrue())
			})
		})

		Context("when ArgoCD API returns 429 (rate limited)", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(429).
					BodyString("rate limited")
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
			})

			It("is retryable", func() {
				deployerErr, ok := err.(*cicd.DeployerError)
				Expect(ok).To(BeTrue())
				Expect(deployerErr.Retryable).To(BeTrue())
			})
		})

		Context("when ArgoCD API returns 404", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Get("/api/v1/applications/my-app").
					Reply(404).
					BodyString("application not found")
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
			})

			It("is not retryable", func() {
				deployerErr, ok := err.(*cicd.DeployerError)
				Expect(ok).To(BeTrue())
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
		It("posts a sync request", func() {
			gock.New(serverURL).
				Post("/api/v1/applications/my-app/sync").
				MatchHeader("Authorization", "Bearer test-token").
				Reply(200).
				JSON(map[string]any{})

			err := deployer.syncApplication(ctx, "my-app")
			Expect(err).NotTo(HaveOccurred())
			Expect(gock.IsDone()).To(BeTrue())
		})

		It("returns a non-retryable error on 400", func() {
			gock.New(serverURL).
				Post("/api/v1/applications/my-app/sync").
				Reply(400).
				BodyString("bad request")

			err := deployer.syncApplication(ctx, "my-app")
			Expect(err).To(HaveOccurred())

			deployerErr := err.(*cicd.DeployerError)
			Expect(deployerErr.Retryable).To(BeFalse())
		})
	})

	Describe("TriggerDeployment", func() {
		var (
			req    cicd.DeploymentRequest
			result *cicd.DeploymentResult
			err    error
		)

		BeforeEach(func() {
			req = cicd.DeploymentRequest{
				Rollback: &deployv1alpha1.Rollback{
					ObjectMeta: metav1.ObjectMeta{Name: "test-rollback", Namespace: "default"},
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

		Context("with a valid request", func() {
			JustBeforeEach(func() {
				gock.New(serverURL).
					Patch("/api/v1/applications/compute-lab-default-my-app").
					MatchHeader("Authorization", "Bearer test-token").
					Reply(200).
					JSON(map[string]any{})

				gock.New(serverURL).
					Post("/api/v1/applications/compute-lab-default-my-app/sync").
					Reply(200).
					JSON(map[string]any{})

				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("succeeds", func() {
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns a deployment ID encoding the app name and target revision", func() {
				Expect(result.ID).To(Equal("compute-lab-default-my-app::abc123"))
			})

			It("returns pending status", func() {
				Expect(result.Status).To(Equal(cicd.DeploymentStatusPending))
			})

			It("returns the ArgoCD application URL", func() {
				Expect(result.URL).To(Equal("https://argocd.example.com/applications/compute-lab-default-my-app"))
			})

			It("calls patch and sync endpoints", func() {
				Expect(gock.IsDone()).To(BeTrue())
			})
		})

		Context("with an app_revision option set", func() {
			BeforeEach(func() {
				req.Options[AppRevisionNameKey] = "app-commit-sha"
			})

			JustBeforeEach(func() {
				gock.New(serverURL).
					Patch("/api/v1/applications/compute-lab-default-my-app").
					Reply(200).
					JSON(map[string]any{})

				gock.New(serverURL).
					Post("/api/v1/applications/compute-lab-default-my-app/sync").
					Reply(200).
					JSON(map[string]any{})

				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("includes both revisions in the deployment ID", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.ID).To(Equal("compute-lab-default-my-app::abc123::app-commit-sha"))
			})
		})

		Context("with argocd_app_name option overriding the template", func() {
			BeforeEach(func() {
				req.Options[AppNameKey] = "custom-app-name"
			})

			JustBeforeEach(func() {
				gock.New(serverURL).
					Patch("/api/v1/applications/custom-app-name").
					Reply(200).
					JSON(map[string]any{})

				gock.New(serverURL).
					Post("/api/v1/applications/custom-app-name/sync").
					Reply(200).
					JSON(map[string]any{})

				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("uses the custom app name in the deployment ID", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.ID).To(Equal("custom-app-name::abc123"))
			})
		})

		Context("with missing target_revision option", func() {
			BeforeEach(func() {
				req.Options = map[string]any{}
			})

			JustBeforeEach(func() {
				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("target_revision is a required deploymentOption"))
			})

			It("is not retryable", func() {
				deployerErr, ok := err.(*cicd.DeployerError)
				Expect(ok).To(BeTrue())
				Expect(deployerErr.Retryable).To(BeFalse())
			})
		})

		Context("with an empty rollback target", func() {
			BeforeEach(func() {
				req.Rollback.Spec.ToReleaseRef.Target = ""
			})

			JustBeforeEach(func() {
				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("rollback target is empty"))
			})

			It("is not retryable", func() {
				deployerErr, ok := err.(*cicd.DeployerError)
				Expect(ok).To(BeTrue())
				Expect(deployerErr.Retryable).To(BeFalse())
			})
		})

		Context("when the patch API call returns 500", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Patch("/api/v1/applications/compute-lab-default-my-app").
					Reply(500).
					BodyString("internal server error")
			})

			JustBeforeEach(func() {
				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
			})

			It("is retryable", func() {
				deployerErr, ok := err.(*cicd.DeployerError)
				Expect(ok).To(BeTrue())
				Expect(deployerErr.Retryable).To(BeTrue())
			})
		})

		Context("when the sync API call returns 400", func() {
			BeforeEach(func() {
				gock.New(serverURL).
					Patch("/api/v1/applications/compute-lab-default-my-app").
					Reply(200).
					JSON(map[string]any{})

				gock.New(serverURL).
					Post("/api/v1/applications/compute-lab-default-my-app/sync").
					Reply(400).
					BodyString("bad request")
			})

			JustBeforeEach(func() {
				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
			})

			It("is not retryable", func() {
				deployerErr, ok := err.(*cicd.DeployerError)
				Expect(ok).To(BeTrue())
				Expect(deployerErr.Retryable).To(BeFalse())
			})
		})
	})

	Describe("PostDeploymentHooks", func() {
		var (
			req cicd.DeploymentRequest
			err error
		)

		BeforeEach(func() {
			req = cicd.DeploymentRequest{
				Rollback: &deployv1alpha1.Rollback{
					ObjectMeta: metav1.ObjectMeta{Name: "test-rollback", Namespace: "default"},
					Spec: deployv1alpha1.RollbackSpec{
						ToReleaseRef: deployv1alpha1.ReleaseReference{
							Target: "my-app",
						},
					},
				},
				Options: map[string]any{},
			}
		})

		JustBeforeEach(func() {
			err = deployer.PostDeploymentHooks(ctx, req, "compute-lab-default-my-app")
		})

		Context("when add_sync_window option is not set", func() {
			It("succeeds without making any API calls", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(gock.IsDone()).To(BeTrue())
			})
		})

		Context("when add_sync_window option is false", func() {
			BeforeEach(func() {
				req.Options[AddSyncWindowKey] = false
			})

			It("succeeds without making any API calls", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(gock.IsDone()).To(BeTrue())
			})
		})

		Context("when add_sync_window option is true", func() {
			BeforeEach(func() {
				req.Options[AddSyncWindowKey] = true
			})

			Context("with a successful project update", func() {
				BeforeEach(func() {
					gock.New(serverURL).
						Get("/api/v1/applications/compute-lab-default-my-app").
						Reply(200).
						JSON(map[string]any{
							"spec":   map[string]any{"project": "my-project"},
							"status": map[string]any{},
						})

					gock.New(serverURL).
						Get("/api/v1/projects/my-project").
						MatchHeader("Authorization", "Bearer test-token").
						Reply(200).
						JSON(map[string]any{
							"metadata": map[string]any{"name": "my-project", "resourceVersion": "42"},
							"spec":     map[string]any{"syncWindows": []any{}},
						})

					gock.New(serverURL).
						Put("/api/v1/projects/my-project").
						MatchHeader("Authorization", "Bearer test-token").
						Reply(200).
						JSON(map[string]any{})
				})

				It("succeeds", func() {
					Expect(err).NotTo(HaveOccurred())
				})

				It("calls the get application, get project, and update project endpoints", func() {
					Expect(gock.IsDone()).To(BeTrue())
				})
			})

			Context("when fetching the application returns 500", func() {
				BeforeEach(func() {
					gock.New(serverURL).
						Get("/api/v1/applications/compute-lab-default-my-app").
						Reply(500).
						BodyString("internal server error")
				})

				It("returns an error", func() {
					Expect(err).To(HaveOccurred())
				})

				It("is retryable", func() {
					deployerErr, ok := err.(*cicd.DeployerError)
					Expect(ok).To(BeTrue())
					Expect(deployerErr.Retryable).To(BeTrue())
				})
			})

			Context("when fetching the project returns 404", func() {
				BeforeEach(func() {
					gock.New(serverURL).
						Get("/api/v1/applications/compute-lab-default-my-app").
						Reply(200).
						JSON(map[string]any{
							"spec":   map[string]any{"project": "my-project"},
							"status": map[string]any{},
						})

					gock.New(serverURL).
						Get("/api/v1/projects/my-project").
						Reply(404).
						BodyString("project not found")
				})

				It("returns an error", func() {
					Expect(err).To(HaveOccurred())
				})

				It("is not retryable", func() {
					deployerErr, ok := err.(*cicd.DeployerError)
					Expect(ok).To(BeTrue())
					Expect(deployerErr.Retryable).To(BeFalse())
				})
			})

			Context("when updating the project returns 500", func() {
				BeforeEach(func() {
					gock.New(serverURL).
						Get("/api/v1/applications/compute-lab-default-my-app").
						Reply(200).
						JSON(map[string]any{
							"spec":   map[string]any{"project": "my-project"},
							"status": map[string]any{},
						})

					gock.New(serverURL).
						Get("/api/v1/projects/my-project").
						Reply(200).
						JSON(map[string]any{
							"metadata": map[string]any{"name": "my-project", "resourceVersion": "1"},
							"spec":     map[string]any{},
						})

					gock.New(serverURL).
						Put("/api/v1/projects/my-project").
						Reply(500).
						BodyString("internal server error")
				})

				It("returns an error", func() {
					Expect(err).To(HaveOccurred())
				})

				It("is retryable", func() {
					deployerErr, ok := err.(*cicd.DeployerError)
					Expect(ok).To(BeTrue())
					Expect(deployerErr.Retryable).To(BeTrue())
				})
			})
		})

		Context("when the rollback target is empty", func() {
			BeforeEach(func() {
				req.Rollback.Spec.ToReleaseRef.Target = ""
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("rollback target is empty"))
			})
		})
	})

	Describe("resolveAppName", func() {
		It("renders the app name from the template", func() {
			req := cicd.DeploymentRequest{
				Rollback: &deployv1alpha1.Rollback{
					ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
					Spec: deployv1alpha1.RollbackSpec{
						ToReleaseRef: deployv1alpha1.ReleaseReference{Target: "my-playground"},
					},
				},
			}
			name, err := deployer.resolveAppName(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(name).To(Equal("compute-lab-default-my-playground"))
		})

		It("uses argocd_app_name from options when provided", func() {
			req := cicd.DeploymentRequest{
				Rollback: &deployv1alpha1.Rollback{
					ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
					Spec: deployv1alpha1.RollbackSpec{
						ToReleaseRef: deployv1alpha1.ReleaseReference{Target: "my-playground"},
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
