package recutil

import (
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsConflictOnly(t *testing.T) {
	conflict := apierrors.NewConflict(schema.GroupResource{Group: "deploy.crd.gocardless.com", Resource: "releases"}, "r", stderrors.New("the object has been modified"))
	other := stderrors.New("analysis failed")

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain conflict", conflict, true},
		{"pkg/errors wrapped conflict", errors.Wrap(conflict, "CreateOrUpdate failed"), true},
		{"fmt wrapped conflict", fmt.Errorf("update: %w", conflict), true},
		{"conflict joined with nil", stderrors.Join(conflict, nil), true},
		{"conflict joined with other error", stderrors.Join(conflict, other), false},
		{"wrapped join with other error", errors.Wrap(stderrors.Join(conflict, other), "ctx"), false},
		{"not found", apierrors.NewNotFound(schema.GroupResource{Resource: "releases"}, "r"), false},
		{"other error", other, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConflictOnly(tc.err); got != tc.want {
				t.Fatalf("isConflictOnly(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
