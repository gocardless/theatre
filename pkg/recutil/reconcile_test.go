package recutil

import (
	stderrors "errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("Reconcile", func() {
	conflict := apierrors.NewConflict(
		schema.GroupResource{Group: "deploy.crd.gocardless.com", Resource: "releases"},
		"release", stderrors.New("the object has been modified"),
	)
	other := stderrors.New("analysis failed")

	DescribeTable("isConflictOnly",
		func(err error, expected bool) {
			Expect(isConflictOnly(err)).To(Equal(expected))
		},
		Entry("Should return false for nil", nil, false),
		Entry("Should return true for a conflict", conflict, true),
		Entry("Should return true for a conflict wrapped with pkg/errors", errors.Wrap(conflict, "CreateOrUpdate failed"), true),
		Entry("Should return true for a conflict wrapped with fmt.Errorf", fmt.Errorf("update: %w", conflict), true),
		Entry("Should return true for a conflict joined with nil", stderrors.Join(conflict, nil), true),
		Entry("Should return false for a conflict joined with another error", stderrors.Join(conflict, other), false),
		Entry("Should return false for a wrapped join of a conflict and another error", errors.Wrap(stderrors.Join(conflict, other), "reconcile"), false),
		Entry("Should return false for a not found error", apierrors.NewNotFound(schema.GroupResource{Resource: "releases"}, "release"), false),
		Entry("Should return false for a non-API error", other, false),
	)
})
