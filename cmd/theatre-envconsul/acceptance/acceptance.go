package acceptance

import (
	"bytes"
	"io"

	kitlog "github.com/go-kit/kit/log"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// This should be in testdata, but right now our test runner doesn't support relative file
// access. We should aim to bring back the ability to run acceptance tests from the ginkgo
// wrapper.
const podFixtureYAML = `
---
apiVersion: v1
kind: Pod
metadata:
  generateName: read-a-secret-
  namespace: staging # provisioned by the acceptance kustomize overlay
spec:
  serviceAccountName: secret-reader
  restartPolicy: Never
  initContainers:
    # Create the secret backend in Vault, ensure all the policies, auth backends
    # and sentinel secret value are present.
    - name: configure-vault
      image: theatre:latest
      imagePullPolicy: Never
      command:
        - /usr/local/bin/theatre-envconsul
      args:
        - configure
        - --vault-address=http://vault.vault.svc.cluster.local:8200
        - --no-vault-use-tls
        - --vault-token=vault-token
  containers:
    - name: print-env
      image: theatre:latest
      imagePullPolicy: Never
      env:
        - name: VAULT_RESOLVED_KEY
          value: vault:secret/data/kubernetes/staging/secret-reader/jimmy
      command:
        - /usr/local/bin/theatre-envconsul
      args:
        - exec
        - --vault-address=http://vault.vault.svc.cluster.local:8200
        - --no-vault-use-tls
        - --install-path=/usr/local/bin
        - --command=env
`

func Run(logger kitlog.Logger, kubeConfigPath string) {
	var clientset *kubernetes.Clientset

	BeforeEach(func() {
		config, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
		Expect(err).NotTo(HaveOccurred(), "failed to construct kubernetes config")

		clientset, err = kubernetes.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes clientset")
	})

	Describe("theatre-envconsul", func() {
		It("Resolves env variables into the pod command", func() {
			decoder := scheme.Codecs.UniversalDeserializer()
			obj, _, err := decoder.Decode([]byte(podFixtureYAML), nil, nil)
			podFixture := obj.(*corev1.Pod)

			podsClient := clientset.CoreV1().Pods("staging")
			pod, err := podsClient.Create(podFixture)
			Expect(err).NotTo(HaveOccurred(), "failed to create pod")

			getPodPhase := func() corev1.PodPhase {
				pod, err := podsClient.Get(pod.Name, metav1.GetOptions{})
				if err != nil {
					return ""
				}

				return pod.Status.Phase
			}

			Eventually(getPodPhase).Should(
				Equal(corev1.PodSucceeded),
			)

			req := podsClient.GetLogs(pod.Name, &corev1.PodLogOptions{})
			logs, err := req.Stream()
			Expect(err).NotTo(HaveOccurred())
			defer logs.Close()

			var buffer bytes.Buffer
			_, err = io.Copy(&buffer, logs)

			Expect(err).NotTo(HaveOccurred())
			Expect(buffer.String()).To(ContainSubstring("VAULT_RESOLVED_KEY=eats-the-world"))
		})
	})
}
