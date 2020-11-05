# Consoles

The consoles component of the workloads controller, along with the associated
CRDs, provides the ability to spin up temporary pods that are configured
similarly to production workloads. This allows for operational tasks to be
executed in a secure and auditable fashion, without having to provide access to
the main production pods.

## How it works

1. A `ConsoleTemplate` object is created in the cluster, which defines the
   specification for a console.
2. The console users are granted the ability to create `Console` objects in a
   given namespace, which allows them to create a console using any
   `ConsoleTemplate` in that namespace. 
3. The console user submits a `Console` object to the cluster, to request a
   console. Using the `theatre-consoles` CLI is recommended.
4. The controller creates a `Job` for this console (which in turn creates a
   `Pod`), then once the pod is running will create the `Role` and `RoleBinding`
   to allow the owning user to `exec` into this pod.

### Authorised consoles

Given that console templates may be configured to provide the same environment,
including secrets and connectivity to external systems, as the primary
workloads, it may be desirable to limit which commands can be run by default in
these pods, to avoid both accidental damage and malicious use.

To facilitate this the `ConsoleTemplate` resource includes fields to define
rules around when a console should be immediately provisioned and when it
requires authorisation from another party.

Configure the `defaultAuthorisationRule` field to define the default behaviour
for a console: setting the `authorisationsRequired` field to a value greater
than 0 means that a user defined in the `subjects` list must authorise the
console for it to begin.

Optionally then also populate the `authorisationRules` list with additional
rules that enforce authorisation for given commands. This effectively provides
white-listing of known safe commands that can be run without authorisation, or
require authorisation from different parties for certain commands.

A console that requires authentication to proceed will stay in a
`PendingAuthorisation` state, until the necessary authorisations have been added
to the `ConsoleAuthorisation` object linked to this console.

## Custom resources

### `ConsoleTemplate`

The `ConsoleTemplate` resource defines the specification for how consoles of a
given class should operate, but does not trigger the creation of any further
resources by itself.

Typically it is desirable to define and deploy a `ConsoleTemplate` along with
all of the other manifests that make up an application deployment.
This will ensure that users of the console are provided with an environment
that's consistent with the main web/worker deployments, i.e. it is using the
same container image and has the same environment, volumes and metadata defined.

See [example `ConsoleTemplate`][example-consoletemplate] object.

[example-consoletemplate]: ../../../config/samples/workloads_v1alpha1_consoletemplate.yaml

## `Console`

Once a template is created, users can request a new console by submitting a
`Console` object that references this template.

The user can supply a command, but all other properties of the resulting pod
remain as specified by the `ConsoleTemplate` spec.

While the resource contains a `spec.user` field, this is not controllable by the
submitting user.
An admission webhook overrides the value of this field to be the entity that
created the object, as per the API server authentication chain. This ensures
that consoles can be linked back to the user that created them, as well as
enabling the [authorised consoles][#authorised-consoles] functionality.

See [example `Console`][example-console] object.

[example-console]: ../../../config/samples/workloads_v1alpha1_console.yaml

## `ConsoleAuthorisation`

As part of the [authorised consoles][#authorised-consoles] functionality, any
console that is created from a template that contains authorisation rules will
automatically trigger the creation of a `ConsoleAuthorisation` object, named the
same as the console.

This resource is used to capture the authorisation(s) of the console by third
parties.
Initially it will be created with an empty `authorisations` field, but any user
with access to update this object can append to this list, while a validating
webhook ensures that they can only append their user identifier.

The consoles controller manages the RBAC resources to allow only those subjects
defined by the matching authorisation rule to be able to update the object.

See [example `ConsoleAuthorisation`][example-consoleauth] object.

[example-consoleauth]: ../../../config/samples/workloads_v1alpha1_consoleauthorisation.yaml

## Access control and security considerations

> Note: Consoles depend upon the `DirectoryRoleBinding` resource, defined in
> this project and managed by the `rbac-manager` controller.
> This controller must also be running in the cluster (although Google
> integration can be disabled), or users will not be able to access their
> consoles.

The ability to run arbitrary consoles which mimic a production environment must
come with careful security considerations.

Our recommendation is to lock-down console creation by applying cluster RBAC
rules which limit the creation of consoles only to user who absolutely require
them, and provide a break-glass procedure for elevating permissions in an
emergency.

Users **must not** be granted the ability to `update` or `patch` consoles, even
if limited to `resourceNames` including only their own consoles. The workloads
controller currently depends on this constraint in order to maintain the
security of authorised consoles.

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
  - apiGroups: [""]
    resources: 
      - pods
    verbs:
      - watch
```
