# Codex Direct Activation Contract Fallback Design

## Context and evidence

The official Microsoft Store product `9PLM9XGG6VKS` installs the exact package
family `OpenAI.Codex_2p2nqsd0c76g0`. Its manifest declares the literal `codex`
protocol and the shared detector returns `ready` with the matching AUMID.

On the Windows 11 acceptance host, invoking the installed product's current
`codexdesktop.Launch` implementation in the logged-in Session 2 failed at
`IApplicationActivationManager.ActivateForProtocol` with `HRESULT 0x80270254`:
"This app does not support the contract specified or is not installed." This
package-level contract failure is distinct from missing installation.

In the same user session, `IApplicationActivationManager.ActivateApplication`
with that same verified AUMID and the generated `codex://threads/new` URL
started a process whose executable path was under the exact Codex package
install location. The process snapshot's existing owner, session, PFN and
canonical install-root validation all passed.

## Goal

Allow the official current Store package to launch from `/api/launch` without
weakening the existing protocol-association or process-identity security
checks.

## Non-goals

- Do not treat an installed package as trusted merely because it has an
  OpenAI-like name or a `codex` registry key.
- Do not call ShellExecute, `Start-Process`, `browser.Open`, `rundll32`, or
  any command-line protocol handler.
- Do not broaden fallback to arbitrary activation failures, change the deep
  link, or alter detection / manifest matching rules.
- Do not expose raw HRESULTs, command lines, install paths, or user SIDs in
  API responses.

## Design

`activateForProtocol` remains the first activation method. It continues to use
the detector-provided AUMID, builds an `IShellItemArray` from the generated
deep link, and invokes `ActivateForProtocol` directly.

If and only if that invocation returns `0x80270254`, the method invokes
`ActivateApplication` on the same `IApplicationActivationManager` instance.
It passes the same detector-validated AUMID and the internally generated deep
link as the argument string. This is an AUMID-directed package activation; it
does not re-resolve the `codex` URI through the current user association.

All other HRESULTs retain the current failure path. Whether either direct
activation call returns success, the existing launch flow still requires a
trusted current-user/current-session process snapshot within the verified
package root before declaring success.

## Security invariants

1. The detector must report `ready`, with an AUMID bound to a literal
   `codex` declaration in one of the two exact allowlisted PFNs, before either
   activation method runs.
2. Both activation methods target `det.AppUserModelID`; neither reads or
   executes the shell association command.
3. The fallback is restricted to the observed contract-incompatibility HRESULT
   (`0x80270254`) and cannot mask unrelated activation failures.
4. The successful activation is followed by the existing process snapshot
   verification: canonical package root, exact PFN, current owner SID and
   current session.
5. User-visible errors remain the existing safe repair guidance; raw Windows
   diagnostics stay internal to test tooling.

## Verification

- Unit-test the exact HRESULT predicate: `0x80270254` enables fallback and
  representative success / unrelated failure HRESULTs do not.
- Keep the source-level guard that prohibits shell activation and extend it to
  require `ActivateApplication` and the constrained fallback predicate.
- Run the Codex desktop package tests and full Go suite.
- Rebuild the Windows installer, install it on the Windows 11 acceptance host,
  call the real launch path in Session 2, and require an exact-PFN process
  after activation.
