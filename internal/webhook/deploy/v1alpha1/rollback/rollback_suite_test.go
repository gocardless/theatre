package rollback

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRollback(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "webhook/deploy/v1alpha1/rollback")
}
