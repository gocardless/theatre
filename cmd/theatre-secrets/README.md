# theatre-secrets


This binary provides the functionality required to authenticate with and pull
secrets from Vault, along with the injection of these secrets into process
environment variables.

## `install`

Install `theatre-secrets` into a specific path.  This is run in an init
container in order to prepare a shared Kubernetes volume with the binary,
as it will be needed by the primary pod containers in order to fetch secrets
from Vault.

## `exec`

This is run as pid 1 of containers that want to use secrets from Vault in their
application environments. It:

- Performs an authentication flow with Vault, exchanging a Kubernetes service
  account token for a Vault token
- For any environment variable that is formatted `vault:/some/secret`, fetches
  the secret and places its contents back into the env var
- For any environment variable that is formatted
  `vault-file:/some/secret:/some/path`, fetches the secret and places its
  contents at the provided path. The provided path is returned to the env var
  for convenience
- For any environment variable that is formatted `vault-file:/some/secret`,
  fetches the secret and places its contents at a temporary path based on the
  name of the secret. The temporary path is returned to the env var for
  convenience
- Runs the command providing the fetched secrets in the processes environment

