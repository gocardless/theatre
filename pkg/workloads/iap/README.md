# iap

GoCardless make use of GCP's Identity Aware Proxy (IAP) to protect services
within GKE clusters. This package provides helpers to ease that integration.

## `Service`

We extend the native Service resource to support Google's IAP. You mark a
service as using IAP with the `protection: enabled` annotation, and specify a
member list of entities that are permitted to view this resource:

```yaml
---
apiVersion: v1
kind: Service
metadata:
  name: team
  annotations:
    iap.workloads.crd.gocardless.com/protection: enabled
    iap.workloads.crd.gocardless.com/members: |
      group:engineering@gocardless.com
      user:admin@gocardless.com
spec:
  selector:
    app: team
    role: backend
  ports:
    - port: 8080
      targetPort: 8080
```

This will ask theatre to enable the IAP on this service, if it can locate the
associated ingress. Once enabled, the IAM permissions for access will be set.

## `Pod`

It is required that any pods serving this service proxy traffic through
`theatre-iap-proxy`, which will validate any HTTP requests against Google's
public IAP keys. If you fail to do this, there is nothing to prevent
unauthenticated requests from accessing the pod while IAP is being configured,
or if it were to ever fail.

```yaml
---
apiVersion: v1
kind: Deployment
metadata:
  name: team
  labels: &labels
    app: team
    role: backend
spec:
  template:
    metadata:
      labels: *labels
    spec:
      # Expose only the port protected by the theatre-iap-proxy
      ports:
        - containerPort: 8080
      containers:
        - name: app
          image: my-app
          command: ['./serve-app', '--port', '4000']
        - name: iap-proxy
          image: theatre:latest
          command:
            - /usr/local/bin/theatre-iap-proxy
            - --port=8080
            - --liveness-probe=/_iap_health_check
            - --readiness-probe=/health_check
            - --target=localhost:4000
```
