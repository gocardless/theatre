module github.com/gocardless/theatre

go 1.13

require (
	github.com/alecthomas/kingpin v2.2.6+incompatible
	github.com/go-logr/logr v0.1.0
	github.com/google/uuid v1.1.1
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.8.1
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.1.0
	github.com/sykesm/zap-logfmt v0.0.3
	go.uber.org/zap v1.12.0
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45
	golang.org/x/sys v0.0.0-20191210023423-ac6580df4449 // indirect
	google.golang.org/api v0.4.0
	gopkg.in/h2non/gock.v1 v1.0.15
	k8s.io/api v0.18.5
	k8s.io/apimachinery v0.18.5
	k8s.io/client-go v0.18.5
	sigs.k8s.io/controller-runtime v0.6.0
)
