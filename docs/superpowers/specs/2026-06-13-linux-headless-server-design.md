# Linux Headless Server Design

## Goal

Build a Linux server edition for machines without a desktop environment. The
package is a single extracted directory and exposes one user-facing binary,
`agentserver`. Running `agentserver` in a directory starts that directory as a
foreground slave. Running `agentserver install-driver` configures the current
user's Codex CLI to automatically mount the driver MCP server.

The Linux path must not change the Windows installer, launcher, or desktop
flows.

## Scope

- Linux `amd64` and `arm64` tarballs.
- One main CLI binary named `agentserver`.
- Foreground directory slave execution.
- Driver installation and remote workspace switching.
- Code-side modelserver authentication through device code.
- Agentserver-side device auth for driver and first-time slave joins.
- Local token proxy/refresher only when the user is not already using a
  long-lived modelserver API key.
- No systemd service in the first version.
- No desktop browser requirement.

## Confirmed Auth Facts

Linux code login uses Hydra's native device flow on `codeapi.cs.ac.cn`, not the
modelserver repository's `/oauth/device/code` wrapper. The wrapper exists in the
source tree but is not exposed by the current production deployment.

Use the existing public modelserver client id already used by the Windows PKCE
flow:

```text
5321f7e6-3d79-4ac9-a742-04809dbf9025
```

The code-side device flow uses:

- authorization endpoint: `https://codeapi.cs.ac.cn/oauth2/device/auth`
- token endpoint: `https://codeapi.cs.ac.cn/oauth2/token`
- scope: `project:inference offline_access`
- grant type: `urn:ietf:params:oauth:grant-type:device_code`
- refresh grant: `refresh_token`

The returned `access_token` and `refresh_token` are opaque to the client. The
client stores and refreshes them, but never parses token internals. The upstream
modelserver proxy introspects bearer tokens and reads extension claims such as
`project_id` and `user_id`; that interpretation stays server-side.

## Modelserver Access Rule

Every Linux `agentserver` command starts by ensuring modelserver access. The
check has two modes, in this order:

1. Prefer an existing long-lived API key. If `OPENAI_API_KEY` is set in the
   environment, repair `~/.codex/config.toml` to direct modelserver mode if
   needed, do not start the local proxy/refresher, and do not require code
   device login:

   ```toml
   [model_providers.modelserver]
   name = "modelserver"
   base_url = "https://code.ai.cs.ac.cn/v1"
   env_key = "OPENAI_API_KEY"
   wire_api = "responses"
   ```

2. Otherwise, ensure device-code credentials and a single user-level local
   proxy/refresher. The Codex modelserver provider is repaired to:

   ```toml
   [model_providers.modelserver]
   name = "modelserver"
   base_url = "http://127.0.0.1:53452/v1"
   env_key = "AGENTSERVER_CODEX_LOCAL_API_KEY"
   wire_api = "responses"
   ```

In proxy mode, the local proxy injects the latest access token from the secrets
store for every request to `https://code.ai.cs.ac.cn/v1`. The refresher uses the
stored refresh token and marks reauth-required on `invalid_grant` or missing
refresh token. The next user-facing command reruns code device login.

This rule applies to `agentserver`, `agentserver install-driver`,
`agentserver switch-workspace`, `agentserver serve-driver-mcp`, and future
diagnostic/login subcommands.

## Commands

### `agentserver`

The default command runs the current directory as a slave in the foreground.

Startup sequence:

1. Run the modelserver access rule.
2. Resolve the current directory to a canonical path.
3. Look up the canonical path in the slave registry.
4. If absent, prompt for a slave name. The default is
   `<hostname>-<dirname>`.
5. Write or repair the slave config for that directory.
6. Start `slave-agent` as a foreground child.
7. Capture auth URLs emitted by `slave-agent`.
8. If an `agent.cs.ac.cn` device URL appears, print the URL, user code when
   available, and an ASCII QR code.
9. Exit when the child exits. Ctrl-C stops the child.

The same directory always reuses the same slave identity and config unless the
registry is manually removed.

### `agentserver install-driver`

Configures the current user as a driver for ordinary Codex CLI sessions.

Startup sequence:

1. Run the modelserver access rule.
2. Ensure agentserver workspace credentials. If missing or invalid, run
   agentserver device auth and print the URL/code/QR.
3. Register or repair the driver config. The display name is
   `<hostname>-星池指挥官`.
4. Register the Codex MCP server globally in `~/.codex/config.toml`:

   ```toml
   [mcp_servers.driver]
   command = "/path/to/agentserver"
   args = ["serve-driver-mcp"]
   ```

5. Health-check the resulting setup and print the current remote workspace.

This command is idempotent. Re-running it repairs missing config and leaves the
selected workspace unchanged.

### `agentserver switch-workspace`

Reruns agentserver-side device auth so the user can select a different remote
workspace. Then it rewrites the driver config and keeps the Codex MCP entry
point unchanged.

The driver has no local directory concept. Its local workdir follows the Codex
CLI process that starts the MCP server. The only workspace switch here is the
remote agentserver workspace.

### `agentserver serve-driver-mcp`

Internal subcommand used by Codex MCP. It runs the modelserver access rule,
loads the current driver config, and then starts or execs
`driver-agent serve-mcp`.

If long-lived API key mode is active, it does not start the local
proxy/refresher. Otherwise it ensures the user-level singleton proxy/refresher
is running before serving MCP.

## Auth UX

Code-side auth prints:

- verification URL from Hydra, usually
  `https://codeapi.cs.ac.cn/oauth2/device/verify?...`
- user code
- ASCII QR code for the verification URL

Agentserver-side auth for drivers and slaves prints:

- `agent.cs.ac.cn` verification URL
- user code when present
- ASCII QR code

The CLI never attempts to open a browser on Linux.

## State And Secrets

All Linux user state lives under the existing `~/.agentserver-app` root unless
the existing path package is configured otherwise.

- `state.json`: driver workspace metadata, install status, and reauth flags.
- `machine.json`: existing machine identity.
- `slaves.json`: existing slave registry extended with
  `canonical_path -> slave_id` lookup.
- `secrets.json`: file fallback for secrets when the platform keyring is not
  available; permissions must be `0600`.
- `token-refresher.lock`: user-level singleton lock for the proxy/refresher.
- `bin/` or the extracted package directory: `agentserver`, `driver-agent`,
  `slave-agent`, and the optional pinned Codex runtime.

Linux secrets use the existing `internal/secrets` abstraction. Keyring is used
when available. If keyring setup fails or is unavailable on a headless server,
the CLI falls back to the `0600` file store.

## Packaging

Publish two tarballs:

- `linux-amd64`
- `linux-arm64`

Each tarball contains:

- `agentserver`
- pinned `driver-agent`
- pinned `slave-agent`
- metadata with versions and SHA256 checksums

The packaging script pulls `driver-agent` and `slave-agent` from the pinned Loom
release for the target architecture and verifies SHA256. Missing assets or
checksum mismatches fail the package build clearly.

Codex runtime handling:

1. Prefer `codex` already on `PATH`.
2. If missing, download or install the pinned runtime into the app-managed bin
   root used by existing paths.

## Error Handling

- If `OPENAI_API_KEY` is set but Codex config is not direct modelserver, repair
  Codex config to direct modelserver and do not start proxy/refresher.
- If no long-lived key exists and refresh is impossible, rerun code device
  login.
- If proxy port `127.0.0.1:53452` is occupied by this user's healthy proxy,
  reuse it.
- If the port is occupied by an unhealthy or unrelated process, fail with a
  clear error that names the port and suggests stopping the process.
- If a slave directory cannot be canonicalized, fail before writing registry
  state.
- If a child `slave-agent` or `driver-agent` is missing, fail with a message
  that names the expected path and package architecture.
- If device-code auth expires, print a concise expiry message and let the user
  rerun the same command.

## Testing

Unit tests:

- modelserver access mode chooses long-lived API key before proxy/refresher.
- modelserver device config uses Hydra native endpoints and the existing public
  client id.
- Codex config merge preserves unrelated settings while updating modelserver
  and driver MCP entries.
- slave registry maps canonical directory path to stable slave id.
- secrets fallback creates `0600` files.
- proxy/refresher lock allows one process and rejects duplicates.

Integration tests with fakes:

- fake codeapi device flow returns challenge, pending, slow_down, approved, and
  invalid_grant refresh cases.
- fake agentserver device flow returns driver and slave workspace credentials.
- fake `slave-agent` emits an auth URL; `agentserver` captures and prints it.
- fake `driver-agent serve-mcp` is launched through `serve-driver-mcp`.

Build and packaging tests:

- `GOOS=linux GOARCH=amd64 go test ./...`
- `GOOS=linux GOARCH=arm64 go test ./...`
- package dry-run validates asset selection, SHA256 checking, and archive
  layout.

Manual online verification:

- create a code device challenge against production and confirm pending token
  polling semantics.
- do not automate real user login in CI.

## Non-Goals

- No Linux desktop launcher.
- No systemd service.
- No root install path.
- No changes to Windows installer UX.
- No dependency on modelserver's currently unexposed `/oauth/device/code`
  wrapper.
