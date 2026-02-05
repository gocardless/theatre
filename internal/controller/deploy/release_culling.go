package deploy

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	coordinationv1 "k8s.io/api/coordination/v1"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/deploy"
)

// Parses namespace annotations to determine culling configuration
// Returns the release limit, or defaults if the annotation is invalid
func (r *ReleaseReconciler) cullConfig(ctx context.Context, logger logr.Logger, namespace string) (limit int, err error) {
	limit = DefaultReleaseLimit

	var namespaceObj corev1.Namespace
	if err := r.Client.Get(ctx, client.ObjectKey{Name: namespace}, &namespaceObj); err != nil {
		return 0, err
	}

	if mpt, ok := namespaceObj.Annotations[deployv1alpha1.AnnotationKeyReleaseLimit]; ok {
		newLimit, err := strconv.Atoi(mpt)
		if err != nil {
			logger.Error(err, fmt.Sprintf("invalid release limit annotation value, defaulting to %d", DefaultReleaseLimit),
				"annotation", deployv1alpha1.AnnotationKeyReleaseLimit, "value", mpt)
		} else {
			limit = newLimit
		}
	}

	return limit, nil
}

// cullReleases ensures that the number of inactive releases does not exceed
// the configured maximum. It will delete based on effective time (deployment
// end time if set, otherwise creation time).
func (r *ReleaseReconciler) cullReleases(ctx context.Context, logger logr.Logger, namespace string, target string) error {
	// TODO: use new logger without the release key
	limit, err := r.cullConfig(ctx, logger, namespace)
	if err != nil {
		return err
	}

	releaseList := &deployv1alpha1.ReleaseList{}
	matchFields := client.MatchingFields(map[string]string{TargetName: target})
	if err := r.List(ctx, releaseList, client.InNamespace(namespace), matchFields); err != nil {
		return err
	}

	if len(releaseList.Items) <= limit {
		logger.Info("number of releases is within limit, skipping", "current", len(releaseList.Items), "limit", limit)
		return nil
	}

	cullingCandidates := []deployv1alpha1.Release{}
	for _, release := range releaseList.Items {
		// We want to cull releases that are initialised but not active
		if release.IsStatusInitialised() && !release.IsConditionActiveTrue() {
			cullingCandidates = append(cullingCandidates, release)
		}
	}

	slices.SortFunc(cullingCandidates, func(a, b deployv1alpha1.Release) int {
		// Oldest first (oldest at index 0, newest at the end)
		return a.GetEffectiveTime().Compare(b.GetEffectiveTime())
	})

	// trim releases to the configured maximum
	excess := len(releaseList.Items) - limit
	if excess > len(cullingCandidates) {
		logger.Info("not enough culling candidates to safely cull, skipping", "current", len(releaseList.Items), "limit", limit, "candidates", len(cullingCandidates))
		return nil
	}

	acquired, err := r.acquireCullingLease(ctx, logger, namespace, target)
	if err != nil {
		return err
	}

	if !acquired {
		logger.Info("culling lease not acquired, skipping culling", "target", target)
		return nil
	}

	excessReleases := cullingCandidates[:excess]
	for _, releaseToDelete := range excessReleases {
		logger.Info("deleting release", "name", releaseToDelete.Name)
		err := r.Delete(ctx, &releaseToDelete)
		if err != nil {
			logger.Error(err, "failed to delete release", "name", releaseToDelete.Name)
			return err
		}
	}

	logger.Info("deleted excess releases", "count", len(excessReleases))
	return nil
}

// acquireCullingLease attempts to acquire a Lease for the given namespace/target.
// Returns true if the lease was acquired (caller should proceed with culling),
// false if another reconcile holds the lease (caller should skip culling).
func (r *ReleaseReconciler) acquireCullingLease(ctx context.Context, logger logr.Logger, namespace, target string) (bool, error) {
	leaseName := cullingLeaseName(target)
	now := metav1.NowMicro()
	leaseDuration := int32(5) // seconds
	holderID := fmt.Sprintf("%d", time.Now().UnixNano())

	var lease coordinationv1.Lease
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: leaseName}, &lease)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return false, err
		}
		// Lease doesn't exist, create it
		lease = coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      leaseName,
				Namespace: namespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &holderID,
				AcquireTime:          &now,
				RenewTime:            &now,
				LeaseDurationSeconds: &leaseDuration,
			},
		}
		if err := r.Create(ctx, &lease); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Another reconcile just created it, skip
				return false, nil
			}
			return false, err
		}
		logger.Info("acquired culling lease", "lease", leaseName)
		return true, nil
	}

	// Lease exists, check if it's expired
	if lease.Spec.RenewTime != nil && lease.Spec.LeaseDurationSeconds != nil {
		expiry := lease.Spec.RenewTime.Time.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
		if time.Now().Before(expiry) {
			// Lease is still valid, skip culling
			return false, nil
		}
	}

	// Lease is expired, try to take it over
	lease.Spec.HolderIdentity = &holderID
	lease.Spec.AcquireTime = &now
	lease.Spec.RenewTime = &now
	lease.Spec.LeaseDurationSeconds = &leaseDuration
	if err := r.Update(ctx, &lease); err != nil {
		if apierrors.IsConflict(err) {
			// Another reconcile updated it first, skip
			return false, nil
		}
		return false, err
	}

	logger.Info("acquired expired culling lease", "lease", leaseName)
	return true, nil
}

func cullingLeaseName(target string) string {
	hash := deploy.HashString([]byte(target))
	return fmt.Sprintf("theatre-release-cull-%s", hash)
}
