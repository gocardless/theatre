# theatre-envconsul

[envconsul]: https://github.com/hashicorp/envconsul

This binary provides the functionality required to authenticate with and pull
secrets from Vault, along with the injection of these secrets into process
environment variables. It relies on Hashicorp's [`envconsul`][envconsul] tool
for the injection of secret material.

## `install`

Install `theatre-envconsul` and Hashicorp's `envconsul` into a specific path.
This is run in an init container in order to prepare a shared Kubernetes volume
with these binaries, as they will be needed by the primary pod containers in
order to fetch secrets from Vault.

## `exec`

This is run as pid 1 of containers that want to use secrets from Vault in their
application environments. This is a shim around Hashicorp's `envconsul`, and it:

- Performs an authentication flow with Vault, exchanging a Kubernetes service
  account token for a Vault token
- Renders a Hashicorp `envconsul` configuration file with the Vault token,
  specifying the command the `envconsul` should run and how to find Vault, etc.
- Exec's into `envconsul` with the rendered configuration file, leaving
  `envconsul` to run the command

## `base64-exec`

This is a hidden command, and is leveraged by `exec`. As `envconsul` only
provides a command string, not a list of command and arguments, it performs
shellword splitting inside the `envconsul` binary.

Shell splitting is unreliable, and we want to ensure any container command is
exec'd in the same way Kubernetes normally would, had we run it outside of the
theatre-envconsul command. To do this, `exec` renders an `envconsul` file with a
command string of:

```
command = "/usr/local/bin/theatre-envconsul base64-exec <base64-encoded-args>"
```

This means envconsul calls back into `theatre-envconsul` once it's prepared the
application environment with Vault secrets, at which point the `base64-exec`
command will decode the args and exec the command as it was originally specified
in the Kubernetes pod.
