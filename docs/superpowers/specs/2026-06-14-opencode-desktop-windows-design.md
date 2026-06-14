# Windows OpenCode Desktop Mode Design

## Goal

Add OpenCode Desktop as a third Windows frontend mode alongside Codex Desktop and Minimal VS Code. The Windows installer must let the user choose exactly one frontend mode, install the selected frontend, and configure OpenCode so model requests go through the local agentserver model proxy and ultimately reach `https://code.ai.cs.ac.cn/v1`.

The default mode remains Codex Desktop for backward compatibility.

## Context

OpenCode Desktop is distributed as a Windows NSIS installer from:

- `https://opencode.ai/download/stable/windows-x64-nsis`

The current response is an executable download with:

- filename: `OpenCode Desktop Installer.exe`
- content type: `application/octet-stream`
- size around 115 MiB

OpenCode Desktop production builds use:

- product name: `OpenCode`
- app id: `ai.opencode.desktop`
- URL protocol: `opencode`
- Windows target: NSIS, per-user one-click install

OpenCode configuration uses global JSON/JSONC files under `~/.config/opencode/`, supports config merging, and supports `{env:VAR}` substitution. Custom providers use `provider.<id>.npm`, `provider.<id>.options.baseURL`, and `provider.<id>.options.apiKey`. For `/v1/responses`, OpenCode documents `@ai-sdk/openai` as the provider package.

Sources:

- https://opencode.ai/download
- https://opencode.ai/docs/config/
- https://opencode.ai/docs/providers/
- https://github.com/anomalyco/opencode/blob/dev/packages/desktop/electron-builder.config.ts
- https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/config/config.ts
- https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/config/variable.ts

## User Experience

The installer offers three mutually exclusive frontend modes:

1. Codex Desktop
2. OpenCode Desktop
3. Minimal VS Code

Portable install exposes `-OpenCodeDesktop` next to the existing `-MinimalVSCode`. Inno Setup exposes an "OpenCode Desktop" frontend task in the same task group as Minimal VS Code. If neither optional task is selected, Codex Desktop is selected.

Onboarding for OpenCode Desktop has the same auth flow as the other Windows modes:

1. Connect modelserver.
2. Connect agentserver workspace.
3. Install OpenCode Desktop.
4. Configure OpenCode Desktop.
5. Finalize shortcuts and context menu.

After onboarding, double-clicking the desktop shortcut opens the completed console and "Open frontend" launches OpenCode Desktop. Explorer right-click opens OpenCode Desktop for the selected folder in OpenCode mode.

## State Model

Add a third frontend mode:

```go
const FrontendModeOpenCodeDesktop FrontendMode = "opencode_desktop"
```

`NormalizeFrontendMode` accepts `opencode_desktop`, `minimal_vscode`, and defaults unknown or empty values to `codex_desktop`.

Add persisted state:

```go
type OpenCodeDesktopState struct {
    Installed     bool   `json:"installed"`
    Version       string `json:"version,omitempty"`
    Path          string `json:"path,omitempty"`
    InstalledByUs bool   `json:"installed_by_us"`
}
```

Add this field to `state.State`:

```go
OpenCodeDesktop OpenCodeDesktopState `json:"opencode_desktop"`
```

The sanitized UI state exposes `opencode_desktop_installed`, `opencode_desktop_version`, and `opencode_desktop_path` only for display and tests. It never exposes tokens.

## Paths

Add OpenCode paths to `internal/paths.Paths`:

- `OpenCodeConfigDir`: `<home>/.config/opencode`
- `OpenCodeConfigFile`: `<home>/.config/opencode/opencode.jsonc`

These are user-level OpenCode global settings. The implementation does not write project-level `opencode.json` files into user workspaces.

## OpenCode Configuration

Add `internal/opencode` for writing OpenCode configuration. It should:

- create the config directory if needed
- read existing `opencode.jsonc` or `opencode.json` when possible
- preserve unrelated user settings
- overwrite only the agentserver-managed `provider.modelserver` block and top-level `model`
- write atomically with user-only file permissions
- leave local proxy credentials outside the file by using env substitution

The managed provider config is:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "model": "modelserver/gpt-5.5",
  "provider": {
    "modelserver": {
      "npm": "@ai-sdk/openai",
      "name": "modelserver",
      "options": {
        "baseURL": "http://127.0.0.1:53452/v1",
        "apiKey": "{env:AGENTSERVER_CODEX_LOCAL_API_KEY}"
      },
      "models": {
        "gpt-5.5": {
          "name": "gpt-5.5"
        }
      }
    }
  }
}
```

Do not set `enabled_providers` initially. It is useful for hard restriction, but it would override users who already have other OpenCode providers configured. The selected default model is enough for the intended first-run behavior.

## Model Access Flow

OpenCode does not receive the real modelserver access token. It sends requests to:

```text
http://127.0.0.1:53452/v1
```

with:

```text
Authorization: Bearer {env:AGENTSERVER_CODEX_LOCAL_API_KEY}
```

The existing Windows desktop path persists:

```text
AGENTSERVER_CODEX_LOCAL_API_KEY=agentserver-local-proxy
```

and starts `token-refresher.exe`. The local proxy injects the current modelserver bearer token and refreshes it through the existing token refresh path. This avoids OpenCode long-lived processes holding a stale access token after refresh.

## OpenCode Desktop Install And Launch

Add `internal/opencodedesktop` with the same shape as `internal/codexdesktop`:

- `Detect() (Detected, error)`
- `EnsureInstalled(ctx, Options) (Detected, error)`
- `Launch(ctx, LaunchOptions) error`
- installer verification helpers

Detection order:

1. `opencode://` protocol registration under HKCU or HKLM.
2. Common per-user executable locations such as `%LOCALAPPDATA%\Programs\OpenCode\OpenCode.exe`.
3. Uninstall registry entries with exact display name `OpenCode`.

Installer behavior:

- Windows packaging downloads `https://opencode.ai/download/stable/windows-x64-nsis` into the build cache.
- Portable and Inno bundles it as `opencode-desktop-installer.exe`.
- `ensure-opencode-desktop.ps1` validates the file is an MZ executable, has a reasonable minimum size, and has a valid Authenticode signature.
- The script must not require the signer to be Microsoft. It should report the signer subject in errors for debugging.
- If the bundled installer fails or is missing, fall back to opening the official download URL or using a documented fallback only if OpenCode later publishes a stable package manager id. Do not invent a winget id.

Launch behavior:

- Prefer launching the detected `OpenCode.exe` directly.
- If a folder is supplied, set the process working directory to that folder.
- If no executable is found but the protocol is registered, open `opencode://` as a fallback.

The current design does not assume a documented OpenCode folder deep link. If OpenCode later documents one, add it behind tests.

## Orchestrator Changes

`EnsureFrontend` dispatches:

- `minimal_vscode` -> existing VS Code path
- `opencode_desktop` -> new OpenCode Desktop ensure path
- default -> existing Codex Desktop path

`ConfigureFrontend` dispatches:

- `minimal_vscode` -> existing VS Code config
- `opencode_desktop` -> shared model proxy setup plus OpenCode config
- default -> existing Codex Desktop config

`configureSharedCodex` remains the shared place that:

- writes Codex config for the bundled CLI and driver use
- persists `AGENTSERVER_CODEX_LOCAL_API_KEY`
- starts the token refresher
- installs the Loom driver MCP config

OpenCode configuration is an additional step after shared model proxy setup. It does not replace Codex CLI config because the driver and support tooling still depend on Codex CLI.

`LaunchAndShutdown`, completed-console launch, and `cmd/open-folder` all dispatch OpenCode mode explicitly.

## Installer And Packaging

Add `ensure-opencode-desktop.ps1`.

Update `write-install-mode.ps1` validation to:

```powershell
[ValidateSet('codex_desktop', 'opencode_desktop', 'minimal_vscode')]
```

Update portable `install.ps1`:

- add `[switch]$OpenCodeDesktop`
- reject `-OpenCodeDesktop` together with `-MinimalVSCode`
- require and copy `opencode-desktop-installer.exe`
- run `ensure-opencode-desktop.ps1` in OpenCode mode
- write `install-mode.json` with `opencode_desktop`

Update Inno:

- add OpenCode installer payload
- add `ensure-opencode-desktop.ps1`
- add `opencodedesktop` task in the frontend mode group
- make `opencodedesktop` and `minimalvscode` mutually exclusive Inno tasks
- ensure Codex Desktop is selected only when neither optional task is selected
- write `opencode_desktop` and run `ensure-opencode-desktop.ps1` when selected

Update packaging scripts:

- download and cache the OpenCode installer every build, like Codex Desktop
- verify MZ, minimum size, and Authenticode validity
- include the installer and helper script in portable and Inno payload checks

## Web UI Changes

Update frontend mode types to include:

```ts
'opencode_desktop'
```

Add OpenCode steps:

- `opencode_desktop_install`: `安装 OpenCode Desktop 智能助手`
- `opencode_desktop_configure`: `准备 OpenCode Desktop 智能助手`

`ActionStep` treats `opencode_desktop_configure` like the other configure actions and calls `api.configureFrontend()`.

Default frontend display name:

- `codex_desktop`: `Codex Desktop`
- `opencode_desktop`: `OpenCode Desktop`
- `minimal_vscode`: `极简界面`

## Error Handling

Installer failures should surface actionable errors:

- missing bundled installer
- too-small or non-MZ installer
- invalid Authenticode signature
- installer exit code
- install completed but detection still failed

Configuration failures should mention the target path and whether parsing or writing failed.

Launching should report whether executable detection failed, protocol fallback failed, or process start failed.

## Testing

Add or update tests for:

- `internal/state`: normalize and state JSON roundtrip for `opencode_desktop`
- `internal/installmode`: read/write `opencode_desktop`, BOM handling still works
- `internal/opencode`: config writer creates config, merges existing settings, overwrites only `provider.modelserver`, keeps unrelated providers, writes env-substituted API key
- `internal/opencodedesktop`: detection parsing, installer validation command structure, launch command selection
- `internal/ui`: ensure/configure dispatch, sanitized frontend name, completed steps
- `cmd/launcher`: completed mode launches OpenCode and starts proxy/refresher
- `cmd/open-folder`: OpenCode mode configures proxy and launches with the selected folder as working directory
- `internal/ui/web`: step config, completed map, API type, action step support
- Windows packaging tests: payload presence, task wiring, install mode value, portable switch behavior, download/cache verification

Verification before PR:

```sh
go test ./...
go test -race ./...
npm --prefix internal/ui/web test
make ui-build
make cross-windows
make package-windows-zip
```

Full Windows installation validation should run on the Windows test machine after building the installer package.

## Out Of Scope

- Replacing the Windows local proxy shared token with the Linux per-user random token scheme.
- Writing project-level OpenCode config files.
- Implementing a folder deep link for OpenCode without documented support.
- Installing OpenCode CLI separately from OpenCode Desktop.
