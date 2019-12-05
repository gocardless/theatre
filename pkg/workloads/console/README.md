# console

If you have a need for providing developers with the ability to spin up
arbitrary pods with the same configuration and access as production workloads,
this controller provides several CRDs that can enable this in a secure manner.

## `ConsoleTemplate`

The ConsoleTemplate CRD is like a standard Deployment, but it never directly
causes pods to be created. An example looks like this:

```yaml
---
apiVersion: workloads.crd.gocardless.com/v1alpha1
kind: ConsoleTemplate
metadata:
  name: app
spec:
  additionalAttachSubjects:
    - group:engineering@gocardless.com
  defaultTimeoutSeconds: 3600
  maxTimeoutSeconds: 7200
  template:
    spec:
      containers:
        - name: app
          command:
            - /bin/bash
```

This resource specifies what console pod might look like, by structuring the pod
under spec.template. This should match the specification of the production
workloads to ensure a consistent environment: your configuration management
tooling can stamp out this and the web/worker deployments.

## `Console`

Once a template is created, users can request a new console by submitting a
Console resource like so:

```yaml
---
apiVersion: workloads.crd.gocardless.com/v1alpha1
kind: Console
metadata:
  name: app-q47pw
spec:
  consoleTemplateRef:
    name: app
  command:
    - /bin/bash
  reason: link-to-ticket
  timeoutSeconds: 3600
  ttlSecondsAfterFinished: 86400
  user: lawrence@gocardless.com
```

The controller will only create the underlying pod for this console if it can
find a valid referenced ConsoleTemplate. The user can alter the command, but
everything else remains as specified by the ConsoleTemplate spec.

While the resource contains a `spec.user` field, this is not controllable by the
submitting user. An admission webhook overrides this field to be the value of
the authorised entity, as per the API server authentication chain. This ensures
consoles are linked back to the user that created them.

## Access control

Obviously, the ability to gain access to run arbitrary consoles comes with
security considerations. Our recommendation is to lock-down console creation by
applying cluster RBAC rules which limit the creation of consoles only to
developers who absolutely require them, and provide a break-glass procedure for
elevating permissions in an emergency.

A ClusterRole that provides the right permissions is:

```yaml
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: console-creator
rules:
  - apiGroups:
      - workloads.crd.gocardless.com
    resources:
      - consoletemplates
    verbs:
      - list
      - get
  - apiGroups:
      - workloads.crd.gocardless.com
    resources:
      - consoles
    verbs:
      - create
      - list
      - get
      - watch
  - apiGroups:
      - rbac.authorization.k8s.io
    resources:
      - rolebindings
    verbs:
      - list
      - get
      - watch
```
