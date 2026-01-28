package github

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/cicd"
	"github.com/google/go-github/v34/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gock "gopkg.in/h2non/gock.v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("GitHub Deployer", func() {
	var (
		deployer *Deployer
		ctx      context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()

		httpClient := &http.Client{Transport: http.DefaultTransport}
		gock.InterceptClient(httpClient)
		gock.DisableNetworking()

		ghClient := github.NewClient(httpClient)
		deployer = NewDeployer(ghClient, logr.Discard())
	})

	AfterEach(func() {
		gock.Off()
	})

	Describe("Name", func() {
		It("returns 'github'", func() {
			Expect(deployer.Name()).To(Equal("github"))
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
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rollback",
						Namespace: "default",
					},
					Spec: deployv1alpha1.RollbackSpec{
						ToReleaseRef: deployv1alpha1.ReleaseReference{Name: "my-service-v1"},
						Reason:       "High error rate",
						InitiatedBy: deployv1alpha1.RollbackInitiator{
							User: "alice@example.com",
						},
					},
					Status: deployv1alpha1.RollbackStatus{
						FromReleaseRef: deployv1alpha1.ReleaseReference{Name: "my-service-v2"},
					},
				},
				ToRelease: &deployv1alpha1.Release{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-service-v1",
						Namespace: "default",
					},
					ReleaseConfig: deployv1alpha1.ReleaseConfig{
						TargetName: "my-service",
						Revisions: []deployv1alpha1.Revision{
							{
								Name:   "commit",
								Type:   "github",
								Source: "gocardless/my-service",
								ID:     "abc123def",
							},
						},
					},
				},
			}
		})

		Context("with a valid request", func() {
			var (
				createDeploymentMatcher func(*http.Request, *gock.Request) (bool, error)
				createDeploymentResp    map[string]interface{}
			)

			BeforeEach(func() {
				createDeploymentMatcher = nil
				createDeploymentResp = map[string]interface{}{
					"id":  12345,
					"url": "https://api.github.com/repos/gocardless/my-service/deployments/12345",
				}
			})

			JustBeforeEach(func() {
				mock := gock.New("https://api.github.com").
					Post("/repos/gocardless/my-service/deployments")

				if createDeploymentMatcher != nil {
					mock = mock.MatchType("json").AddMatcher(createDeploymentMatcher)
				}

				mock.Reply(201).JSON(createDeploymentResp)
			})

			JustBeforeEach(func() {
				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("succeeds", func() {
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns the deployment ID", func() {
				Expect(result.ID).To(Equal("https://github.com/gocardless/my-service/deployments/12345"))
			})

			It("returns the deployment URL", func() {
				Expect(result.URL).To(Equal("https://github.com/gocardless/my-service/deployments/12345"))
			})

			It("returns pending status", func() {
				Expect(result.Status).To(Equal(cicd.DeploymentStatusPending))
			})

			Context("with deployment options", func() {
				var (
					createDeploymentBody map[string]interface{}
				)

				BeforeEach(func() {
					req.Options = map[string]string{
						"environment": "staging",
						"key_1":       "value_1",
						"key_2":       "value_2",
					}

					createDeploymentMatcher = func(req *http.Request, _ *gock.Request) (bool, error) {
						b, err := io.ReadAll(req.Body)
						if err != nil {
							return false, err
						}
						req.Body = io.NopCloser(bytes.NewBuffer(b))

						var body map[string]interface{}
						if err := json.Unmarshal(b, &body); err != nil {
							return false, err
						}
						createDeploymentBody = body

						return true, nil
					}
				})

				It("succeeds", func() {
					Expect(err).NotTo(HaveOccurred())
					Expect(result.ID).To(Equal("https://github.com/gocardless/my-service/deployments/12345"))
				})

				It("sends the environment in the deployment request", func() {
					Expect(createDeploymentBody).NotTo(BeNil())
					Expect(createDeploymentBody["environment"]).To(Equal("staging"))
				})

				It("includes additional options in the payload", func() {
					Expect(createDeploymentBody).NotTo(BeNil())
					payload, ok := createDeploymentBody["payload"].(map[string]interface{})
					Expect(ok).To(BeTrue())
					Expect(payload["key_1"]).To(Equal("value_1"))
					Expect(payload["key_2"]).To(Equal("value_2"))
				})
			})
		})

		Context("with no github revision", func() {
			BeforeEach(func() {
				req.ToRelease.ReleaseConfig.Revisions = []deployv1alpha1.Revision{
					{
						Name:   "image",
						Type:   "docker",
						Source: "gcr.io/my-project/my-service",
						ID:     "sha256:abc123",
					},
				}
			})

			JustBeforeEach(func() {
				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no revision with type 'github' found"))
			})

			It("is not retryable", func() {
				deployerErr, ok := err.(*cicd.DeployerError)
				Expect(ok).To(BeTrue())
				Expect(deployerErr.Retryable).To(BeFalse())
			})
		})

		Context("with invalid github repository format", func() {
			BeforeEach(func() {
				req.ToRelease.ReleaseConfig.Revisions[0].Source = "invalid-source"
			})

			JustBeforeEach(func() {
				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid github repository format"))
			})
		})

		Context("with empty github revision ID", func() {
			BeforeEach(func() {
				req.ToRelease.ReleaseConfig.Revisions[0].ID = ""
			})

			JustBeforeEach(func() {
				result, err = deployer.TriggerDeployment(ctx, req)
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("github revision has no ID"))
			})
		})

		Context("when GitHub API returns 500", func() {
			BeforeEach(func() {
				gock.New("https://api.github.com").
					Post("/repos/gocardless/my-service/deployments").
					Reply(500).
					JSON(map[string]string{"message": "Internal Server Error"})
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

		Context("when GitHub API returns 429 (rate limited)", func() {
			BeforeEach(func() {
				gock.New("https://api.github.com").
					Post("/repos/gocardless/my-service/deployments").
					Reply(429).
					JSON(map[string]string{"message": "Rate limit exceeded"})
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
	})

	Describe("GetDeploymentStatus", func() {
		var (
			deploymentURL string
			result        *cicd.DeploymentResult
			err           error
		)

		BeforeEach(func() {
			deploymentURL = "https://github.com/gocardless/my-service/deployments/12345"
		})

		JustBeforeEach(func() {
			result, err = deployer.GetDeploymentStatus(ctx, deploymentURL)
		})

		Context("with a successful deployment", func() {
			BeforeEach(func() {
				gock.New("https://api.github.com").
					Get("/repos/gocardless/my-service/deployments/12345/statuses").
					Reply(200).
					JSON([]map[string]interface{}{
						{
							"id":          1,
							"state":       "success",
							"description": "Deployment finished successfully",
							"target_url":  "https://ci.example.com/jobs/123",
						},
					})
			})

			It("succeeds", func() {
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns succeeded status", func() {
				Expect(result.Status).To(Equal(cicd.DeploymentStatusSucceeded))
			})

			It("returns the description as message", func() {
				Expect(result.Message).To(Equal("Deployment finished successfully"))
			})

			It("returns the target URL", func() {
				Expect(result.URL).To(Equal("https://ci.example.com/jobs/123"))
			})
		})

		Context("with a failed deployment", func() {
			BeforeEach(func() {
				gock.New("https://api.github.com").
					Get("/repos/gocardless/my-service/deployments/12345/statuses").
					Reply(200).
					JSON([]map[string]interface{}{
						{
							"id":          1,
							"state":       "failure",
							"description": "Deployment failed: container crashed",
						},
					})
			})

			It("returns failed status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusFailed))
			})
		})

		Context("with an in-progress deployment", func() {
			BeforeEach(func() {
				gock.New("https://api.github.com").
					Get("/repos/gocardless/my-service/deployments/12345/statuses").
					Reply(200).
					JSON([]map[string]interface{}{
						{
							"id":          1,
							"state":       "in_progress",
							"description": "Deploying...",
						},
					})
			})

			It("returns in-progress status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusInProgress))
			})
		})

		Context("with no statuses yet", func() {
			BeforeEach(func() {
				gock.New("https://api.github.com").
					Get("/repos/gocardless/my-service/deployments/12345/statuses").
					Reply(200).
					JSON([]map[string]interface{}{})
			})

			It("returns pending status", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(cicd.DeploymentStatusPending))
				Expect(result.Message).To(Equal("No status updates yet"))
			})
		})

		Context("with invalid deployment URL", func() {
			BeforeEach(func() {
				deploymentURL = "invalid-url"
			})

			It("returns an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid deployment URL format"))
			})
		})

		Context("when GitHub API returns 500", func() {
			BeforeEach(func() {
				gock.New("https://api.github.com").
					Get("/repos/gocardless/my-service/deployments/12345/statuses").
					Reply(500).
					JSON(map[string]string{"message": "Internal Server Error"})
			})

			It("returns a retryable error", func() {
				Expect(err).To(HaveOccurred())
				deployerErr, ok := err.(*cicd.DeployerError)
				Expect(ok).To(BeTrue())
				Expect(deployerErr.Retryable).To(BeTrue())
			})
		})
	})

	Describe("parseOwnerRepo", func() {
		It("parses valid owner/repo format", func() {
			owner, repo, err := deployer.parseOwnerRepo("gocardless/theatre")
			Expect(err).NotTo(HaveOccurred())
			Expect(owner).To(Equal("gocardless"))
			Expect(repo).To(Equal("theatre"))
		})

		It("returns error for invalid format", func() {
			_, _, err := deployer.parseOwnerRepo("invalid")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid github repository format"))
		})

		It("returns error for too many parts", func() {
			_, _, err := deployer.parseOwnerRepo("a/b/c")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("parseDeploymentURL", func() {
		It("parses valid deployment URL", func() {
			owner, repo, id, err := deployer.parseDeploymentURL("https://github.com/gocardless/theatre/deployments/12345")
			Expect(err).NotTo(HaveOccurred())
			Expect(owner).To(Equal("gocardless"))
			Expect(repo).To(Equal("theatre"))
			Expect(id).To(Equal(int64(12345)))
		})

		It("returns error for non-github URL", func() {
			_, _, _, err := deployer.parseDeploymentURL("https://gitlab.com/owner/repo/deployments/123")
			Expect(err).To(HaveOccurred())
		})

		It("returns error for missing deployments path", func() {
			_, _, _, err := deployer.parseDeploymentURL("https://github.com/owner/repo/pulls/123")
			Expect(err).To(HaveOccurred())
		})

		It("returns error for non-numeric ID", func() {
			_, _, _, err := deployer.parseDeploymentURL("https://github.com/owner/repo/deployments/abc")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("findGitHubRevision", func() {
		It("finds single github revision without options", func() {
			revisions := []deployv1alpha1.Revision{
				{Type: "docker", ID: "sha256:abc"},
				{Type: "github", Source: "gocardless/app", ID: "abc123"},
				{Type: "helm", ID: "1.0.0"},
			}
			rev, err := deployer.findGitHubRevision(revisions, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(rev).NotTo(BeNil())
			Expect(rev.ID).To(Equal("abc123"))
		})

		It("returns error when no github revision exists", func() {
			revisions := []deployv1alpha1.Revision{
				{Type: "docker", ID: "sha256:abc"},
			}
			_, err := deployer.findGitHubRevision(revisions, "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no revision with type 'github'"))
		})

		It("returns error for empty revisions", func() {
			_, err := deployer.findGitHubRevision(nil, "")
			Expect(err).To(HaveOccurred())
		})

		It("returns error when multiple github revisions exist without repository option", func() {
			revisions := []deployv1alpha1.Revision{
				{Type: "github", Source: "gocardless/app1", ID: "abc123"},
				{Type: "github", Source: "gocardless/app2", ID: "def456"},
			}
			_, err := deployer.findGitHubRevision(revisions, "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("multiple github revisions found"))
			Expect(err.Error()).To(ContainSubstring("repository"))
		})

		It("finds matching revision when repository option is provided", func() {
			revisions := []deployv1alpha1.Revision{
				{Type: "github", Source: "gocardless/app1", ID: "abc123"},
				{Type: "github", Source: "gocardless/app2", ID: "def456"},
			}

			rev, err := deployer.findGitHubRevision(revisions, "gocardless/app2")
			Expect(err).NotTo(HaveOccurred())
			Expect(rev.ID).To(Equal("def456"))
		})

		It("returns error when repository option doesn't match any revision", func() {
			revisions := []deployv1alpha1.Revision{
				{Type: "github", Source: "gocardless/app1", ID: "abc123"},
			}

			_, err := deployer.findGitHubRevision(revisions, "gocardless/other")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no github revision found with source"))
		})
	})
})
