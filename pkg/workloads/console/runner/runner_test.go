package runner

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	workloadsv1alpha1 "github.com/gocardless/theatre/v5/api/workloads/v1alpha1"
)

var _ = Describe("checkPodState", func() {
	var (
		pod  *corev1.Pod
		done bool
		err  error
	)

	JustBeforeEach(func() {
		done, err = checkPodState(pod)
	})

	AssertNotDone := func() {
		It("Returns not done", func() {
			Expect(done).To(BeFalse())
			Expect(err).NotTo(HaveOccurred())
		})
	}

	AssertDone := func() {
		It("Returns done without error", func() {
			Expect(done).To(BeTrue())
			Expect(err).NotTo(HaveOccurred())
		})
	}

	When("pod is Running", func() {
		BeforeEach(func() {
			pod = &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}
		})

		AssertNotDone()
	})

	When("pod has Succeeded", func() {
		BeforeEach(func() {
			pod = &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
		})

		AssertDone()
	})

	When("pod has Failed", func() {
		BeforeEach(func() {
			pod = &corev1.Pod{Status: corev1.PodStatus{
				Phase:   corev1.PodFailed,
				Message: "OOMKilled",
			}}
		})

		It("Returns done with error containing phase and message", func() {
			Expect(done).To(BeTrue())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Failed"))
			Expect(err.Error()).To(ContainSubstring("OOMKilled"))
		})
	})
})

var _ = Describe("checkConsoleState", func() {
	var (
		csl                  *workloadsv1alpha1.Console
		waitForAuthorisation bool
		done                 bool
		err                  error
	)

	BeforeEach(func() {
		waitForAuthorisation = true
		csl = &workloadsv1alpha1.Console{}
	})

	JustBeforeEach(func() {
		done, err = checkConsoleState(csl, waitForAuthorisation)
	})

	AssertNotDone := func() {
		It("Returns not done", func() {
			Expect(done).To(BeFalse())
			Expect(err).NotTo(HaveOccurred())
		})
	}

	AssertDone := func() {
		It("Returns done without error", func() {
			Expect(done).To(BeTrue())
			Expect(err).NotTo(HaveOccurred())
		})
	}

	When("console is Running", func() {
		BeforeEach(func() {
			csl.Status.Phase = workloadsv1alpha1.ConsoleRunning
		})

		AssertDone()
	})

	When("console is Stopped", func() {
		BeforeEach(func() {
			csl.Status.Phase = workloadsv1alpha1.ConsoleStopped
		})

		AssertDone()
	})

	When("console is Pending", func() {
		BeforeEach(func() {
			csl.Status.Phase = workloadsv1alpha1.ConsolePending
		})

		AssertNotDone()
	})

	When("console has empty phase", func() {
		AssertNotDone()
	})

	Describe("Pending Authorisation", func() {
		BeforeEach(func() {
			csl.Status.Phase = workloadsv1alpha1.ConsolePendingAuthorisation
		})

		When("waitForAuthorisation is true", func() {
			AssertNotDone()
		})

		When("waitForAuthorisation is false", func() {
			BeforeEach(func() {
				waitForAuthorisation = false
			})

			It("Returns done with errConsolePendingAuthorisation", func() {
				Expect(done).To(BeTrue())
				Expect(err).To(MatchError(errConsolePendingAuthorisation))
			})
		})
	})
})

var _ = Describe("rbHasSubject", func() {
	var rb *rbacv1.RoleBinding

	BeforeEach(func() {
		rb = &rbacv1.RoleBinding{
			Subjects: []rbacv1.Subject{
				{Kind: rbacv1.UserKind, Name: "alice"},
				{Kind: rbacv1.UserKind, Name: "bob"},
			},
		}
	})

	When("subject is present", func() {
		It("Returns true", func() {
			Expect(rbHasSubject(rb, "alice")).To(BeTrue())
		})
	})

	When("subject is not present", func() {
		It("Returns false", func() {
			Expect(rbHasSubject(rb, "charlie")).To(BeFalse())
		})
	})

	When("subjects list is empty", func() {
		BeforeEach(func() {
			rb.Subjects = nil
		})

		It("Returns false", func() {
			Expect(rbHasSubject(rb, "alice")).To(BeFalse())
		})
	})
})
