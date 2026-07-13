# ChatGPT Desktop / Codex Windows Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to
> implement this plan task-by-task. Every production change follows a focused red-green test cycle.
> After implementation, send the full diff to the same `gpt-5.5 xhigh` Codex reviewer and fix every
> Critical/Important issue before completion.

**Goal:** Adapt the Windows `codex_desktop` mode to the new ChatGPT desktop app while preserving
legacy Codex support, securely validating `codex://`, proving that the trusted desktop process
actually starts, and surfacing actionable failures.

**Architecture:** Keep the persisted `codex_desktop` compatibility identifier, but replace the
binary installed/not-installed probe with a typed availability model. A single PowerShell detector
is embedded by Go and shipped beside the Windows installer so runtime launch and installer checks
share the same exact-PFN/AppX-contract validation. Launch performs preflight, direct activation of
the verified AppUserModelID, and bounded process-identity confirmation. Packaging moves to the
official ChatGPT Store ID and binds the bundled bootstrapper to a generated metadata manifest plus
a shared hardened Authenticode policy.

**Tech stack:** Go, embedded PowerShell 5.1-compatible scripts, Vue 3/Vitest, Bash packaging,
Inno Setup, existing Go source-contract tests.

**Approved design:**
`docs/superpowers/specs/2026-07-10-chatgpt-codex-desktop-windows-design.md`

---

## Compatibility and security constants

Use these exact values throughout implementation and tests:

```text
ChatGPT Store Product ID: 9NT1R1C2HH7J
ChatGPT PFN:              OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0
Legacy Codex PFN:         OpenAI.Codex_2p2nqsd0c76g0
Protocol:                 codex
Fixed ProgID query flag:  ASSOCF_INIT_FIXED_PROGID = 0x0800
Short display name:       ChatGPT / Codex
Long display name:        ChatGPT 桌面应用（含 Codex）
Signer root SHA-256:      847DF6A78497943F27FC72EB93F9A637320A02B561D0A91B09E87A7807ED7C61
Timestamp root SHA-256:   DF545BF919A2439C36983B54CDFC903DFA4F37D3996D8D84B4C31EEC6F3C163E
```

No fuzzy package/process matching, registry command execution, or broad System32 allowlist is
permitted. A Windows-only behavior that cannot run on this Linux host must still have parser tests,
source-contract tests, and an explicit unverified-Windows handoff note.

## Task 1: Add the typed availability model and strict parser

**Files:**

- Modify: `internal/codexdesktop/detect.go`
- Modify: `internal/codexdesktop/detect_test.go`

- [ ] **Step 1: Write failing table tests for every availability state**

Add tests for compact JSON payloads representing `ready`, `not_installed`, `scheme_missing`, and
`scheme_target_invalid`. Assert the returned `Detected` fields and `errors.Is` behavior for
`ErrNotFound`, `ErrSchemeMissing`, and `ErrSchemeTargetInvalid`.

Add rejection tests for empty output, unknown status/PFN, missing install location on an installed
package, contradictory scheme flags, and a non-zero PowerShell execution error. For `ready`, reject
an empty AppUserModelID, an AUMID without exactly one `!ApplicationId` separator, an empty
ApplicationId, a PFN prefix different from the selected `PackageFamilyName`, and either untrusted
PFN. The operational error must not satisfy an availability sentinel.

- [ ] **Step 2: Run the focused parser tests and observe the expected compile/test failure**

```bash
go test -count=1 ./internal/codexdesktop -run 'TestDetectedFromPowerShellOutput|TestDetectionPayload'
```

Expected: FAIL because typed statuses, fields, and sentinel errors do not exist.

- [ ] **Step 3: Implement strict status parsing**

Add `Status`, the five status constants, exact package constants, display-name constants, and these
fields to `Detected`: `Status`, `PackageFamilyName`, `InstallLocation`, `AppUserModelID`,
`SchemeRegistered`, and `SchemeTargetValid`.

Replace the text sentinel parser with `encoding/json`. Validate field combinations before returning:

- `ready`: trusted PFN, non-empty normalized-source install location, both scheme flags true, and an
  AppUserModelID exactly `<PackageFamilyName>!<non-empty ApplicationId>` whose PFN prefix equals the
  selected trusted package;
- `not_installed`: `Installed == false`;
- `scheme_missing`: trusted installed package and `SchemeRegistered == false`;
- `scheme_target_invalid`: trusted installed package, scheme registered, target invalid.

Wrap the corresponding sentinel while preserving the populated `Detected` value. Treat malformed or
inconsistent output as an operational error.

- [ ] **Step 4: Re-run focused tests**

```bash
go test -count=1 ./internal/codexdesktop -run 'TestDetectedFromPowerShellOutput|TestDetectionPayload'
```

Expected: PASS.

## Task 2: Implement one secure Windows detector shared by Go and packaging

**Files:**

- Create: `internal/codexdesktop/detect_windows.ps1`
- Modify: `internal/codexdesktop/detect_windows.go`
- Create: `internal/codexdesktop/windows_system.go`
- Modify: `internal/codexdesktop/detect_test.go`
- Modify: `packaging/windows/ensure-codex-desktop.ps1`
- Modify: `internal/vscode/install_test.go`

- [ ] **Step 1: Add failing source-contract tests**

Tests must require the shared script to:

- match only the two exact PFNs; select `ready` from the unique effective AUMID mapping and use the
  ChatGPT PFN preference only for non-ready diagnostic metadata;
- query the effective association with
  `IApplicationAssociationRegistration.QueryCurrentDefault(..., AL_EFFECTIVE)`;
- resolve only the documented
  `AssocQueryStringW(ASSOCF_INIT_FIXED_PROGID = 0x0800, ASSOCSTR_APPID = 21,
  effectiveProgID, NULL, ...)` result, and reject a flags=0 ProgID lookup;
- parse the exact package manifest with `Get-AppxPackageManifest`;
- bind `PackageFamilyName!ApplicationId` to an application declaring exact `windows.protocol` name
  `codex`;
- reject ambiguous mappings and every association without a unique exact AUMID/PFN/manifest match;
- contain no UserChoice/HKCR/AppModel Repository read, `Invoke-Expression`, registry-command
  parsing/invocation, fuzzy `*ChatGPT*`/`*Codex*`, or process-name fallback;
- classify missing association separately from invalid association.
- require Go detector execution to resolve the trusted Windows PowerShell 5.1 executable through
  `GetSystemDirectory` plus `WindowsPowerShell\v1.0\powershell.exe`; source tests must reject a bare
  `powershell.exe`, `exec.LookPath`, and all PATH/search-order resolution.

Tests must also require `detect_windows.go` to embed this script and require
`ensure-codex-desktop.ps1` to dot-source the shipped copy instead of maintaining separate detection
rules.

- [ ] **Step 2: Run the focused tests and observe failure**

```bash
go test -count=1 ./internal/codexdesktop ./internal/vscode \
  -run 'TestWindowsDetect|TestEnsureCodexDesktopDetection'
```

Expected: FAIL because the shared detector and new PFN/AppX rules do not exist.

- [ ] **Step 3: Implement `detect_windows.ps1` as a function-only module**

Expose `Get-ChatGPTCodexDetection`, returning one `PSCustomObject`; do not execute a main block when
dot-sourced. The function must:

1. query current-user AppX packages and select only the exact new/legacy PFN;
2. load the selected package manifest and collect exact application IDs declaring `codex` under a
   `windows.protocol` extension using namespace-independent XML selectors;
3. resolve the actual effective ProgID using `QueryCurrentDefault` with URL protocol and
   `AL_EFFECTIVE`, rather than reproducing UserChoice/HKCR precedence;
4. validate the ProgID character set/length, then query `ASSOCSTR_APPID` with
   `ASSOCF_INIT_FIXED_PROGID = 0x0800`. The input is already the effective ProgID returned by
   `QueryCurrentDefault`, so flags=0 is forbidden because it could remap the value through current
   defaults a second time. A failed/empty fixed-ProgID lookup is `scheme_target_invalid`;
5. require exactly one installed exact package manifest application to equal that AppUserModelID;
6. return the validated AppUserModelID with sanitized facts/status only. Never read or return raw
   registry command, DelegateExecute, broker executable, or private AppModel metadata.

An orphan scheme without a trusted package remains `not_installed`. An installed package with no
association is `scheme_missing`; an unbound/ambiguous AppX ProgID, traditional handler, LOLBin, or
AUMID/PFN/ApplicationId/manifest mismatch is `scheme_target_invalid`.

- [ ] **Step 4: Embed and invoke the shared detector from Go**

Use `//go:embed detect_windows.ps1`. Execute static script text plus a fixed call to
`Get-ChatGPTCodexDetection | ConvertTo-Json -Compress` via an absolute executable returned by
`GetSystemDirectory` joined with `WindowsPowerShell\v1.0\powershell.exe`, using
`-NoProfile -NonInteractive -Command`. Do not use `exec.LookPath`, PATH resolution, a bare executable
name, or `ExecutionPolicy Bypass`; do not interpolate paths, URLs, or registry values into script
source. A system-directory or process-launch failure is an operational detector error and must never
be translated to `not_installed`.

- [ ] **Step 5: Reuse the detector from the installer script**

Add a `DetectionScriptPath` parameter defaulting to the adjacent shipped script, dot-source it by
literal known path, and branch on the typed status. Only `not_installed` may install. `ready` skips;
scheme missing/invalid fails with Repair/Reset/Reinstall guidance. Post-install polling requires
`ready`.

Preserve the UTF-8 BOM on `ensure-codex-desktop.ps1`.

- [ ] **Step 6: Run focused tests**

```bash
go test -count=1 ./internal/codexdesktop ./internal/vscode \
  -run 'TestWindowsDetect|TestEnsureCodexDesktopDetection'
```

Expected: PASS.

## Task 3: Move Go and PowerShell installation to the exact ChatGPT Store ID

**Files:**

- Modify: `internal/codexdesktop/winget.go`
- Modify: `internal/codexdesktop/winget_test.go`
- Modify: `internal/codexdesktop/install.go`
- Modify: `internal/codexdesktop/install_test.go`
- Modify: `packaging/windows/ensure-codex-desktop.ps1`
- Modify: `internal/vscode/install_test.go`

- [ ] **Step 1: Write failing winget and install-state tests**

Assert the exact arguments:

```text
install --id=9NT1R1C2HH7J --source=msstore --exact
--accept-package-agreements --accept-source-agreements --disable-interactivity
```

Add `EnsureInstalled` tests proving:

- `ready` skips winget;
- only `ErrNotFound` invokes winget;
- scheme missing/invalid returns actionable repair errors without invoking winget;
- post-install `ready` succeeds;
- post-install missing/invalid scheme fails with its sentinel and does not claim installation success.

PowerShell source tests must assert the same exact ID/source/exact flags and reject the old product
name command.

- [ ] **Step 2: Run focused tests and observe failure**

```bash
go test -count=1 ./internal/codexdesktop ./internal/vscode \
  -run 'TestWinget|TestClassifyWinget|TestEnsureInstalled|TestEnsureCodexDesktopScriptUses'
```

- [ ] **Step 3: Implement exact install arguments and actionable errors**

Update Go and PowerShell commands, user-facing names, and error classifiers. Preserve `errors.Is` for
availability sentinels. Error text must include the exact Product ID and must not depend on a
localized Store product name.

- [ ] **Step 4: Re-run focused tests**

```bash
go test -count=1 ./internal/codexdesktop ./internal/vscode \
  -run 'TestWinget|TestClassifyWinget|TestEnsureInstalled|TestEnsureCodexDesktopScriptUses'
```

Expected: PASS.

## Task 4: Directly activate the verified package and confirm a trusted app process

**Files:**

- Modify: `internal/browser/open.go`
- Modify: `internal/browser/open_windows.go`
- Modify: `internal/browser/open_other.go`
- Modify: `internal/browser/open_windows_source_test.go`
- Modify: `internal/codexdesktop/launch.go`
- Create: `internal/codexdesktop/launch_windows.go`
- Create: `internal/codexdesktop/launch_other.go`
- Create: `internal/codexdesktop/process_snapshot_windows.ps1`
- Create: `internal/codexdesktop/process_snapshot.go`
- Modify: `internal/codexdesktop/windows_system.go`
- Create: `internal/codexdesktop/hresult.go`
- Modify: `internal/codexdesktop/launch_test.go`
- Modify: `internal/codexdesktop/detect_test.go`

- [ ] **Step 1: Write failing launch-state and liveness tests**

Introduce package-private launch options for detector, protocol activator, process snapshotter,
timeout, poll interval, and sleep control. Keep the production `Launch(ctx, folder)` entry point
non-injectable. Add tests for:

- every preflight state and operational detector failure;
- `ready` values with an empty/malformed AppUserModelID, missing/empty `!ApplicationId`, mismatched
  PFN prefix, or untrusted PFN are rejected before the activator is called;
- canceled context before any side effect;
- activator error -> `ErrLaunchFailed` plus Repair/Reset/Reinstall guidance;
- activation success but no trusted process before timeout -> `ErrLaunchFailed`;
- a new trusted package PID in the first post-launch sample -> success;
- an already-running trusted package PID only after two consecutive post-launch samples -> success;
- snapshot error -> visible launch failure;
- a process under the same PFN/path but owned by another SID or in another session is ignored;
- folder URL round-trip remains correctly encoded.

Add parser/source tests for process snapshots: exact PFNs and file-handle-resolved final
`InstallLocation` containment, no process-name or fuzzy command-line matching, and
empty/inaccessible `ExecutablePath` skipped. Require the Windows final-path helper and reject a
pure `[System.IO.Path]::GetFullPath` containment decision. Cover a lexical child that resolves
through a reparse point outside the package root, a sibling-prefix collision, and final-path handle
failure; none may be emitted or confirm launch.

- [ ] **Step 2: Run focused tests and observe failure**

```bash
go test -count=1 ./internal/browser ./internal/codexdesktop \
  -run 'TestOpenWindows|TestLaunch|TestProcessSnapshot'
```

- [ ] **Step 3: Harden the generic Windows URL opener**

Add `browser.OpenContext`; keep `Open` as a background-context compatibility wrapper. Windows uses
`GetSystemDirectory` and absolute paths for both `rundll32.exe` and `url.dll`, then
`exec.CommandContext(...).Run()` so PATH/DLL search cannot redirect the helper and exit errors are
observable. Non-Windows keeps existing best effort with `CommandContext`. This generic opener is
not the trusted Codex activation path.

- [ ] **Step 4: Implement direct AUMID protocol activation**

On Windows, revalidate `Detected` and call
`IApplicationActivationManager::ActivateForProtocol` with the exact validated AppUserModelID and
an `IShellItemArray` created from the fixed `codex://` URL. Load system DLLs by fixed system names,
classify HRESULT failure by the high bit so `CoInitializeEx` `S_FALSE` remains success, balance COM
lifetime, and never invoke a registry command or ask Shell to resolve the association again.

- [ ] **Step 5: Implement exact package-process snapshots**

Embed a static PowerShell script that re-queries only the two exact packages. Add a static C# helper
through `Add-Type` that opens directories/files with `CreateFileW` (`FILE_READ_ATTRIBUTES`,
`FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE`, and `FILE_FLAG_BACKUP_SEMANTICS` for package
directories), then calls `GetFinalPathNameByHandleW` with normalized DOS-volume output. Resolve both
the AppX `InstallLocation` and each candidate executable through this helper, normalize the same
`\\?\` prefix form, append one trailing separator to the final package root, and use
`OrdinalIgnoreCase` true-child containment. Never fall back to `GetFullPath` when handle opening or
final-path resolution fails; this prevents reparse-point and sibling-prefix escapes.

Capture the calling process's SessionId and `[WindowsIdentity]::GetCurrent().User.Value`. For each
`Win32_Process`, first require the same SessionId and non-empty `ExecutablePath`, then resolve and
contain its final path as above, call the bounded `GetOwnerSid` CIM method, and require an exact
current-user SID match. Missing/failed owner, session, file-handle, or final-path reads skip the
candidate (fail closed). Only then emit PID/PFN/install-location/SID/session facts. Go parses bounded
JSON and independently requires the already validated PFN, normalized final install location,
current SID, and SessionId. Do not pass registry commands or user folder strings into the script.

Source/parser tests must explicitly cover same package/path with different SessionId, different SID,
unreadable owner, lexical-only path, reparse escape, sibling-prefix collision, and failed final-path
resolution; none may confirm startup. Resolve Windows PowerShell through the absolute
`System32\WindowsPowerShell\v1.0\powershell.exe` path.

- [ ] **Step 6: Implement launch preflight and bounded confirmation**

Production Windows defaults are `Detect`, direct `ActivateForProtocol`, and the exact snapshotter.
After successful preflight, create one 10-second deadline covering the initial snapshot, activation,
and 250ms polling. A PID absent from baseline may confirm on the first post-launch sample. A baseline
PID confirms only when it survives two consecutive post-launch samples. Context cancellation aborts
polling.

All expected failures return a message beginning with the specific availability diagnosis and ending
with Windows Repair/Reset/Reinstall instructions, while wrapping the sentinel/cause.

- [ ] **Step 7: Re-run focused tests**

```bash
go test -count=1 ./internal/browser ./internal/codexdesktop \
  -run 'TestOpenWindows|TestLaunch|TestProcessSnapshot'
```

Expected: PASS.

## Task 5: Propagate launch failures and update product copy

**Files:**

- Modify: `cmd/launcher/main.go`
- Modify: `cmd/launcher/main_test.go`
- Create: `internal/codexdesktop/errors.go`
- Modify: `cmd/open-folder/main.go`
- Modify: `cmd/open-folder/main_test.go`
- Modify: `cmd/agentctl/main.go`
- Modify: `cmd/agentctl/cmd_test_subcommands.go`
- Modify: `cmd/agentctl/cmd_test_subcommands_test.go`
- Modify: `internal/ui/orchestrator.go`
- Modify: `internal/ui/orchestrator_real.go`
- Modify: `internal/ui/orchestrator_real_test.go`
- Modify: `internal/ui/server_test.go`
- Modify: `internal/console/state.go`
- Modify: `internal/console/state_test.go`
- Modify: `internal/tray/tray.go`
- Modify: `internal/tray/tray_windows.go`
- Modify: `internal/tray/tray_test.go`
- Modify: `internal/ui/web/src/stepConfig.ts`
- Modify: `internal/ui/web/src/composables/useOnboarding.ts`
- Modify: `internal/ui/web/src/components/Dashboard.vue`
- Modify: `internal/ui/web/src/components/SuccessBanner.vue`
- Modify: `internal/ui/web/src/__tests__/Dashboard.spec.ts`
- Modify: `internal/ui/web/src/__tests__/SuccessBanner.spec.ts`
- Modify: matching onboarding/config tests under `internal/ui/web/src/__tests__/`

- [ ] **Step 1: Add failing backend propagation tests**

For launcher and open-folder, inject an opener/launch dependency returning a launch failure. Assert:

- the error reaches the caller with ChatGPT/Codex and repair guidance;
- no `opened ...` success is returned/printed;
- onboarding/console shutdown is not called on failure;
- successful launch still shuts down only after confirmation;
- onboarding `/api/launch`, completed-console `/api/console/open-frontend`, persistent state, and
  tray-visible errors never contain injected token, user path, PowerShell output, or registry text;
  their safe `Error()` may retain the original cause only through `Unwrap`/`errors.Is`.

Table-test Codex user-visible launch classification exactly: `ErrNotFound` includes Store Product ID
`9NT1R1C2HH7J`; `ErrSchemeMissing` says the scheme is absent; `ErrSchemeTargetInvalid` says the
registered target is invalid/untrusted; `ErrLaunchFailed` says the desktop app itself cannot start;
an unknown cause receives a generic bounded message. Every category must suppress raw details,
remain at most 256 runes, and preserve sentinel/cause identity through `errors.Is`.

Keep the existing Dashboard overlapping-refresh regression and strengthen the error fixture to an
actionable launch failure. Add deferred-promise tests for both success and failure where a slow
refresh starts while frontend launch is in progress and resolves after launch settles: it must not
restore stale `frontend_error`. A refresh started after launch settles may update the persisted
error. Implement this with one monotonically increasing frontend-action generation captured by
state loads/refreshes and advanced when a launch starts and settles.

- [ ] **Step 2: Add failing copy tests**

Update expectations for `ChatGPT / Codex` short names and
`ChatGPT 桌面应用（含 Codex）` setup text. Cover frontend state, launcher/tray labels, onboarding
steps/defaults, Dashboard model text, SuccessBanner, agentctl, and open-folder success output.

- [ ] **Step 3: Run focused Go and UI tests and observe failure**

```bash
go test -count=1 ./cmd/launcher ./cmd/open-folder ./cmd/agentctl ./internal/ui ./internal/console ./internal/tray
(cd internal/ui/web && npm test -- --run)
```

- [ ] **Step 4: Update copy and preserve error flow**

Use `codexdesktop.ShortDisplayName`/`LongDisplayName` in Go packages where imports do not create a
cycle; otherwise use the exact same constants locally. Do not rename persisted modes, API fields,
completed tokens, config paths, or technical CLI flags.

Existing backend error responses and Dashboard persistent error region should be reused. Do not add a
client-side generic message that discards the typed backend diagnosis. Finalize marks state complete
and starts the completed console in `--background` mode, but does not shut down onboarding;
`ActionStep` then calls `/api/launch`, and only a confirmed successful launch shuts onboarding
down. Persist a bounded, mode-specific sanitized frontend error for automatic, tray, and manual
completed-console launches; serialize launch calls and clear the persisted error only after success.

- [ ] **Step 5: Re-run focused tests**

```bash
go test -count=1 ./cmd/launcher ./cmd/open-folder ./cmd/agentctl ./internal/ui ./internal/console ./internal/tray
(cd internal/ui/web && npm test -- --run)
```

Expected: PASS.

## Task 6: Bind the bundled ChatGPT bootstrapper to Store metadata

**Files:**

- Modify: `scripts/windows-package-common.sh`
- Modify: `scripts/package-windows-zip.sh`
- Modify: `packaging/windows/installer.iss`
- Modify: `packaging/windows/install.ps1`
- Modify: `packaging/windows/ensure-codex-desktop.ps1`
- Modify: `internal/vscode/install_test.go`
- Modify: `internal/updater/codex_desktop_signature_source_test.go`

- [ ] **Step 1: Add failing packaging-source tests**

Require:

- cache path `cache/chatgpt-desktop/9NT1R1C2HH7J/ChatGPT Installer.exe`;
- portable/Inno destination `chatgpt-desktop-installer.exe`;
- adjacent `chatgpt-desktop-installer.manifest.json` in required files and both payload formats;
- manifest generation after download verification with exact Product ID, exact official source URL,
  SHA-256, and size;
- temporary manifest creation and atomic replacement after the installer cache is replaced;
- runtime manifest Product ID/URL/size/hash checks before existing MZ/AuthentiCode/signer/chain checks;
- one shared verifier used by build and runtime, requiring `Get-AuthenticodeSignature=Valid`, exact
  Microsoft Corporation O/C/CN attributes via `CertGetNameStringW`, code-signing EKU, online
  revocation with `ExcludeRoot` and bounded retrieval, and signer-root SHA-256 pin
  `847DF6A78497943F27FC72EB93F9A637320A02B561D0A91B09E87A7807ED7C61`;
- a signer-time exception only when the non-empty chain status contains exclusively `NotTimeValid`
  and a valid Authenticode timestamp exists; require timestamp EKU, online chain, and the verified
  timestamp-root SHA-256 pin
  `DF545BF919A2439C36983B54CDFC903DFA4F37D3996D8D84B4C31EEC6F3C163E`. Compute root SHA-256
  from `RawData` with PowerShell 5.1-compatible APIs;
- local verification failure falls back to exact winget and never executes the invalid file.

Preserve tests that a failed refresh does not delete a previously verified shared cache.

- [ ] **Step 2: Run focused packaging tests and observe failure**

```bash
go test -count=1 ./internal/vscode ./internal/updater \
  -run 'TestWindowsPackage|TestEnsureCodexDesktop|TestCodexDesktopSignature'
```

- [ ] **Step 3: Generate and distribute the bootstrapper manifest**

Rename packaging variables to ChatGPT semantics. Download to a unique temporary file, perform size,
MZ, and optional build-host Authenticode checks, then atomically publish the installer. Compute the
published file's SHA-256/size and use `jq -n` to write a temporary manifest with exact Product ID and
URL before atomic publication.

Generate installer and manifest completely in a staging directory before publishing either. Preserve
the previous verified pair, publish the staged pair with rollback around both same-filesystem renames,
and re-run pair verification after publication. A `jq`/manifest/hash/rename/post-publish verification
failure must restore or leave the previous pair and fail the build; it must never leave a new
installer publicly paired with an old manifest. Add a focused source/helper test for manifest
generation failure and rollback behavior. Extract staging/publication into a shell helper that can be
executed against a temporary directory; at least one test must exercise real files and forced failure,
not only inspect source strings.

Add the shared detector script, installer, and manifest to required files, portable payloads, Inno
`[Files]`, and portable installer required-input checks.

- [ ] **Step 4: Verify the manifest at install time**

Before `Start-Process`, parse with `ConvertFrom-Json`, compare exact Product ID/source URL, compare
integer size and case-insensitive SHA-256, then run the same shared hardened Authenticode verifier
used during packaging. Use `-LiteralPath` for all payload reads. An empty chain-status collection
must never qualify for the timestamp-only exception.

- [ ] **Step 5: Re-run focused tests and shell syntax checks**

```bash
go test -count=1 ./internal/vscode ./internal/updater \
  -run 'TestWindowsPackage|TestEnsureCodexDesktop|TestCodexDesktopSignature'
bash -n scripts/windows-package-common.sh scripts/package-windows.sh scripts/package-windows-zip.sh
```

Expected: PASS.

## Task 7: Remove broad desktop-process termination and update Windows E2E detection

**Files:**

- Modify: `packaging/windows/install.ps1`
- Modify: `packaging/windows/installer.iss`
- Modify: `internal/vscode/install_test.go`
- Modify: `test/e2e/windows/e2e_test.go`

- [ ] **Step 1: Write failing process-scope and E2E tests**

Require install.ps1 and generated Inno stop script to stop only agentserver install-root processes and
the explicitly managed local `codex.exe`. Reject both desktop PFNs, `OpenAI.Codex_` path-prefix
matching, `ChatGPT.exe`, and desktop-package command-line matching.

Update E2E detection assertions to accept either exact new/legacy PFN and require `codex://`, while
rejecting fuzzy package patterns.

- [ ] **Step 2: Run focused tests and observe failure**

```bash
go test -count=1 ./internal/vscode ./test/e2e/windows \
  -run 'Test.*StopRunning|TestCodexDesktopInstalledPowerShell'
```

- [ ] **Step 3: Narrow process termination and update E2E helper**

Delete desktop-package process predicates from both installers. Keep path-canonicalized agentserver
process filtering and timeout behavior unchanged. Make E2E use an exact-PFN array/`-contains` check
and an explicit scheme check.

- [ ] **Step 4: Re-run focused tests**

```bash
go test -count=1 ./internal/vscode ./test/e2e/windows \
  -run 'Test.*StopRunning|TestCodexDesktopInstalledPowerShell'
```

Expected: PASS.

## Task 8: Integration verification and final Codex code-review loop

**Files:**

- Modify only files needed to address integration failures or reviewer findings.
- Update: this plan's checkboxes/review record if the repository convention expects it.

- [x] **Step 1: Format and inspect**

```bash
gofmt -w <all changed Go files>
git diff --check
git status --short
```

Inspect the diff for raw registry-command execution, fuzzy package/process matching, old Product ID,
and accidental changes outside this worktree:

```bash
rg -n '9PLM9XGG6VKS|Name -like .*(Codex|ChatGPT)|PackageFullName -like|Invoke-Expression' \
  internal/codexdesktop packaging/windows scripts test/e2e/windows
```

Any remaining old ID must be an explicit negative test or historical document, not production code.

- [x] **Step 2: Run all verification commands**

```bash
go test -count=1 ./...
(cd internal/ui/web && npm test -- --run)
(make ui-build)
(cd extensions/agentserver-app && npm run compile)
GOOS=windows GOARCH=amd64 go build ./...
mkdir -p .tmp/windows-test-binaries
GOOS=windows GOARCH=amd64 go test -c -o .tmp/windows-test-binaries/ ./internal/codexdesktop ./internal/browser
go vet ./...
bash -n scripts/*.sh
go test -tags=e2e -count=1 ./test/e2e/windows \
  -run 'TestCodexDesktop|TestFindSetupExe|TestRemoteInstallerPath|TestPreUninstallCommand|TestWingetListCommand'
git diff --check
```

Record exact pass/fail counts. `pwsh` is absent on the current Linux host, so do not claim live
PowerShell or Windows behavior was executed.

Final fresh verification record:

- `go test -count=1 ./...`: PASS for every package; zero failures.
- UI Vitest: 9 files / 100 tests PASS.
- `make ui-build`: PASS, 1625 modules transformed; fresh embedded assets generated.
- VS Code extension TypeScript compile: PASS.
- Windows amd64 full build and two scoped Windows test-binary cross-compiles: PASS.
- scoped race tests: 10 packages PASS; `go vet ./...`, `bash -n scripts/*.sh`, selected
  `-tags=e2e` Windows source-contract tests, and `git diff --check`: PASS.
- No live PowerShell/AppX/COM/Authenticode execution was possible on this Linux host; the real
  Windows acceptance matrix remains a release prerequisite.

- [x] **Step 3: Send the complete diff to the same Codex reviewer**

Reuse reviewer thread `gpt-5.5 xhigh`. Provide the approved spec, this plan, `git diff`, test output,
and the explicit lack of live Windows validation. Request Critical/Important/Minor findings with
special focus on command execution, protocol hijack/TOCTOU, exact process identity, error propagation,
and bootstrapper integrity.

- [x] **Step 4: Fix and re-review until clean**

Fix every Critical/Important issue and re-run affected focused/full tests before re-review. Resolve
Minor issues too unless they are demonstrably out of scope and do not affect correctness or security.
Final acceptance requires reviewer `READY` with no unresolved code problem.

## Plan-stage review gate

Before Task 1 implementation begins, the same `gpt-5.5 xhigh` Codex reviewer must review this plan
against the approved design. No implementation starts until the reviewer reports no Critical or
Important issue. Minor implementation cautions are incorporated into the relevant task before code.

## Codex plan review record

### Round 1 — NOT READY

- **Important:** process confirmation was not scoped to the current Windows user/session. Task 4 now
  requires exact current SID and SessionId plus package-root containment; unreadable ownership fails
  closed.
- **Minor:** Task 2 now names UserChoice, `AssocQueryStringW`, AppUserModelID-as-locator, AppModel,
  and package manifest validation order.
- **Minor:** Task 6 now stages and rollback-publishes installer/manifest as a logical atomic pair.
- **Minor:** Task 5 now explicitly lists `SuccessBanner.vue` and its matching tests.

### Round 2 — READY

- No Critical or Important findings.
- Implementation caution recorded: `AssocQueryStringW` is corroboration only; exact package/AUMID/
  manifest binding remains authoritative.
- Implementation caution recorded: installer/manifest rollback receives a real temporary-directory
  helper test, not source-string assertions alone.

### Post-plan implementation hardening (final code gate passed)

- The implementation removed the registry/AppModel locator fallbacks described in the original
  review record. `QueryCurrentDefault(AL_EFFECTIVE)` plus documented `ASSOCSTR_APPID` is now the
  only association evidence, followed by exact AUMID/PFN/ApplicationId/manifest validation. The
  fixed-ProgID lookup must use `ASSOCF_INIT_FIXED_PROGID = 0x0800`; flags=0 is a blocking security
  defect.
- The implementation no longer performs a Shell association handoff. It activates the already
  verified AppUserModelID with `ActivateForProtocol` and then confirms exact package/SID/session
  process identity.
- Build-time and runtime installer checks now share one stronger Authenticode policy with exact
  publisher attributes/EKUs, online revocation, pinned Microsoft roots, and a narrow timestamp
  exception.
- User-facing launch and onboarding-install errors are sanitized at core/UI boundaries while
  retaining causes through Go unwrapping. The same `gpt-5.5 xhigh` reviewer assessed these
  refinements with the full diff and targeted follow-up rounds.
- Process confirmation resolves both package root and candidate image through Windows file handles
  and compares final paths; red-green final-path/reparse tests cover escape, sibling-prefix and
  handle-failure cases without lexical fallback.

### Security-hardening re-review

#### Round 1 — NOT READY

- **Important:** detector execution still named bare `powershell.exe`. Task 2 now creates the shared
  Windows-system helper first, resolves PowerShell 5.1 under absolute System32, rejects PATH/LookPath,
  and keeps resolution/launch failures as operational detector errors.
- **Minor:** Task 1/4 now explicitly reject empty, malformed, PFN-mismatched, or untrusted AUMIDs
  before activation.
- **Minor:** Task 8 now cross-compiles Windows test binaries for `internal/codexdesktop` and
  `internal/browser`, in addition to the production Windows build.

#### Round 2 — READY

- 无 Critical、Important 或 Minor 问题。
- reviewer 确认 absolute detector PowerShell、AUMID pre-activation validation、Windows test
  binary cross-compilation，以及 fixed-ProgID/final-path/root-pin/rollback/UI generation 等安全
  合同均有具体 TDD 步骤覆盖。

## Final code review record

All rounds used the same persistent Codex reviewer session with `gpt-5.5` and `xhigh` reasoning.

### Round 1 — NOT READY

- **Critical:** none.
- **Important:** fixed-ProgID APPID lookup failures escaped as operational detector errors; fixed by
  catching only that lookup and falling through to `scheme_target_invalid`.
- **Important:** the shared Authenticode verifier used `Get-AuthenticodeSignature -FilePath` for an
  attacker-influenced path; fixed with `-LiteralPath` and a regression test.
- **Minor:** the not-installed UI message omitted “含 Codex”; fixed through the shared exact long
  display-name constant.

### Round 2 — NOT READY

- All Round 1 findings were closed; no Critical or Minor findings remained.
- **Important:** onboarding installation SSE exposed raw winget/PowerShell diagnostics. Fixed with a
  mode-specific, bounded safe install error at the SSE boundary, retaining causes only through Go
  wrapping.
- The reviewer explicitly adjudicated bare `winget` App Execution Alias/PATH discovery as not a
  separate finding for this unprivileged per-user flow, given exact Store arguments and trusted
  post-install identity validation.

### Round 3 — READY

- Critical: none.
- Important: none.
- Minor: none.
- The targeted re-review confirmed the sole Round 2 finding was closed without new issues.
