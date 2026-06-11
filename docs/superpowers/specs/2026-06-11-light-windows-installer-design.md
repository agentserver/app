# Light Windows Installer With Mirrored Codex Runtime

**Date**: 2026-06-11
**Status**: Design approved; written spec pending user review
**Scope**: Windows x64 Inno installer and portable zip stop bundling `codex.exe` and the VS Code installer executable. Install-time setup downloads the Codex Windows runtime from domestic npm mirrors. Minimal VS Code mode installs VS Code through Microsoft Store / `winget`, then uses the existing onboarding configuration path.

## Motivation

The current Windows packaging flow builds large offline installers by prefetching and embedding:

- `codex.exe` from OpenAI GitHub Releases.
- `vscode-installer.exe` from Microsoft CDN.
- Existing app binaries, VSIX, loom driver/slave binaries, and helper scripts.

This makes the installer large and makes packaging dependent on direct access to GitHub and Microsoft CDN. The new installer should be small enough to build without those heavy payloads while still giving users a working setup in China.

The user-approved product behavior is:

- Build installer artifacts without bundled `codex.exe`.
- Build installer artifacts without bundled VS Code installer exe.
- During `星池指挥官` installation, download Codex from domestic mirrors.
- Prefer the pinned `0.136.0-win32-x64` Codex runtime; if it is unavailable on mirrors, use the latest Windows x64 Codex npm platform package.
- If the user chooses `极简风` / minimal VS Code, install VS Code from Microsoft Store using the same `winget` style as Codex Desktop, then let onboarding configure it.

## Mirror Findings

The OpenAI npm package provides per-platform native packages. For the current pinned runtime:

- Main package: `@openai/codex@0.136.0`
- Windows x64 platform package: `@openai/codex@0.136.0-win32-x64`
- Domestic npm tarball:
  `https://registry.npmmirror.com/@openai/codex/-/codex-0.136.0-win32-x64.tgz`
- USTC fallback tarball:
  `https://npmreg.proxy.ustclug.org/@openai/codex/-/codex-0.136.0-win32-x64.tgz`
- npm integrity:
  `sha512-zS6DAmvjdWeAB1CL9KTUMkwzTwfXtxHy8GAtePw2a93jIqawoG07fBxAXuyoHZ3QXQkwEgqBx1zEEh33gdIKAw==`
- npm shasum:
  `b1eddf5e906d5e23a35db293d96e0cc8390e5563`

The platform tarball contains more than one executable:

- `vendor/x86_64-pc-windows-msvc/bin/codex.exe`
- `vendor/x86_64-pc-windows-msvc/codex-path/rg.exe`
- `vendor/x86_64-pc-windows-msvc/codex-resources/codex-command-runner.exe`
- `vendor/x86_64-pc-windows-msvc/codex-resources/codex-windows-sandbox-setup.exe`

The installer must extract the full `vendor/x86_64-pc-windows-msvc` runtime tree, not only `codex.exe`. The destination keeps the existing app contract:

```text
%LOCALAPPDATA%\agentserver-app\bin\codex.exe
%LOCALAPPDATA%\agentserver-app\codex-path\rg.exe
%LOCALAPPDATA%\agentserver-app\codex-resources\codex-command-runner.exe
%LOCALAPPDATA%\agentserver-app\codex-resources\codex-windows-sandbox-setup.exe
```

Existing VS Code settings, loom driver config, and local slave config can continue to point at `%LOCALAPPDATA%\agentserver-app\bin\codex.exe`.

## Product Decision

Use a light installer by default. The installer still installs the same app components and supports the same two frontend modes:

| Mode | Default | Frontend install source | Codex runtime source | Onboarding configure step |
|---|---:|---|---|---|
| Codex Desktop | Yes | Existing Codex Desktop ensure flow | Domestic npm mirrors | `ConfigureCodexDesktop` |
| Minimal VS Code | No | Microsoft Store via `winget` | Domestic npm mirrors | `ConfigureVSCode` |

No Node.js or npm executable is required on the target machine. The app downloads npm tarballs directly and verifies npm integrity itself.

## Codex Runtime Installer

### Manifest

Add `packaging/windows/codex-manifest.json` and include it in both Inno and portable artifacts:

```json
{
  "package": "@openai/codex",
  "platform": "win32-x64",
  "pinned_version": "0.136.0-win32-x64",
  "strip_prefix": "vendor/x86_64-pc-windows-msvc/",
  "codex_exe": "bin/codex.exe",
  "required_files": [
    "bin/codex.exe",
    "codex-path/rg.exe",
    "codex-resources/codex-command-runner.exe",
    "codex-resources/codex-windows-sandbox-setup.exe"
  ],
  "pinned": {
    "integrity": "sha512-zS6DAmvjdWeAB1CL9KTUMkwzTwfXtxHy8GAtePw2a93jIqawoG07fBxAXuyoHZ3QXQkwEgqBx1zEEh33gdIKAw==",
    "shasum": "b1eddf5e906d5e23a35db293d96e0cc8390e5563",
    "urls": [
      "https://registry.npmmirror.com/@openai/codex/-/codex-0.136.0-win32-x64.tgz",
      "https://npmreg.proxy.ustclug.org/@openai/codex/-/codex-0.136.0-win32-x64.tgz"
    ]
  },
  "latest_metadata_urls": [
    "https://registry.npmmirror.com/@openai%2Fcodex/latest",
    "https://npmreg.proxy.ustclug.org/@openai%2Fcodex/latest"
  ],
  "package_metadata_url_templates": [
    "https://registry.npmmirror.com/@openai%2Fcodex/{version}",
    "https://npmreg.proxy.ustclug.org/@openai%2Fcodex/{version}"
  ]
}
```

`latest_metadata_urls` are used only after every pinned URL fails because the pinned version is unavailable. Transport errors first try the next mirror. Integrity mismatches are hard failures for that downloaded payload and must not silently fall through to an unverified file.

### Latest Fallback

If `0.136.0-win32-x64` cannot be found on every configured mirror:

1. Fetch `@openai/codex/latest` metadata from the configured domestic mirrors.
2. Read `optionalDependencies["@openai/codex-win32-x64"]`.
3. Parse values of the form `npm:@openai/codex@0.139.0-win32-x64`.
4. Fetch that exact platform package metadata from the same mirror family.
5. Require `dist.tarball` and `dist.integrity`.
6. Download `dist.tarball`, verify `dist.integrity`, and extract the runtime tree.

This allows Codex to move forward if the pinned version disappears from the mirror, while still avoiding unauthenticated "latest" downloads.

### Extraction Rules

Implement extraction in Go, exposed through `agentctl install-codex`, so Windows does not need npm, Node.js, GNU tar, or PowerShell archive modules.

The extractor:

- Downloads to `%LOCALAPPDATA%\agentserver-app\cache\codex\...`.
- Verifies npm `sha512` integrity before extraction.
- Opens the `.tgz` with standard gzip/tar readers.
- Only extracts entries under `vendor/x86_64-pc-windows-msvc/`.
- Strips that prefix into `%LOCALAPPDATA%\agentserver-app`.
- Rejects absolute paths, `..` segments, symlinks, hard links, and device files.
- Writes files through temporary paths and renames them into place.
- Confirms every `required_files` entry exists after extraction.
- Prints the installed Codex package version and destination path.

`agentctl install-codex` is idempotent. If all required files already exist and `bin/codex.exe --version` exits successfully, it skips download.

## Windows Installer Changes

### Inno Setup

`packaging/windows/installer.iss` should stop declaring these bundled payloads:

- `dist/cache/rust-v0.136.0/codex-x86_64-pc-windows-msvc.exe`
- `dist/cache/vscode/<version>/VSCodeUserSetup-x64-<version>.exe`

It should include:

- `packaging/windows/ensure-codex.ps1`
- `packaging/windows/codex-manifest.json`

Install order in `CurStepChanged(ssPostInstall)`:

1. Initialize machine identity with `machine.ps1`.
2. Run `ensure-codex.ps1`, which calls `agentctl.exe install-codex`.
3. Write `install-mode.json`.
4. Ensure the selected frontend:
   - default: existing Codex Desktop ensure script.
   - minimal VS Code: Store-based `ensure-vscode.ps1`.

`StageBundledCodexForLocalSlaves` is removed. Failure to download or verify Codex stops installation with a visible error and a log path.

### Portable Zip

`scripts/package-windows-zip.sh` should stop downloading, requiring, and copying:

- `CODEX_CACHE`
- `VSCODE_CACHE`
- `codex.exe`
- `vscode-installer.exe`

The staged zip includes `ensure-codex.ps1` and `codex-manifest.json`. `install.ps1` calls `ensure-codex.ps1` after machine identity setup and before frontend setup.

### Build Script

`scripts/package-windows.sh` should stop prefetching `codex.exe` and VS Code installer. Its preflight list should require the new Codex manifest and ensure script instead of the removed cache files.

The resulting Inno setup exe is still named the same unless release automation chooses to add a suffix later.

## Store-Based VS Code Install

`ensure-vscode.ps1` changes from "download a locked VS Code installer exe" to "install VS Code from Microsoft Store via winget".

Behavior:

1. Detect an existing usable `code` command.
2. If found, print version and skip install.
3. Require `winget.exe`.
4. Run a Microsoft Store install command:

```powershell
winget install --id XP9KHM4BK9FZ7Q -e -s msstore --accept-source-agreements --accept-package-agreements --disable-interactivity
```

5. Poll until a usable `code` command is detected.
6. Store the detected path/version in onboarding state when `EnsureVSCode` runs.

The package ID lives in the script as a constant and can later move to a manifest if Microsoft changes the Store ID.

Go-side `internal/vscode` install behavior should match the script. If onboarding sees minimal VS Code mode and VS Code is missing, `EnsureVSCode` should use the same Store / `winget` path rather than downloading a locked installer.

Detection should be updated to include Microsoft Store app execution aliases:

```text
%LOCALAPPDATA%\Microsoft\WindowsApps\code.exe
%LOCALAPPDATA%\Microsoft\WindowsApps\code.cmd
```

Existing user and Program Files install locations remain supported.

## Configuration Flow

Codex runtime download happens during Windows installation for every frontend mode. This supports local slaves and keeps the path stable before onboarding starts.

Frontend-specific configuration remains in onboarding:

- Codex Desktop mode uses `ConfigureCodexDesktop`.
- Minimal VS Code mode uses `ConfigureVSCode`.

This is deliberate. VS Code configuration installs the bundled VSIX, writes VS Code user settings, updates `~/.codex/config.toml`, starts token refresh where available, and writes loom driver config. Those operations belong to onboarding because they depend on state, paths, and completed logins.

## Error Handling

Codex runtime errors:

- All mirrors unavailable for pinned version and latest metadata unavailable: fail install with "无法从国内 npm 镜像下载 Codex".
- Integrity mismatch: fail install with "Codex npm 包校验失败" and include expected and actual digest in logs.
- Tarball missing `bin/codex.exe` or required resources: fail install with "Codex npm 包内容不完整".
- Existing `bin/codex.exe` fails `--version`: reinstall runtime from mirrors.

VS Code Store errors:

- `winget.exe` missing: fail with "未找到 winget；请安装或更新 Windows App Installer / Windows Package Manager 后重试。"
- `msstore` source unavailable: fail with "Microsoft Store source 不可用；请检查 Store 源、网络或企业策略。"
- Store install succeeds but `code` command is not detected: fail with a message that includes the winget output and app execution alias paths checked.

## Tests

Go tests:

- Codex manifest parses and contains pinned URLs, integrity, strip prefix, and required files.
- Codex resolver prefers pinned `0.136.0-win32-x64`.
- Codex resolver falls back to latest Windows platform package when pinned URLs return 404.
- Codex resolver rejects latest metadata without `dist.integrity`.
- Tar extractor strips `vendor/x86_64-pc-windows-msvc/` and writes `bin/codex.exe`.
- Tar extractor rejects path traversal and symlinks.
- `agentctl install-codex` skips when required files already exist and `codex.exe --version` succeeds.
- VS Code install plan builds the Microsoft Store `winget` command.
- VS Code detection checks WindowsApps aliases.
- Onboarding `EnsureVSCode` uses Store / `winget` install semantics when detection fails.

Packaging text tests:

- `installer.iss` no longer references `codex-x86_64-pc-windows-msvc.exe`.
- `installer.iss` no longer references `VSCodeUserSetup-x64`.
- `installer.iss` includes `ensure-codex.ps1` and `codex-manifest.json`.
- `installer.iss` runs Codex ensure before frontend ensure.
- `install.ps1` includes `ensure-codex.ps1` and calls it before frontend setup.
- `package-windows.sh` and `package-windows-zip.sh` no longer prefetch or copy `codex.exe` and `vscode-installer.exe`.
- `ensure-vscode.ps1` contains `winget`, `msstore`, and the Store package ID.

Manual Windows verification:

1. Build the light Inno installer.
2. Confirm the setup exe does not contain `codex.exe` or `vscode-installer.exe` in its file table.
3. Install default mode on a clean Windows user.
4. Confirm `%LOCALAPPDATA%\agentserver-app\bin\codex.exe` exists after install.
5. Confirm default mode launches Codex Desktop onboarding.
6. Install minimal VS Code mode on a clean Windows user.
7. Confirm VS Code is installed from Microsoft Store / `winget`.
8. Complete onboarding and confirm VS Code settings point to `%LOCALAPPDATA%\agentserver-app\bin\codex.exe`.

## Non-Goals

- Do not require Node.js or npm on the target machine.
- Do not add a UI for choosing Codex versions.
- Do not switch local slave or loom config paths away from `%LOCALAPPDATA%\agentserver-app\bin\codex.exe`.
- Do not remove the existing Codex Desktop mode.
- Do not implement offline VS Code fallback in this feature.
