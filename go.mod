module github.com/gocardless/theatre/v3

go 1.14

require (
	github.com/alecthomas/kingpin v2.2.6+incompatible
	github.com/go-kit/kit v0.9.0
	github.com/go-logr/logr v0.4.0
	github.com/google/uuid v1.1.2
	github.com/hashicorp/go-multierror v1.0.0
	github.com/hashicorp/vault/api v1.0.4
	github.com/mitchellh/mapstructure v1.1.2
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.13.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/sykesm/zap-logfmt v0.0.3
	go.uber.org/zap v1.17.0
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d
	gomodules.xyz/jsonpatch/v3 v3.0.1
	google.golang.org/api v0.20.0
	gopkg.in/h2non/gock.v1 v1.0.15
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.21.2
	k8s.io/apimachinery v0.21.2
	k8s.io/cli-runtime v0.21.2
	k8s.io/client-go v0.21.2
	k8s.io/klog v1.0.0
	k8s.io/kubectl v0.21.2
	sigs.k8s.io/controller-runtime v0.9.2
)
