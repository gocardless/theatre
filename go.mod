module github.com/gocardless/theatre/v3

go 1.14

require (
	cloud.google.com/go/pubsub v1.17.1
	github.com/alecthomas/kingpin v2.2.6+incompatible
	github.com/go-kit/kit v0.8.0
	github.com/go-logr/logr v0.1.0
	github.com/google/uuid v1.1.2
	github.com/hashicorp/go-multierror v1.0.0
	github.com/hashicorp/vault/api v1.0.4
	github.com/mitchellh/mapstructure v1.1.2
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.1.0
	github.com/sykesm/zap-logfmt v0.0.3
	go.uber.org/zap v1.12.0
	golang.org/x/oauth2 v0.0.0-20211005180243-6b3c2da341f1
	gomodules.xyz/jsonpatch/v3 v3.0.1
	google.golang.org/api v0.58.0
	gopkg.in/h2non/gock.v1 v1.0.15
	gopkg.in/yaml.v2 v2.3.0
	k8s.io/api v0.18.9
	k8s.io/apimachinery v0.18.9
	k8s.io/cli-runtime v0.18.9
	k8s.io/client-go v0.18.9
	k8s.io/klog v1.0.0
	k8s.io/kubectl v0.18.9
	sigs.k8s.io/controller-runtime v0.6.1
)
