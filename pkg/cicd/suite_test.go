package cicd

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRelease(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/cicd")
}
