package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	kitlog "github.com/go-kit/kit/log"
	"github.com/hashicorp/vault/api"
	"github.com/pkg/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	AuthBackendMountPath = "kubernetes"
	AuthBackendRole      = "default"
	// use "=" characters in the secret to test the string splitting code in
	// theatre-secrets is correct
	SentinelSecretValue          = "eats=the=world"
	SentinelSecretFileValue      = "value\x00with\x00nulls"
	SentinelSecretValueNonASCII  = "valueΣwithλnonσASCIIμ"
	SentinelSecretValueShellword = "echo $(env)"
)

type Runner struct{}

func (r *Runner) Name() string {
	return "cmd/vault-manager/acceptance"
}

// Prepare is used for configuring a Vault server in our acceptance tests to provide
// Kubernetes authentication via service account.
//
// It does several things:
//
//   - Mounts a kv2 secrets engine at secret/
//
//   - Creates a Kubernetes auth backend mounted at auth/kubernetes
//
//   - Configures the Kubernetes backend to authenticate against the currently detected
//     Kubernetes API server (the current cluster, if run from within)
//
//   - For all successful Kubernetes logins, the user is assigned a token that maps to a
//     cluster-reader policy, which permits reading of secrets from:
//
//     secret/data/kubernetes/{namespace}/{service-account-name}/*
func (r *Runner) Prepare(logger kitlog.Logger, config *rest.Config) error {
	cfg := api.DefaultConfig()
	cfg.Address = "http://localhost:8200"
	cfg.Timeout = time.Second

	transport := cfg.HttpClient.Transport.(*http.Transport)
	transport.TLSClientConfig = nil

	client, err := api.NewClient(cfg)
	if err != nil {
		return errors.Wrap(err, "failed to configure vault client")
	}

	client.SetToken("vault-token") // set in the acceptance overlay (config/overlays/acceptance)

	// Wait for Vault to respond until we begin our preparation. Otherwise we might race
	// Vault when booting.
	for {
		logger.Log("event", "vault.connect")
		if resp, err := client.Sys().Health(); err == nil {
			if resp.Initialized && !resp.Sealed {
				break
			}
		}
	}

	mountPath := "secret"
	mountOptions := &api.MountInput{
		Type:        "kv",
		Description: "Generic Vault kv mount",
		Options: map[string]string{
			"version": "2",
		},
	}

	logger.Log("msg", "mounting secret engine", "path", mountPath, "options", mountOptions)
	client.Sys().Unmount(mountPath)
	if err := client.Sys().Mount(mountPath, mountOptions); err != nil {
		return err
	}

	enableOptions := &api.EnableAuthOptions{
		Type:        "kubernetes",
		Description: "Permit authentication by Kubernetes service accounts",
	}

	logger.Log("msg", "enabling auth mount", "path", AuthBackendMountPath, "options", enableOptions)
	client.Sys().DisableAuth(AuthBackendMountPath)
	if err := client.Sys().EnableAuthWithOptions(AuthBackendMountPath, enableOptions); err != nil {
		return err
	}

	var ca []byte = config.CAData

	if len(ca) == 0 {
		ca, err = os.ReadFile(config.CAFile)
		if err != nil {
			return errors.Wrap(err, "could not parse certificate for kubernetes")
		}
	}

	// We'll be running the acceptance tests from outside the kubernetes cluster, where the
	// API server will have an IP address that is relative to the host machine. When we're
	// within the cluster, like Vault, we want to talk to kubernetes.default.svc to ensure
	// we're tapping the host IP address.
	backendConfigPath := fmt.Sprintf("auth/%s/config", AuthBackendMountPath)
	backendConfig := map[string]interface{}{
		"kubernetes_host":    "https://kubernetes.default.svc",
		"kubernetes_ca_cert": string(ca),
		"issuer":             "api",
	}

	logger.Log("msg", "writing auth backend config", "path", backendConfigPath, "config", backendConfig)
	if _, err := client.Logical().Write(backendConfigPath, backendConfig); err != nil {
		return err
	}

	backendRolePath := fmt.Sprintf("auth/%s/role/default", AuthBackendMountPath)
	backendRoleConfig := map[string]interface{}{
		// https://github.com/hashicorp/vault-plugin-auth-kubernetes/pull/66
		"bound_service_account_names": strings.Split(
			"a*,b*,c*,d*,e*,f*,h*,i*,j*,k*,l*,m*,n*,o*,p*,q*,r*,s*,t*,u*,v*,w*,x*,y*,z*,1*,2*,3*,4*,5*,6*,7*,8*,9*,0*", ",",
		),
		"bound_service_account_namespaces": []string{"*"},
		"token_policies":                   []string{"default", "cluster-reader"},
		"token_ttl":                        600,
	}

	logger.Log("msg", "creating default backend role", "path", backendRolePath)
	if _, err := client.Logical().Write(backendRolePath, backendRoleConfig); err != nil {
		return err
	}

	auths, err := client.Sys().ListAuth()
	if err != nil {
		return errors.Wrap(err, "could not list auth backends which prevents linking roles against a backend")
	}

	backend := auths[fmt.Sprintf("%s/", AuthBackendMountPath)]
	readerPathTemplate :=
		"{{identity.entity.aliases.%s.metadata.service_account_namespace}}/" +
			"{{identity.entity.aliases.%s.metadata.service_account_name}}/" +
			"*"

	policyRules := fmt.Sprintf(
		`path "secret/data/kubernetes/%s" { capabilities = ["read"] }`,
		fmt.Sprintf(readerPathTemplate, backend.Accessor, backend.Accessor),
	)

	logger.Log("msg", "creating cluster-reader policy to permit kubernetes service accounts to read secrets")
	if err := client.Sys().PutPolicy("cluster-reader", policyRules); err != nil {
		return err
	}

	secretPath := "secret/data/kubernetes/staging/secret-reader/jimmy"
	secretData := map[string]interface{}{"data": map[string]interface{}{"data": SentinelSecretValue}}

	logger.Log("msg", "writing sentinel secret value", "path", secretPath)
	if _, err := client.Logical().Write(secretPath, secretData); err != nil {
		return err
	}

	secretPath = "secret/data/kubernetes/staging/secret-reader/shellword"
	secretData = map[string]interface{}{"data": map[string]interface{}{"data": SentinelSecretValueShellword}}

	logger.Log("msg", "writing shellword sentinel secret value", "path", secretPath)
	if _, err := client.Logical().Write(secretPath, secretData); err != nil {
		return err
	}

	secretFilePath := "secret/data/kubernetes/staging/secret-reader/file-with-binary-contents"
	secretFileData := map[string]interface{}{"data": map[string]interface{}{"data": SentinelSecretFileValue}}

	logger.Log("msg", "writing sentinel secret file", "path", secretFilePath)
	if _, err := client.Logical().Write(secretFilePath, secretFileData); err != nil {
		return err
	}

	secretFilePath = "secret/data/kubernetes/staging/secret-reader/file-non-ascii"
	secretFileData = map[string]interface{}{"data": map[string]interface{}{"data": SentinelSecretValueNonASCII}}

	logger.Log("msg", "writing non ascii sentinel secret file", "path", secretFilePath)
	if _, err := client.Logical().Write(secretFilePath, secretFileData); err != nil {
		return err
	}

	return nil
}

// This should be in testdata, but right now our test runner doesn't support relative file
// access. We should aim to bring back the ability to run acceptance tests from the ginkgo
// wrapper.
const rawPodYAML = `
---
apiVersion: v1
kind: Pod
metadata:
  generateName: read-a-secret-
  namespace: staging # provisioned by the acceptance kustomize overlay
spec:
  serviceAccountName: secret-reader
  restartPolicy: Never
  volumes:
    - name: theatre-secrets-serviceaccount
      projected:
        sources:
        - serviceAccountToken:
            path: token
            expirationSeconds: 900
  containers:
    - name: app
      image: theatre:latest
      imagePullPolicy: Never
      env:
        - name: VAULT_RESOLVED_KEY
          value: vault:jimmy
        - name: VAULT_TEST_SHELLWORD
          value: vault:shellword
      command:
        - /usr/local/bin/theatre-secrets
      args:
        - exec
        - --vault-address=http://vault.vault.svc.cluster.local:8200
        - --vault-path-prefix=secret/data/kubernetes/staging/secret-reader
        - --no-vault-use-tls
        - --service-account-token-file=/var/run/secrets/kubernetes.io/vault/token
        - --
        - env
      volumeMounts:
        - name: theatre-secrets-serviceaccount
          mountPath: /var/run/secrets/kubernetes.io/vault
`

// This pod tests that our mutating webhook injects theatre-secrets. We'll verify the
// environment is set correctly.
const annotatedPodYAML = `
---
apiVersion: v1
kind: Pod
metadata:
  generateName: read-a-secret-
  namespace: staging # provisioned by the acceptance kustomize overlay
  annotations:
    secrets-injector.vault.crd.gocardless.com/configs: app
spec:
  serviceAccountName: secret-reader
  restartPolicy: Never
  containers:
    - name: app
      image: theatre:latest
      imagePullPolicy: Never
      env:
        - name: VAULT_RESOLVED_KEY
          value: vault:jimmy
        - name: VAULT_TEST_SHELLWORD
          value: vault:shellword
      command:
        - env
`

// As with annotatedPodYAML, but this time non-root. This validates that non-root users
// can access the projected token, which relies on correctly setting the fsGroup.
const annotatedNonRootPodYAML = `
---
apiVersion: v1
kind: Pod
metadata:
  generateName: read-a-secret-
  namespace: staging # provisioned by the acceptance kustomize overlay
  annotations:
    secrets-injector.vault.crd.gocardless.com/configs: app
spec:
  serviceAccountName: secret-reader
  restartPolicy: Never
  containers:
    - name: app
      image: theatre:latest
      imagePullPolicy: Never
      securityContext:
        runAsNonRoot: true
        runAsUser: 1001
      env:
        - name: VAULT_RESOLVED_KEY
          value: vault:jimmy
        - name: VAULT_TEST_SHELLWORD
          value: vault:shellword
      command:
        - env
`

const annotatedNonRootPodYAMLFiles = `
---
apiVersion: v1
kind: Pod
metadata:
  generateName: read-a-secret-
  namespace: staging # provisioned by the acceptance kustomize overlay
  annotations:
    secrets-injector.vault.crd.gocardless.com/configs: app
spec:
  serviceAccountName: secret-reader
  restartPolicy: Never
  containers:
    - name: app
      image: theatre:latest
      imagePullPolicy: Never
      securityContext:
        runAsNonRoot: true
        runAsUser: 1001
      env:
        - name: VAULT_FILE_RESOLVED_KEY
          value: vault-file:jimmy:/tmp/jimmy
        - name: VAULT_TMP_FILE_RESOLVED_KEY
          value: vault-file:file-with-binary-contents
        - name: VAULT_NON_ASCII_FILE 
          value: vault-file:file-non-ascii
      command:
        - bash
        - -c
        - 'echo -n "file:" && cat $(echo $VAULT_FILE_RESOLVED_KEY) &&
           echo -n " tmp:" && cat $(echo $VAULT_TMP_FILE_RESOLVED_KEY) &&
           echo -n " ascii:" && cat $(echo $VAULT_NON_ASCII_FILE)'
`

func (r *Runner) Run(logger kitlog.Logger, config *rest.Config) {
	var (
		clientset      *kubernetes.Clientset
		podFixtureYAML string
	)

	BeforeEach(func() {
		var err error
		clientset, err = kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes clientset")
	})

	// Create pod from fixture, verify that pod runs successfully and resolves the secret
	// environment variable
	expectResolvesEnvVariables := func(expects func(buffer bytes.Buffer)) {
		ctx := context.Background()
		decoder := scheme.Codecs.UniversalDeserializer()
		obj, _, err := decoder.Decode([]byte(podFixtureYAML), nil, nil)
		Expect(err).NotTo(HaveOccurred(), "invalid pod spec")
		podFixture := obj.(*corev1.Pod)

		podsClient := clientset.CoreV1().Pods("staging")

		By("creating pod")
		pod, err := podsClient.Create(ctx, podFixture, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to create pod")

		getPodPhase := func() corev1.PodPhase {
			pod, err := podsClient.Get(ctx, pod.Name, metav1.GetOptions{})
			if err != nil {
				return ""
			}

			return pod.Status.Phase
		}

		By("waiting on pod to succeed")
		Eventually(getPodPhase, 10*time.Second).Should(
			Equal(corev1.PodSucceeded),
		)

		By("checking pod logs for secret value")
		req := podsClient.GetLogs(pod.Name, &corev1.PodLogOptions{})
		logs, err := req.Stream(ctx)
		Expect(err).NotTo(HaveOccurred())
		defer logs.Close()

		var buffer bytes.Buffer
		_, err = io.Copy(&buffer, logs)

		fmt.Fprint(GinkgoWriter, buffer.String())

		Expect(err).NotTo(HaveOccurred())
		expects(buffer)
	}

	expectsFunc := func(buffer bytes.Buffer) {
		Expect(buffer.String()).To(
			ContainSubstring(fmt.Sprintf("VAULT_RESOLVED_KEY=%s", SentinelSecretValue)),
		)
		Expect(buffer.String()).To(
			ContainSubstring(fmt.Sprintf("VAULT_TEST_SHELLWORD=%s", SentinelSecretValueShellword)),
		)
	}

	expectsFuncFiles := func(buffer bytes.Buffer) {
		Expect(buffer.String()).To(
			ContainSubstring(fmt.Sprintf("file:%s", SentinelSecretValue)),
		)
		Expect(buffer.String()).To(
			ContainSubstring(fmt.Sprintf("tmp:%s", strings.Split(SentinelSecretFileValue, "\n")[0])),
		)
		Expect(buffer.String()).To(
			ContainSubstring(fmt.Sprintf("ascii:%s", strings.Split(SentinelSecretValueNonASCII, "\n")[0])),
		)
	}

	Describe("theatre-secrets", func() {
		BeforeEach(func() { podFixtureYAML = rawPodYAML })

		It("Resolves env variables into the pod command", func() { expectResolvesEnvVariables(expectsFunc) })

		Context("As configured by the vault secrets-injector webhook", func() {
			BeforeEach(func() { podFixtureYAML = annotatedPodYAML })

			It("Resolves env variables into the pod command", func() { expectResolvesEnvVariables(expectsFunc) })

			Context("With a non-root user", func() {
				BeforeEach(func() { podFixtureYAML = annotatedNonRootPodYAML })

				It("Resolves env variables into the pod command", func() { expectResolvesEnvVariables(expectsFunc) })
			})
		})
	})

	Describe("theatre-secrets files", func() {
		Context("As configured by the vault secrets-injector webhook", func() {

			Context("With a non-root user", func() {
				BeforeEach(func() { podFixtureYAML = annotatedNonRootPodYAMLFiles })

				It("Resolves env variables into the pod command", func() { expectResolvesEnvVariables(expectsFuncFiles) })
			})
		})
	})
}
