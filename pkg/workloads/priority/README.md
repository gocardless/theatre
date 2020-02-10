# priority

Priority classes can be really useful to separate critical from optional
workloads. It's normal for all workloads within a particular namespace to have
the same priority class, but it's not possible to set a default on a namespace.

This package implements a webhook that permits setting a default priority class
for a given namespace. By applying the label `theatre-priority-injector:
<priority-class-name>` onto your namespace, you'll activate the webhook for all
pods.

If a pod already has a priority class, it will be ignored. Otherwise the pods
priority is set to match that of the namespace label.
