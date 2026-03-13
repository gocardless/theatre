package runner

import (
	"context"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	workloadsv1alpha1 "github.com/gocardless/theatre/v5/api/workloads/v1alpha1"
)

func goneError() error {
	return apierrors.NewGone("Gone")
}

func expiredError() error {
	return apierrors.NewResourceExpired("Expired")
}

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

var _ = Describe("withGoneRetry", func() {
	When("function succeeds on first attempt", func() {
		It("Returns the result without retrying", func() {
			calls := 0
			result, err := withGoneRetry(3, func() (string, error) {
				calls++
				return "ok", nil
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("ok"))
			Expect(calls).To(Equal(1))
		})
	})

	When("function returns a non-Gone error", func() {
		It("Returns the error without retrying", func() {
			calls := 0
			_, err := withGoneRetry(3, func() (any, error) {
				calls++
				return nil, fmt.Errorf("some other error")
			})
			Expect(err).To(MatchError("some other error"))
			Expect(calls).To(Equal(1))
		})
	})

	When("function returns Gone then succeeds", func() {
		It("Retries and returns the successful result", func() {
			calls := 0
			result, err := withGoneRetry(3, func() (string, error) {
				calls++
				if calls < 2 {
					return "", goneError()
				}
				return "recovered", nil
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("recovered"))
			Expect(calls).To(Equal(2))
		})
	})

	When("function returns Gone, Expired, then succeeds", func() {
		It("Retries and returns the successful result", func() {
			calls := 0
			result, err := withGoneRetry(3, func() (string, error) {
				calls++
				if calls == 1 {
					return "", goneError()
				}
				if calls == 2 {
					return "", expiredError()
				}
				return "recovered", nil
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("recovered"))
			Expect(calls).To(Equal(3))
		})
	})

	When("function always returns Gone", func() {
		It("Gives up after max retries and returns Gone error", func() {
			calls := 0
			_, err := withGoneRetry(3, func() (any, error) {
				calls++
				return nil, goneError()
			})
			Expect(apierrors.IsGone(err)).To(BeTrue())
			Expect(calls).To(Equal(3))
		})
	})

	When("function always returns Expired", func() {
		It("Gives up after max retries and returns Expired error", func() {
			calls := 0
			_, err := withGoneRetry(3, func() (any, error) {
				calls++
				return nil, expiredError()
			})
			Expect(apierrors.IsResourceExpired(err)).To(BeTrue())
			Expect(calls).To(Equal(3))
		})
	})
})

var _ = Describe("watchUntil", func() {
	var (
		ctx           context.Context
		cancel        context.CancelFunc
		ctxDoneCalled bool
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		ctxDoneCalled = false
	})

	AfterEach(func() {
		cancel()
	})

	newRetryWatcher := func(fw *watch.FakeWatcher) *watchtools.RetryWatcher {
		rw, err := watchtools.NewRetryWatcherWithContext(ctx, "100", &cache.ListWatch{
			WatchFuncWithContext: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				return fw, nil
			},
		})
		Expect(err).NotTo(HaveOccurred())
		return rw
	}

	ctxDoneErr := func() error {
		ctxDoneCalled = true
		return fmt.Errorf("watch interrupted: %w", ctx.Err())
	}

	sendPodEvent := func(fw *watch.FakeWatcher) {
		fw.Modify(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "test", ResourceVersion: "101"},
		})
	}

	When("processEvent signals done", func() {
		It("Returns the result", func() {
			fw := watch.NewFake()
			rw := newRetryWatcher(fw)
			defer rw.Stop()

			go func() { sendPodEvent(fw) }()

			result, err := watchUntil(ctx, rw, func(event watch.Event) (string, bool, error) {
				return "done", true, nil
			}, ctxDoneErr)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("done"))
			Expect(ctxDoneCalled).To(BeFalse())
		})
	})

	When("processEvent returns an error", func() {
		It("Returns the error and the result", func() {
			fw := watch.NewFake()
			rw := newRetryWatcher(fw)
			defer rw.Stop()

			go func() { sendPodEvent(fw) }()

			result, err := watchUntil(ctx, rw, func(event watch.Event) (string, bool, error) {
				return "partial", false, fmt.Errorf("processing failed")
			}, ctxDoneErr)
			Expect(err).To(MatchError("processing failed"))
			Expect(result).To(Equal("partial"))
			Expect(ctxDoneCalled).To(BeFalse())
		})
	})

	When("a watch Error event is received", func() {
		It("Returns the error as an API status error", func() {
			fw := watch.NewFake()
			rw := newRetryWatcher(fw)
			defer rw.Stop()

			go func() {
				fw.Error(&metav1.Status{
					Code:   http.StatusGone,
					Reason: metav1.StatusReasonGone,
				})
			}()

			_, err := watchUntil(ctx, rw, func(event watch.Event) (any, bool, error) {
				Fail("processEvent should not be called for error events")
				return nil, false, nil
			}, ctxDoneErr)
			Expect(apierrors.IsGone(err)).To(BeTrue())
			Expect(ctxDoneCalled).To(BeFalse())
		})
	})

	When("context is cancelled", func() {
		It("Calls ctxDoneErr and returns its error", func() {
			fw := watch.NewFake()
			rw := newRetryWatcher(fw)
			defer rw.Stop()

			cancel()

			_, err := watchUntil(ctx, rw, func(event watch.Event) (any, bool, error) {
				Fail("processEvent should not be called")
				return nil, false, nil
			}, ctxDoneErr)
			Expect(ctxDoneCalled).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("watch interrupted"))
		})
	})
})
