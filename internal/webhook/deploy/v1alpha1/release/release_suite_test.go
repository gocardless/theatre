package release_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestRelease(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "webhook/deploy/v1alpha1/release")
}
