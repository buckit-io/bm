# Buckit Manager CLI (`bm`)

`bm` is the Buckit Manager command line tool for working with
[Buckit](https://github.com/buckit-io/buckit) and other S3-compatible object
storage services. It extends the open source
[MinIO `mc` CLI](https://github.com/minio/mc) with Buckit-specific management
features and a web-based UI.

It provides object storage commands similar to familiar UNIX tools such as
`ls`, `cat`, `cp`, `mirror`, and `diff`, plus administration commands for
Buckit deployments.

Full documentation: <https://buckit.sh/docs/reference/bm-cli>

## Buckit Manager Web UI

`bm` also includes Buckit Manager Web, a local web UI for deploying and
managing Buckit clusters.

Start the web UI:

```sh
bm web
```

By default, Buckit Manager opens at `http://127.0.0.1:9443/`.

Use the web UI to:

- Prepare a local single-node Buckit deployment on macOS or Windows.
- Deploy Buckit as a managed cluster on one or more Linux servers over SSH.
- Import an existing Buckit or MinIO cluster.
- Monitor cluster health, nodes, pools, and drives.
- Run supported cluster and node operations.

Web UI documentation:
<https://buckit.sh/docs/administration/buckit-manager>

## Install

Install `bm` on the computer where you want to run the CLI.

macOS and Linux:

```sh
curl -fsSL https://buckit-io.github.io/bm/install.sh | sh
bm --help
```

Windows PowerShell:

```powershell
irm https://buckit-io.github.io/bm/install.ps1 | iex
bm --help
```

The installer downloads the latest stable build for your platform, verifies
its SHA-256 checksum, and installs it into your user account.

## Quickstart

### 1. Add an Alias

Most `bm` commands operate against an alias. An alias stores the endpoint and
credentials for a Buckit or S3-compatible service.

```sh
bm alias set ALIAS HOSTNAME ACCESS_KEY SECRET_KEY
```

Example:

```sh
bm alias set mybuckit https://buckitserver.example.net ACCESS_KEY SECRET_KEY
```

If you omit `ACCESS_KEY` and `SECRET_KEY`, `bm` prompts for them.

To reduce the chance of credentials being saved in shell history, temporarily
disable history while setting an alias:

```sh
bash +o history
bm alias set mybuckit https://buckitserver.example.net ACCESS_KEY SECRET_KEY
bash -o history
```

### 2. Test the Connection

Use `bm admin info` to confirm that `bm` can connect to the deployment:

```sh
bm admin info mybuckit
```

If the command fails, check network connectivity, the service URL, and the
access key and secret key.

### 3. Work with Buckets and Objects

Create a bucket:

```sh
bm mb mybuckit/mydata
```

List buckets or objects:

```sh
bm ls mybuckit
bm ls mybuckit/mydata
```

Copy files to Buckit:

```sh
bm cp --recursive ~/mydata/ mybuckit/mydata/
```

Copy an object from Buckit:

```sh
bm cp mybuckit/mydata/object.txt ./object.txt
```

Display an object:

```sh
bm cat mybuckit/mydata/object.txt
```

Synchronize a local directory to Buckit:

```sh
bm mirror ~/mydata mybuckit/mydata
```

Watch a local directory and synchronize changes:

```sh
bm mirror --watch ~/mydata mybuckit/mydata
```

Remove objects:

```sh
bm rm mybuckit/mydata/object.txt
```

Use `--dry-run` before large or recursive remove operations:

```sh
bm rm --recursive --dry-run mybuckit/mydata
```

## Common Commands

| Command | Purpose |
| --- | --- |
| `bm alias set` | Add or update an alias for a Buckit or S3-compatible service. |
| `bm alias list` | List configured aliases. |
| `bm admin info` | Show deployment information and test connectivity. |
| `bm ls` | List buckets, prefixes, objects, or local files. |
| `bm mb` | Create a bucket or local directory. |
| `bm cp` | Copy files or objects between local storage and object storage. |
| `bm cat` | Print object or file contents to standard output. |
| `bm mirror` | Synchronize content between filesystems and object storage. |
| `bm rm` | Remove objects or local files. |
| `bm version` | Manage bucket versioning. |
| `bm retention` | Manage object retention settings. |
| `bm legalhold` | Manage object legal holds. |
| `bm tag` | Manage object tags. |
| `bm ilm` | Manage lifecycle rules and tiers. |
| `bm replicate` | Manage bucket replication. |
| `bm update` | Update the `bm` binary to the latest stable version. |

Run any command with `--help` for usage details:

```sh
bm COMMAND --help
bm cp --help
bm alias set --help
```

## Global Options

Common global options include:

| Option | Purpose |
| --- | --- |
| `--config-dir` | Use a custom configuration directory. |
| `--debug` | Print verbose debug output. |
| `--insecure` | Disable TLS certificate verification. Use only for trusted test environments. |
| `--json` | Print JSON lines output. |
| `--no-color` | Disable colored output. |
| `--quiet` | Suppress console output. |
| `--version` | Print the current `bm` version. |
| `--help` | Show command usage. |

Global options go before the command:

```sh
bm --json ls mybuckit
bm --debug admin info mybuckit
```

## Configuration

`bm` stores aliases and CLI settings in a JSON configuration file.

Default paths:

| Platform | Configuration path |
| --- | --- |
| Linux and macOS | `~/.config/bm/config.json` |
| Windows | `%APPDATA%\bm\config.json` |

You can override the configuration directory with `--config-dir` or the
`MC_CONFIG_DIR` environment variable.

## CLI TLS Certificates for Aliases

This section applies to the mc-style CLI commands that `bm` delegates to
`bm-cli`, such as `bm alias`, `bm ls`, and `bm admin`. It does not apply to
`bm web`, which serves its local UI over loopback HTTP by default.

Aliases for HTTPS endpoints with certificates trusted by the operating system
(for example, a public CA certificate) need no certificate files in the BM
configuration directory. For a self-signed or private-CA endpoint, `bm-cli`
can save the certificate you accept so later connections can verify that
endpoint.

Accepted certificates and certificate authorities are stored under the
configuration directory:

Linux, macOS, and other Unix-like systems:

```sh
~/.config/bm/certs/
~/.config/bm/certs/CAs/
```

Windows:

```powershell
%APPDATA%\bm\certs\
%APPDATA%\bm\certs\CAs\
```

When creating an alias for an untrusted HTTPS endpoint, `bm` displays the
service certificate's public-key fingerprint and asks whether to trust it. If
you confirm, it stores the certificate under `certs/CAs/` for subsequent
connections. Verify the fingerprint through an independent channel before
accepting it: trust-on-first-use cannot protect the first connection from a
network attacker.

For test environments only, some commands support `--insecure` to bypass
certificate verification.

## Update

Update `bm` to the latest stable version:

```sh
bm update
```

For best compatibility, use a `bm` version released close to your Buckit Server
version. A newer `bm` can usually work with older Buckit Server releases, but
large version differences may result in warnings or unsupported flags.

## More Documentation

- CLI reference: <https://buckit.sh/docs/reference/bm-cli>
- Administration commands: <https://buckit.sh/docs/reference/bm-admin>
- Buckit Manager web UI: <https://buckit.sh/docs/administration/buckit-manager>
