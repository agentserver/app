# Light Windows Installer Implementation Record

**Date:** 2026-06-12
**Status:** Implemented in PR #4

## Goal

Build a light Windows installer that does not bundle `codex.exe` or `vscode-installer.exe`. The installer downloads the Codex Windows runtime from repository-pinned domestic npm mirror URLs during install, and minimal VS Code mode downloads and runs the Microsoft Store bootstrapper at install time.

## Implemented Architecture

- `internal/codexruntime` loads a repo-pinned manifest, produces pinned package candidates, verifies npm integrity, extracts only the Windows runtime tree, and rejects unsafe tar entries.
- `agentctl install-codex` runs the Codex runtime ensure flow used by Windows install scripts.
- `packaging/windows/codex-manifest.json` is the trust boundary for Codex runtime downloads. URLs, version, and integrity are pinned in the repository.
- `ensure-codex.ps1`, Inno setup, and portable zip install call `agentctl install-codex` during installation instead of staging a bundled `codex.exe`.
- `internal/vscode` and `ensure-vscode.ps1` use the Microsoft Store bootstrapper URL for minimal VS Code installs.
- Onboarding remains responsible for VS Code settings, VSIX installation, Codex config, token refresh, and loom driver config.

## Codex Runtime Trust Boundary

The runtime installer uses pinned candidates only:

1. Read `packaging/windows/codex-manifest.json`.
2. Try the manifest's pinned mirror URLs in order.
3. Verify the repository-pinned npm integrity before extraction.
4. Extract only entries under `vendor/x86_64-pc-windows-msvc/`.
5. Confirm required runtime files exist.
6. Run `codex.exe --version` and require it to match the manifest pin.

If every pinned mirror fails with unavailable status, installation fails with a visible retryable error. The installer does not resolve mirror-provided package metadata to discover a future runtime. Moving to a newer Codex runtime requires a repository change that pins the new version, URL list, and integrity.

Existing runtimes are accepted only when all required files exist and `codex.exe --version` matches the manifest pin. Older or mismatched runtimes are reinstalled from pinned mirrors.

## VS Code Bootstrapper Safety

The VS Code Microsoft Store bootstrapper path is hardened as follows:

- Downloads use bounded total, header, and body-idle timeouts.
- Downloads write to `.part` files and promote only after validation.
- Go validation checks minimum size and MZ executable header on all platforms.
- Windows Go validation additionally checks Authenticode status and Microsoft signer/chain details.
- PowerShell validation checks minimum size, MZ header, Authenticode status, Microsoft signer/chain details, and deletes partial files on failure.
- The bootstrapper process wait is bounded by `InstallTimeoutSeconds`; timeout kills the process and reports a clear install error.

## Verification Coverage

- Codex manifest parsing and required pinned fields.
- Pinned candidate order.
- No unpinned metadata fetch when pinned mirrors return unavailable status.
- Reinstall when an existing runtime version does not match the manifest pin.
- npm integrity verification.
- Safe tar extraction and required-file validation.
- VS Code Store bootstrapper URL planning and WindowsApps detection.
- Bootstrapper download body timeout and executable validation.
- Packaging text tests confirming removed bundled payloads and required install scripts.
- Documentation regression test preventing reintroduction of unpinned latest fallback language.
