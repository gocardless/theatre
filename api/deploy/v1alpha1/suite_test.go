package v1alpha1

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestRelease(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "api/deploy/v1alpha1")
}
