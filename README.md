# Theatre

## Features

- [x] DirectoryRoleBinding: support Google group subjects
- [ ] SudoRoleBinding: temporarily join role binding
  - Paused due to kubernetes not supporting arbitrary subresources. Might need a
    rethink.
- [ ] Console implementation

## Operator Implementation

- [x] Verify all API groups are registered
- [x] Integrate Kubernetes events with logging
- [ ] Use caching informers to power controllers
- [ ] Unit/integration testing
- [ ] Acceptance testing with real kubernetes
- [ ] Auto-install CRDs into the cluster
- [x] Log changes to Kubernetes resources as events
- [ ] Auto-generate:
  - [ ] RBAC roles for the manager
  - [ ] Stateful set for the deployment
