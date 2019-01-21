package apis

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("AddToSchemes", func() {
	// This test exists to ensure that all the API groups that have been defined under
	// pkg/apis/<group>/<version> have exposed an AddToScheme handler, which has then been
	// added to the aggregate AddToSchemes in the apis package. Any API group that does not
	// do this cannot then be used by any clients that register our CRDs, which would be
	// unexpected and broken.
	//
	// If this test is failing for your new API group, just add it to the list of groups on
	// apis.AddToSchemes- that should make this go green.
	It("Contains all defined API groups", func() {
		scheme := runtime.NewScheme()
		Expect(AddToScheme(scheme)).To(Succeed())

		groupVersions := make([]schema.GroupVersion, 0)
		filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
			// We're hoping for <group>/<version>/<file.go>
			pieces := strings.Split(path, "/")
			if len(pieces) > 3 {
				return filepath.SkipDir // we've gone too deep
			}

			if len(pieces) == 3 {
				if ast, err := parser.ParseFile(token.NewFileSet(), path, nil, 0); err == nil {
					if ast.Scope.Lookup("AddToScheme") != nil {
						groupVersions = append(groupVersions, schema.GroupVersion{
							Group:   fmt.Sprintf("%s.crd.gocardless.com", pieces[0]),
							Version: pieces[1],
						})
					}
				}
			}

			return nil
		})

		for _, gv := range groupVersions {
			Expect(scheme.IsVersionRegistered(gv)).To(
				BeTrue(), "api group is not registered in the scheme: %v", gv,
			)
		}
	})
})
