# Codex Store Product Correction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Every production edit follows a focused red-green test cycle. Before final verification and Windows acceptance, send the diff to the same GPT-5.5/xhigh Codex reviewer that approved the design and plan; resolve every Critical or Important finding.

**Goal:** Make Windows codex_desktop install the live-confirmed Microsoft Store Codex product, recognize only manifest-proven codex AppX packages, and return a nonzero silent Inno exit code when a post-install operation fails.

**Architecture:** Keep the two exact PFNs as an allowlist, but read each installed candidate's AppX manifest before assigning any installed status. Only applications that literally declare the codex windows.protocol extension enter the existing effective-ProgID/AUMID/manifest binding. The same 9PLM9XGG6VKS identity flows through Go, PowerShell, Bash, Inno, portable packaging, and E2E source contracts. Inno records post-install failure state, stops remaining post-install steps, and exposes it through GetCustomSetupExitCode rather than relying on RaiseException.

**Tech Stack:** Go, Windows PowerShell 5.1-compatible scripts, Bash packaging, Inno Setup, Go source-contract tests, Windows 11 acceptance host.

**Approved design:** docs/superpowers/specs/2026-07-13-codex-store-product-correction-design.md

---

## Fixed security values and non-negotiable rules

~~~
Codex Store Product ID:          9PLM9XGG6VKS
Codex package family:            OpenAI.Codex_2p2nqsd0c76g0
Classic ChatGPT package family:  OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0
Codex bootstrapper URL:          https://get.microsoft.com/installer/download/9PLM9XGG6VKS?cid=website_cta_psi
Protocol literal:                codex
Inno post-install exit code:     1
~~~

Never use Store display names, executable names, package-name wildcards, registry commands, or chatgpt:// as evidence that an installed package can handle Codex. Preserve the existing literal-path Authenticode, Microsoft O/C/CN, code-signing EKU, online revocation, root-pin, timestamp, MZ, SHA-256, and byte-size checks. A manifest-read failure is operationally fatal and must not become not_installed.

### Task 1: Lock the revised typed detection contract with failing tests

**Files:**

- Modify: internal/codexdesktop/detect_test.go
- Modify: internal/codexdesktop/install_test.go
- Modify: internal/codexdesktop/winget.go
- Modify: internal/codexdesktop/winget_test.go
- Modify: internal/codexdesktop/launch.go
- Modify: internal/codexdesktop/launch_test.go
- Modify: internal/codexdesktop/process_snapshot.go
- Modify: internal/codexdesktop/detect.go
- Modify: internal/codexdesktop/detect_windows.ps1
- Modify: internal/ui/orchestrator.go
- Modify: internal/ui/orchestrator_test.go

- [ ] **Step 1: Add the stale-association and corrected-identity regression cases**

In TestDetectedFromPowerShellOutputStates add this case. It proves a stale scheme has no package metadata and remains eligible for actual Codex installation:

~~~
{
    name:       "not installed with stale scheme",
    json:       "{\"status\":\"not_installed\",\"installed\":false,\"scheme_registered\":true,\"scheme_target_valid\":false}",
    wantStatus: StatusNotInstalled,
    wantErr:    ErrNotFound,
},
~~~

Rename parser and ready-install fixtures to use ChatGPTClassicPackageFamily for Classic and CodexPackageFamily for the real Codex package. Make TestEnsureInstalledRunsWingetThenVerifies expect:

~~~
"install --id=9PLM9XGG6VKS --source=msstore --exact " +
    "--accept-package-agreements --accept-source-agreements --disable-interactivity"
~~~

In internal/ui/orchestrator_test.go, make the SafeFrontendLaunchError not-found expectation use
codexdesktop.CodexStoreProductID rather than a literal. Its required tokens must include that new
constant and must reject 9NT1R1C2HH7J. Keep SafeFrontendInstallError's not-found assertion on the
same renamed constant. In internal/codexdesktop/launch_test.go, extend the unavailable not-installed
case to require CodexStoreProductID in the safe preflight text and reject 9NT1R1C2HH7J. In
internal/codexdesktop/winget_test.go, require the exact new ID in WingetInstallArgs and generic
ClassifyWingetError output, then reject the retired ID. These tests must preserve the existing
sensitive-detail non-leak assertions.

Add TestWindowsDetectFiltersManifestCapablePackagesBeforeStatusClassification. It must require all of:

~~~
function Get-CodexProtocolCapablePackages
$capablePackages = @(Get-CodexProtocolCapablePackages -Packages $packages)
Find-CodexProtocolPackageByAppUserModelID -Packages $capablePackages
Get-DiagnosticChatGPTCodexPackage -Packages $capablePackages
return New-ChatGPTCodexDetection -Status 'not_installed'
~~~

Slice Get-ChatGPTCodexDetection and assert capable-package selection is after exact enumeration and before both mapping/diagnostic calls. Assert the no-capable branch has -SchemeRegistered $schemeRegistered but no -PackageFamilyName, -InstallLocation, or -AppUserModelID. Slice Get-CodexProtocolCapablePackages and reject catch { there, so Get-AppxPackageManifest errors stay operational.

- [ ] **Step 2: Run the focused tests and observe expected failure**

Run:

~~~
go test -count=1 ./internal/codexdesktop ./internal/ui -run 'TestDetectedFromPowerShellOutputStates|TestEnsureInstalledRunsWingetThenVerifies|TestWindowsDetectFiltersManifestCapablePackagesBeforeStatusClassification|TestWinget|TestClassifyWinget|TestLaunchWithOptionsRejectsUnavailableStatesBeforeOpening|TestSafeFrontend(Launch|Install)Error'
~~~

Expected: the revised winget, UI/launch safe-error, and manifest-capable source contracts fail against the old Store ID and detector control flow. The stale-association parser case may pass already; it remains regression coverage while the source contract proves missing behavior.

- [ ] **Step 3: Implement the minimal Go identity rename and strict validation continuity**

In internal/codexdesktop/detect.go replace the misleading constants with:

~~~
const (
    CodexStoreProductID         = "9PLM9XGG6VKS"
    CodexPackageFamily          = "OpenAI.Codex_2p2nqsd0c76g0"
    ChatGPTClassicPackageFamily = "OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0"
    ShortDisplayName            = "ChatGPT / Codex"
    LongDisplayName             = "ChatGPT 桌面应用（含 Codex）"
)
~~~

Define the trusted exact-PFN predicate from only those two renamed families. Keep StatusNotInstalled valid only with Installed false, no package metadata, and SchemeTargetValid false; SchemeRegistered may be true. Do not relax ready AUMID/PFN validation. Update winget.go, launch.go, and any other Codex preflight/error construction to use CodexStoreProductID. In SafeFrontendLaunchError, replace the literal retired product ID with codexdesktop.CodexStoreProductID. No safe user-visible Codex error may retain 9NT1R1C2HH7J.

- [ ] **Step 4: Implement manifest-capable filtering in the shared detector**

Retain Get-CodexProtocolApplications and add:

~~~
function Get-CodexProtocolCapablePackages {
    param([Parameter(Mandatory = $true)][object[]]$Packages)

    $capable = @()
    foreach ($package in $Packages) {
        if (@(Get-CodexProtocolApplications -Package $package).Count -gt 0) {
            $capable += $package
        }
    }
    return $capable
}
~~~

At the start of Get-ChatGPTCodexDetection, enumerate the two exact families, compute $capablePackages, then query the effective ProgID for scheme_registered. If no capable package exists, return only not_installed plus that boolean. Feed $capablePackages, not $packages, to unique AUMID mapping and diagnostic package selection. Do not catch manifest retrieval or XML exceptions; the embedded invocation has ErrorActionPreference Stop, so callers receive an operational error rather than a false installation decision.

- [ ] **Step 5: Re-run focused regression suite**

Run:

~~~
gofmt -w internal/codexdesktop/detect.go internal/codexdesktop/detect_test.go internal/codexdesktop/install_test.go internal/codexdesktop/winget.go internal/codexdesktop/winget_test.go internal/codexdesktop/launch.go internal/codexdesktop/launch_test.go internal/ui/orchestrator.go internal/ui/orchestrator_test.go
go test -count=1 ./internal/codexdesktop ./internal/ui -run 'TestDetectedFromPowerShellOutput|TestEnsureInstalled|TestWindowsDetect|TestWinget|TestClassifyWinget|TestLaunchWithOptionsRejectsUnavailableStatesBeforeOpening|TestSafeFrontend(Launch|Install)Error'
~~~

Expected: PASS. The source contract proves Classic is not treated as installed Codex unless its own manifest literally declares codex.

- [ ] **Step 6: Commit the focused detector change**

~~~
git add internal/codexdesktop/detect.go internal/codexdesktop/detect_windows.ps1 internal/codexdesktop/detect_test.go internal/codexdesktop/install_test.go internal/codexdesktop/winget.go internal/codexdesktop/winget_test.go internal/codexdesktop/launch.go internal/codexdesktop/launch_test.go internal/ui/orchestrator.go internal/ui/orchestrator_test.go
git commit -m "fix: require manifest-proven Codex desktop packages"
~~~

Task 2 starts only after this commit succeeds and git status --short is empty. The UI safe-error
files intentionally belong to this Task 1 commit because they consume the renamed Go constant; do
not defer or restage them in Task 2.

### Task 2: Propagate the exact Codex Store product through every installer path

**Files:**

- Modify: packaging/windows/ensure-codex-desktop.ps1
- Modify: packaging/windows/verify-chatgpt-desktop-installer.ps1
- Modify: scripts/windows-package-common.sh
- Modify: packaging/windows/installer.iss
- Modify: internal/vscode/install_test.go
- Modify: test/e2e/windows/e2e_test.go

- [ ] **Step 1: Add failing cross-layer source-contract expectations**

Update TestEnsureCodexDesktopScriptUsesBundledInstallerBeforeWingetFallback and TestWindowsPackageScriptsRefreshChatGPTDesktopInstaller to require 9PLM9XGG6VKS and the exact corrected URL. Each test must reject 9NT1R1C2HH7J in the relevant source.

Add table-driven TestCodexStoreProductIDSourceContract in internal/vscode/install_test.go for:

~~~
../../packaging/windows/ensure-codex-desktop.ps1
../../packaging/windows/verify-chatgpt-desktop-installer.ps1
../../scripts/windows-package-common.sh
../../packaging/windows/installer.iss
../../test/e2e/windows/e2e_test.go
~~~

Require 9PLM9XGG6VKS and reject 9NT1R1C2HH7J for every row. For the Bash and Inno rows also require cache/chatgpt-desktop/9PLM9XGG6VKS. Update TestWingetListCommandUsesExactStoreProductID to assert --id=9PLM9XGG6VKS and reject the old ID.

- [ ] **Step 2: Run source-contract tests and observe failure**

Run:

~~~
go test -count=1 ./internal/vscode ./test/e2e/windows -run 'TestEnsureCodexDesktopScriptUsesBundledInstallerBeforeWingetFallback|TestWindowsPackageScriptsRefreshChatGPTDesktopInstaller|TestCodexStoreProductIDSourceContract|TestWingetListCommandUsesExactStoreProductID'
~~~

Expected: FAIL because current runtime and packaging layers still select the Classic product.

- [ ] **Step 3: Change every source selector as one atomic identity update**

Apply the new ID and URL in these locations:

~~~
packaging/windows/ensure-codex-desktop.ps1
  expected product ID/source URL, winget --id, progress/error messages
packaging/windows/verify-chatgpt-desktop-installer.ps1
  root-pin evidence comment naming the current official product ID
scripts/windows-package-common.sh
  CHATGPT_DESKTOP_PRODUCT_ID and derived URL/cache/manifest paths
packaging/windows/installer.iss
  both [Files] cache source paths
test/e2e/windows/e2e_test.go
  wingetListCommand()
~~~

Leave payload file names unchanged. Retain paired manifest product_id/source_url equality checks, shared literal-path signature verification, fallback-to-winget behavior, BOM, and strict install flags.

- [ ] **Step 4: Re-run changed-file tests and scan for the retired source selector**

Run:

~~~
go test -count=1 ./internal/vscode ./test/e2e/windows -run 'TestEnsureCodexDesktop|TestWindowsPackageScriptsRefreshChatGPTDesktopInstaller|TestCodexStoreProductIDSourceContract|TestWingetListCommandUsesExactStoreProductID'
if rg -n '9NT1R1C2HH7J' internal/codexdesktop internal/ui packaging/windows scripts/windows-package-common.sh test/e2e/windows; then
  exit 1
else
  result_status=$?
  if [ "$result_status" -ne 1 ]; then exit "$result_status"; fi
fi
~~~

Expected: PASS and no matching source selectors. There is no old-ID allowlist in these scanned
runtime, packaging, UI, test, or E2E paths: every occurrence of 9NT1R1C2HH7J there, including a
comment or expected string, must be removed or rewritten to the corrected ID. Historical evidence
belongs only in the dated design documents, which this source scan intentionally does not include.

- [ ] **Step 5: Commit unified identity propagation**

~~~
git add packaging/windows/ensure-codex-desktop.ps1 packaging/windows/verify-chatgpt-desktop-installer.ps1 scripts/windows-package-common.sh packaging/windows/installer.iss internal/vscode/install_test.go test/e2e/windows/e2e_test.go internal/codexdesktop
git commit -m "fix: install the official Codex Store product"
~~~

### Task 3: Make silent post-install failure observable and halt remaining Inno actions

**Files:**

- Modify: internal/vscode/install_test.go
- Modify: packaging/windows/installer.iss

- [ ] **Step 1: Add the failing Inno exit-contract source test**

Add TestWindowsInnoPostInstallFailureUsesCustomExitCode. Read installer.iss and require:

~~~
PostInstallFailed: Boolean;
PostInstallFailureMessage: String;
procedure RecordPostInstallFailure(Message: String; LogPath: String);
function GetCustomSetupExitCode(): Integer;
Result := 1;
function RunEstimatedPowerShellStep(StepID: String; StatusText: String; ScriptName: String; ScriptArgs: String; EstimateSeconds: Integer): Boolean;
~~~

Restrict assertions to [Code]/CurStepChanged: every post-install execution has an early Exit on a false result; machine-name-write/no-mode branches use RecordPostInstallFailure rather than RaiseException; Log(FullMessage) exists; one MsgBox is guarded by not WizardSilent; and SetErrorFlag(True) is not the sole exit-code mechanism. Require each of these four branches to pass LogPath to the helper rather than losing its evidence:

~~~
RecordPostInstallFailure('无法准备安装步骤：' + StatusText, LogPath)
RecordPostInstallFailure('无法启动安装步骤：' + StatusText, LogPath)
RecordPostInstallFailure('无法读取安装步骤结果：' + StatusText, LogPath)
RecordPostInstallFailure(StatusText + ' 失败：' + ResultText, LogPath)
~~~

The test must also require that the helper appends the nonempty LogPath to FullMessage before setting PostInstallFailureMessage, writing the log, and optionally showing the message. Its RunEstimatedPowerShellStep slice must contain no RaiseException.

- [ ] **Step 2: Run focused test and observe failure**

Run:

~~~
go test -count=1 ./internal/vscode -run TestWindowsInnoPostInstallFailureUsesCustomExitCode
~~~

Expected: FAIL because post-install errors currently RaiseException and a silent installer can return zero.

- [ ] **Step 3: Replace exception-driven post-install control flow with recorded failure state**

In installer.iss add global values plus:

~~~
procedure RecordPostInstallFailure(Message: String; LogPath: String);
var
  FullMessage: String;
begin
  FullMessage := Message;
  if LogPath <> '' then begin
    FullMessage := FullMessage + '。日志：' + LogPath;
  end;
  if not PostInstallFailed then begin
    PostInstallFailureMessage := FullMessage;
    Log(FullMessage);
    if not WizardSilent then begin
      MsgBox(FullMessage, mbError, MB_OK);
    end;
  end;
  PostInstallFailed := True;
end;

function GetCustomSetupExitCode(): Integer;
begin
  Result := 0;
  if PostInstallFailed then begin
    Result := 1;
  end;
end;
~~~

Change RunEstimatedPowerShellStep to return Boolean. Initialize Result false; replace its four RaiseException branches with RecordPostInstallFailure and Exit, passing the already computed LogPath in every branch; set Result true only after ResultText equals 0. In CurStepChanged, exit immediately after any false RunEstimatedPowerShellStep result. On a machine-name write failure or impossible no-mode branch, call RecordPostInstallFailure with an empty log path and exit. Never start later runtime, driver, mode, or frontend steps after a recorded failure.

Do not alter pre-install page validation or unrelated exception behavior. The success path remains unchanged and GetCustomSetupExitCode returns zero when no post-install failure was recorded.

- [ ] **Step 4: Verify source contract and compile Inno through real package target**

Run:

~~~
go test -count=1 ./internal/vscode -run 'TestWindowsInno.*|TestWindowsInnoInstallerFrontendInstallUsesEstimatedProgress'
make package
~~~

Expected: tests PASS; make package exits zero and produces packaging/windows/Output/agentserver-app-0.1.8-setup.exe, proving ISCC compiled GetCustomSetupExitCode.

- [ ] **Step 5: Commit Inno failure contract**

~~~
git add packaging/windows/installer.iss internal/vscode/install_test.go
git commit -m "fix: return nonzero for silent post-install failures"
~~~

### Task 4: Verify corrected package and perform authorized Windows acceptance

**Files:**

- Modify only if verification demonstrates a defect: files named in Tasks 1-3
- Evidence output: local test logs and Windows acceptance-machine job/log files

- [ ] **Step 1: Run complete local verification before final review**

Run:

~~~
go test -count=1 ./...
go vet ./...
go test -race ./internal/codexdesktop ./internal/vscode ./internal/updater
make ui-test
make ui-build
make cross-windows
make ext-build
(cd extensions/agentserver-app && npm test)
bash -n scripts/windows-package-common.sh scripts/package-windows.sh scripts/package-windows-zip.sh
git diff --check
if rg -n '9NT1R1C2HH7J' internal/codexdesktop internal/ui packaging/windows scripts/windows-package-common.sh test/e2e/windows; then
  exit 1
else
  result_status=$?
  if [ "$result_status" -ne 1 ]; then exit "$result_status"; fi
fi
~~~

Expected: all Go tests/vet/scoped race checks, Vue tests/build, Windows amd64 cross-build, VS Code extension compile/package/test, Bash syntax checks, and git diff --check exit zero. The final guarded scan exits zero only when it prints no source matches. The scan has no old-ID allowlist: any matching runtime, packaging, UI, test, or E2E source text must be rewritten; only dated design documents may retain historical evidence. If any command fails, reproduce the root cause, add/tighten a regression test first, and repeat the relevant red-green task before final review.

- [ ] **Step 2: Rebuild and capture bootstrapper evidence**

Run:

~~~
make package
sha256sum packaging/windows/Output/agentserver-app-0.1.8-setup.exe dist/cache/chatgpt-desktop/9PLM9XGG6VKS/'ChatGPT Installer.exe'
~~~

Expected: the real package target repeats the Windows cross-build and extension packaging, refreshes the corrected Store bootstrapper and manifest, compiles the Inno event handler, and prints both artifact hashes. Do not edit root pins if Windows verification rejects the official bootstrapper; return to the review flow with certificate evidence.

- [ ] **Step 3: Clean only authorized products on WIN-8650DR8KQKD**

In Administrator's interactive session, use the registered agentserver uninstaller only for {A1B2C3D4-E5F6-4789-ABCD-EF0123456789}_is1; remove the product root only after confirming no registered uninstall reference and no process uses it. Remove only these AppX package families:

~~~
OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0
OpenAI.Codex_2p2nqsd0c76g0
~~~

Before installing, query both exact PFNs and dot-source the detector. Require zero exact package matches and detector not_installed; preserve unrelated Store packages and user data.

- [ ] **Step 4: Verify bundled payload on Windows before running it**

Copy rebuilt setup, bootstrapper/manifest, and packaging/windows/verify-chatgpt-desktop-installer.ps1 to the host. Run verifier against literal bootstrapper path. Require MZ, paired manifest hash/size equality, Authenticode Valid, Microsoft Corporation O/C/CN, code-signing EKU, online revocation, existing root pins, and timestamp validation. Abort acceptance on any verification failure.

- [ ] **Step 5: Run a disposable negative silent-install acceptance test**

Do not add a production failure switch. On the packaging host create a disposable copy of the
reviewed worktree, then use apply_patch only in that copy to make a test-only payload with both of
these changes:

~~~
packaging/windows/ensure-codex-desktop.ps1
  Insert: throw 'forced post-install Codex desktop failure (acceptance only)'
  immediately after: $ErrorActionPreference = 'Stop'

packaging/windows/installer.iss
  Immediately after the codex-install RunEstimatedPowerShellStep call, insert:
  if not SaveStringToFile(ExpandConstant('{tmp}\agentserver-after-codex-install.marker'), 'ran', False) then begin
    RecordPostInstallFailure('无法写入 post-install 测试标记。', '');
    Exit;
  end;
~~~

Build the disposable copy with make package and transfer only its test EXE to the Windows host. The
test source directory and test EXE must never be committed, uploaded as a release asset, or used
for the positive acceptance run. After exact authorized cleanup, run this test EXE in Session 2 with
the same silent arguments and a separate log path. Require process exit exactly 1; require the Inno
log to contain the forced failure and the relevant agentserver-codex-install.log path; and require
the after-codex-install marker to be absent. This proves the custom exit status is observable under
/VERYSILENT /SUPPRESSMSGBOXES and no later post-install action ran. Clean the exact app/agentserver
test state again before the positive run.

- [ ] **Step 6: Install interactively and require complete ready contract**

Create Session-2 task with New-ScheduledTaskPrincipal -LogonType Interactive. Pass Inno arguments as one string:

~~~
$argumentLine =
  '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART ' +
  '/TASKS="desktopicon,codexdesktop" /LOG="' + $log + '"'
Start-Process -FilePath $setup -ArgumentList $argumentLine -Wait -PassThru
~~~

Require process exit 0 and no CurStepChanged raised an exception in the Inno log. Then require winget list --id 9PLM9XGG6VKS --source=msstore --exact; exact Codex AppX manifest literal codex declaration; shared detector ready with a matching non-empty AUMID; and direct codex://threads/new activation followed by a Session-2 process belonging to exact Codex PFN. Record package versions and SHA-256 values.

- [ ] **Step 7: Do not commit generated or sensitive acceptance artifacts**

Do not commit executables, cache payloads, logs, test credentials, scheduled-task XML, or uninstall traces. If source/plan correction was necessary, commit only reviewed text/code with a focused message.
