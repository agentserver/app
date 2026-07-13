# Codex Store Product Correction Design

**Date:** 2026-07-13
**Status:** Draft — requires same-session Codex review
**Issue:** [agentserver/app#23](https://github.com/agentserver/app/issues/23)
**Scope:** Correct the Windows `codex_desktop` Store bootstrapper after real Windows acceptance exposed that the previous Store product is ChatGPT Classic, and make silent Inno failures observable to automation.

## Evidence from the Windows acceptance machine

The full `0.1.8` installer was built from PR #24 and tested on `WIN-8650DR8KQKD`
(Windows 11 build 26100) after a clean removal of agentserver and the prior Codex package.

The bundled bootstrapper for Store Product ID `9NT1R1C2HH7J` passed the existing full integrity
policy on Windows: manifest hash and size matched, Authenticode status was `Valid`, the signing
identity was Microsoft Corporation, and the online chain/revocation/root-pin checks passed.
The bootstrapper then installed this exact package:

```text
Store ID:       9NT1R1C2HH7J
Store name:     ChatGPT Classic
PFN:            OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0
Version:        1.2026.190.0
Application ID: ChatGPT
Executable:     app\ChatGPT Classic.exe
Protocols:      chatgpt
```

Its real AppX manifest declares no `codex` protocol. Windows returned effective ProgID `codex`,
but fixed-ProgID `ASSOCSTR_APPID` lookup returned `0x80070002`; the shared detector consequently
returned `scheme_target_invalid`. This is a correct fail-closed result, not a detector defect.

Independent live evidence identifies `9PLM9XGG6VKS` as the current official agentic ChatGPT/Codex
product: the Microsoft Store page title is **ChatGPT**, `winget show --id 9PLM9XGG6VKS --source
msstore --exact` describes the Codex agentic-development application and lists
`https://openai.com/codex/` as publisher URL, and
`https://get.microsoft.com/installer/download/9PLM9XGG6VKS?cid=website_cta_psi` returns the
official bootstrapper. Before the clean test, that product was installed as
`OpenAI.Codex_26.707.3748.0_x64__2p2nqsd0c76g0`, had a valid `codex://` association, and was
reported `ready` by the shared detector.

Therefore `9NT1R1C2HH7J` must not be used as a Codex installer source. Its package family remains
an exact, non-fuzzy potential package identity only so that a future official version which really
declares `codex` can be recognized; the product is not trusted as Codex solely because its PFN is
known.

## Goals

- Use the live-confirmed Microsoft Store Product ID `9PLM9XGG6VKS` for every Codex bootstrapper,
  manifest, bundled payload, winget install and E2E source contract.
- Accept an installed package as a Codex candidate only when its exact allowed PFN and its own AppX
  manifest declare the literal `codex` `windows.protocol` contract.
- Preserve the existing fail-closed association binding: effective ProgID, fixed-ProgID AUMID,
  exact PFN/ApplicationId and manifest protocol declaration must still uniquely agree before
  detection returns `ready`.
- Make an Inno post-install step failure set a nonzero setup exit status even with
  `/VERYSILENT /SUPPRESSMSGBOXES`, while retaining the per-step log path in the error.
- Rebuild and prove the corrected installer on the same Windows test machine after removing the
  failed Classic installation and the prior agentserver install.

## Non-goals

- Do not reinterpret `chatgpt://` as `codex://`, synthesize a Codex handler, or execute a handler
  command from the registry.
- Do not trust ChatGPT Classic merely because it is signed by OpenAI or has the exact
  `OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0` PFN.
- Do not remove a user's Classic package automatically. The test-machine cleanup is an explicitly
  authorized acceptance action; production code only installs the required Codex product.
- Do not weaken Authenticode, exact publisher, online revocation, root-pin, manifest hash/size, or
  process-identity checks.
- Do not change frontend mode names, `codex://threads/new`, or unrelated installers.

## Design

### 1. Canonical product identities

Rename the installation constant to make its purpose unambiguous:

```text
CodexStoreProductID             = 9PLM9XGG6VKS
CodexPackageFamily              = OpenAI.Codex_2p2nqsd0c76g0
ChatGPTClassicPackageFamily     = OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0
Codex bootstrapper source URL   = https://get.microsoft.com/installer/download/9PLM9XGG6VKS?cid=website_cta_psi
```

`ChatGPTClassicPackageFamily` remains in the detector's exact allowlist only as a future-compatible
candidate identity. The code must not call it a ready Codex installation without its own manifest
proving a `codex` protocol declaration. User-facing product names remain `ChatGPT / Codex` and
`ChatGPT 桌面应用（含 Codex）`; they describe the supported Codex experience, not the localized
Store listing.

### 2. Manifest-capable candidate selection

The detector keeps the current exact package enumeration and Shell association query. Before
classifying a package as installed for `codex_desktop`, it obtains each exact candidate's manifest
using `Get-AppxPackageManifest` and obtains only applications that declare a literal `codex`
`windows.protocol` extension.

```text
exact PFN installed
  -> manifest readable and declares codex?
      -> no: it is not a Codex-capable installed package
      -> yes: it remains a candidate
  -> no capable candidates: StatusNotInstalled
  -> capable candidates: retain the existing effective ProgID -> fixed AUMID ->
     exact PFN/ApplicationId/manifest unique binding
```

If manifest inspection of an exact installed candidate fails, the detector reports an operational
error rather than treating the package as absent. This prevents an unreadable or corrupted package
from causing an unsafe automatic installation decision. A readable Classic manifest with no
`codex` contract is not corruption and results in `not_installed` (with no package metadata), even
if a stale `codex` association remains. This lets `EnsureInstalled` install the actual Codex
product rather than incorrectly instructing a Classic-only user to repair an unrelated protocol.

When one or more Codex-capable candidates exist, the current status rules are unchanged: missing
effective association is `scheme_missing`; a non-unique, absent, malformed, or mismatched AUMID /
ApplicationId / manifest binding is `scheme_target_invalid`; only a unique three-way binding is
`ready`.

### 3. Bootstrapper and manifest binding

All source, cache and runtime expectations change together to `9PLM9XGG6VKS`:

- Go winget install arguments use `install --id=9PLM9XGG6VKS --source=msstore --exact`.
- The Windows packaging cache moves to `cache/chatgpt-desktop/9PLM9XGG6VKS/ChatGPT Installer.exe`.
- The generated manifest embeds the exact corrected Product ID and official URL, plus fresh
  SHA-256 and byte size.
- Inno's `[Files]` entries read the corrected cache path; portable and required-file arrays use the
  same pair.
- `ensure-codex-desktop.ps1` validates the corrected Product ID and URL before its existing MZ,
  hash/size, literal-path Authenticode and certificate-chain checks, then uses the corrected ID for
  winget fallback.

The new bootstrapper must be re-verified on Windows before it is installed: `Valid` Authenticode,
Microsoft Corporation O/C/CN, code-signing EKU, online revocation, existing root-pin policy and
timestamp rules are mandatory. If an official replacement changes a pinned chain, it must be
reviewed as a separate evidence-backed change; this correction does not silently add root pins.

### 4. Silent Inno exit contract

`RaiseException` alone is not a reliable silent-setup exit-status mechanism: the observed
`/VERYSILENT /SUPPRESSMSGBOXES` run logged `CurStepChanged raised an exception` and still returned
zero. The setup must instead use Inno's documented `GetCustomSetupExitCode` event, which replaces a
would-be zero exit code after setup completes.

The installer keeps global `PostInstallFailed` and `PostInstallFailureMessage` state. Every
post-file-copy failure path in `RunEstimatedPowerShellStep` and direct `CurStepChanged` validation
(including failure to save machine-name input) must record the localized message and the relevant
per-step log path, set `PostInstallFailed`, and return control without starting any subsequent
post-install action. Interactive runs may surface the recorded localized error, but silent runs
must not rely on a modal message or an exception for process failure. `GetCustomSetupExitCode`
returns one stable nonzero setup-specific exit code when `PostInstallFailed` is true and zero
otherwise. This function must be compiled into the packaged installer. Consequently a silent
installer process returns nonzero if `ensure-codex-desktop.ps1` cannot reach `ready`; a deployment
wrapper must check the installer exit code rather than only observing that agentserver files were
copied.

### 5. Tests and acceptance

Automated coverage must prove:

- exact Go winget arguments and safe error text contain `9PLM9XGG6VKS`, not `9NT1R1C2HH7J`;
- all packaging, Inno, runtime and E2E source contracts share the corrected product ID and URL;
- the detector's `not_installed` JSON contract permits a stale registered scheme but no package
  metadata, and source structure derives Codex-capable candidates from manifest declarations before
  status classification;
- a readable exact Classic package with no `codex` contract cannot become `ready`;
- the Inno source records post-install failures, stops subsequent post-install actions and declares
  `GetCustomSetupExitCode` with a stable nonzero result for the recorded-failure state; the
  `make package` Inno compilation path compiles that event function;
- Go, UI, extension compile/test, Windows builds, source-contract tests, race/vet/shell checks and
  `git diff --check` pass.

On `WIN-8650DR8KQKD`, acceptance must:

1. remove only agentserver's registered uninstaller/root and the exact Classic and Codex PFNs;
2. confirm no exact package remains and detector is `not_installed`;
3. run the corrected installer in Administrator's interactive session and require a zero process
   exit code plus no `CurStepChanged raised an exception` in the Inno log;
4. verify the corrected official bootstrapper's manifest/hash/signature chain on Windows;
5. verify `winget list --id 9PLM9XGG6VKS --source=msstore --exact`, an exact Codex-capable AppX
   manifest, detector `ready` with a nonempty matching AUMID, and direct `codex://threads/new`
   activation followed by a process snapshot belonging to the exact package.

## Security invariants

1. The Store Product ID is an exact, live-validated source selector; it never by itself makes an
   installed package trusted for Codex.
2. Package recognition remains exact-PFN-only and manifest-capability-gated; no display-name,
   executable-name, Store-name, path-prefix or registry-command matching is permitted.
3. Shell association remains bound through `QueryCurrentDefault`, fixed-ProgID `ASSOCSTR_APPID`,
   exact PFN/ApplicationId and manifest protocol declaration; stale associations do not pass.
4. Payload integrity remains a paired product-ID/URL/hash/size manifest plus literal-path
   Authenticode and certificate-chain policy at install time.
5. A silent installation failure is observable to callers through a nonzero exit status; it cannot
   be reported as success merely because non-frontend files were installed.

## Review gate

This correction is a post-acceptance amendment to the reviewed 2026-07-10 design. The same
persistent GPT-5.5/xhigh Codex reviewer must review this spec, its implementation plan and the
final diff. Any Critical or Important finding blocks implementation; Minor security/correctness
findings are resolved before the rebuilt installer is accepted.
