# vault

[theatre-secrets]: ../../../cmd/theatre-secrets
[theatre-secrets-acceptance]: ../../../cmd/theatre-secrets/acceptance/acceptance.go

This package contains any CRDs and webhooks GoCardless use to interact with
Vault. At present, the only thing we provide is a webhook to automatically
configure Kubernetes pods with access to Vault secrets.

## Summary

GoCardless use Vault to manage application environment variable secrets. We
happen to build software in a variety of languages, and make use of process
environment variables as a language agnostic method of injecting configuration
material.

We needed a way to inject secrets from Vault into application environment
variables without requiring developers to write code to speak directly to Vault.
Besides the effort involved in writing that interaction, and the risk involved
in doing it incorrectly, there's no guarantee each language would be well
supported by the Vault ecosystem.

Instead, we've built a webhook that listens for pods with an annotations like:

```
secrets-injector.vault.crd.gocardless.com/configs: app:config/env.yaml
```

When our webhook sees this annotation, it tries configuring the `app` container
to pull secrets from Vault, using the configuration from the `config/env.yaml`
file within the container. It resolves these secrets and sets them as
environment variables, finally running the original container process.

The webhook makes use of the [`theatre-secrets`][theatre-secrets] command to
perform an authentication dance with Vault. Once we've acquired a Vault token,
we translate our simple [configuration file format](#config) into a Hashicorp
[secrets][secrets] config file, then use secrets to perform the fetching
and lease-management of the secret values.

## Configuring Vault

For this authentication flow to work, we expect Vault to be configured with a
Kubernetes authentication backend that points at the Kubernets API server. The
authentication exchange works as follows:

- Pods attempt a login with Vault using their service account token
- Vault receives a service account token, and uses this token to perform a token
  review request against the API server configured on this auth backend. If the
  request succeeds, we know the token is valid, and we permit the login

The theatre-secrets acceptance tests verify this flow against a Vault server.
If anything is unclear, look at the [Prepare][theatre-secrets-acceptance]
method for how we configure the test Vault server.

## How does the webhook work

Once installed, the webhook will listen for containers with a specific
annotation:

```yaml
---
apiVersion: v1
kind: Pod
metadata:
  name: app
  annotations:
    "secrets-injector.vault.crd.gocardless.com/configs": "app"
    "envconsul-injector.vault.crd.gocardless.com/configs": "app"
spec:
  containers:
    - name: app
      command:
        - env
```
Currently we still support the now deprecated envconsul-injector annotation as
well. The two should not be used together.

```yaml
---
apiVersion: v1
kind: Pod
metadata:
  name: app
  annotations:
    "envconsul-injector.vault.crd.gocardless.com/configs": "app"
spec:
  containers:
    - name: app
      command:
        - env
```

We will modify this pod to do the following...

### 1. Install binaries

Add an init container that installs the `theatre-secrets` tool into a temporary
installation path volume. We use a default storage medium. This installation
volume will be mounted into any of the containers that are targeted by the
`secrets-injector.vault.crd.gocardless.com/configs` annotation. In our example,
this means the `app` container is the only target.

```yaml
---
apiVersion: v1
kind: Pod
metadata:
  name: app
  annotations:
    "secrets-injector.vault.crd.gocardless.com/configs": "app"
spec:
  initContainers:
    - name: theatre-secrets-injector
      image: theatre:latest
      imagePullPolicy: IfNotPresent
      command:
        - theatre-secrets
        - install
        - --path
        - /var/run/theatre
      volumeMounts:
        - mountPath: /var/run/theatre
          name: theatre-secrets-install
  containers:
    - name: app
      command:
        - env
  volumes:
    - name: theatre-secrets-install
      emptyDir: {}
```

### 2. Add service account volume

Instead of relying on default Kubernetes service account volume mounts, we make
use of the projected service account volume mounts. Unlike default tokens, these
are managed by the kubelet and automatically rotated at regular intervals, and
whenever the pod is destroyed.

We mount the emphemeral token at `/var/run/secrets/kubernetes.io/vault`. In
terms of additional pod configuration, this means we add a projected volume and
volume-mount to any targeted containers:

```yaml
---
apiVersion: v1
kind: Pod
metadata:
  name: app
  annotations:
    "secrets-injector.vault.crd.gocardless.com/configs": "app"
spec:
  initContainers: ...
  containers:
    - name: app
      command:
        - env
      volumeMounts:
        - name: theatre-secrets-serviceaccount
          mountPath: /var/run/secrets/kubernetes.io/vault
  volumes:
    - name: theatre-secrets-install
      emptyDir: {}
    - name: theatre-secrets-serviceaccount
      projected:
        sources:
          - serviceAccountToken:
              path: token
              expirationSeconds: 900
```

### 3. Prepend theatre-secrets inject

The container must resolve secrets before we run the original command. We use
theatre-secrets to perform the resolution, then exec the original container
command. The application container has access to the theatre binaries via the
init container, having installed them in the `theatre-secrets-install` volume:

```yaml
---
apiVersion: v1
kind: Pod
metadata:
  name: app
  annotations:
    "secrets-injector.vault.crd.gocardless.com/configs": "app"
spec:
  initContainers: ...
  containers:
    - name: app
      command:
        - /var/run/theatre/theatre-secrets
      args:
        - exec
        - --vault-address=http://vault.vault.svc.cluster.local:8200
        - --vault-path-prefix=secret/data/kubernetes/project/namespace/app
        - --install-path=/var/run/theatre
        - --service-account-token-file=/var/run/secrets/kubernetes.io/vault/token
        - --
        - env
      volumeMounts:
        - name: theatre-secrets-install
          mountPath: /var/run/theatre
        - name: theatre-secrets-serviceaccount
          mountPath: /var/run/secrets/kubernetes.io/vault
  volumes:
    - name: theatre-secrets-install
      emptyDir: {}
    - name: theatre-secrets-serviceaccount
      projected:
        sources:
          - serviceAccountToken:
              path: token
              expirationSeconds: 900
```
