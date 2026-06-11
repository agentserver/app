# Local Slave Start/Auth UX Fix Design

## Problem

Windows users see a command-line window when clicking "创建并启动" for a local slave. After the slave prints a device authentication URL, the dashboard only stores and displays the URL; the user must manually click "完成认证".

## Goals

- Starting `slave-agent.exe` from 星池指挥官 must not show a console window on Windows.
- When a slave startup emits an auth URL, 星池指挥官 must automatically open that URL in the default browser.
- The dashboard fallback link remains available for cases where the browser fails to open or was blocked.
- The behavior must cover both immediate auth URLs returned during startup and delayed auth URLs emitted after startup monitoring begins.

## Non-Goals

- Do not change the local slave creation UX beyond automatic browser opening.
- Do not change the `discovery.skills` list in this fix; bash/powershell skills are handled separately.
- Do not change loom `slave-agent.exe` behavior or its authentication protocol.

## Current Behavior

- `internal/slave/execRunner.Start` creates the slave process with `exec.Command(req.Exe, req.ConfigPath)` and calls `cmd.Start()` without hiding the window.
- `internal/slave.Manager.start` records `StartResult.AuthURL` but does not open it.
- `internal/slave.Manager.monitor` records delayed auth URLs through `recordAuthURL` but does not open them.

## Desired Behavior

- `execRunner.Start` must call `process.HideWindow(cmd)` before `cmd.Start()`.
- `ManagerDeps` must accept an optional `OpenAuthURL func(string)` dependency.
- `Manager.start` must call `OpenAuthURL` asynchronously when `StartResult.AuthURL` is non-empty.
- `Manager.monitor` must call `OpenAuthURL` asynchronously when a delayed auth URL is received.
- `cmd/launcher` must wire this dependency to `browser.Open`.

## Acceptance Criteria

- Unit tests prove that `execRunner.Start` hides the process before starting it.
- Unit tests prove immediate auth URLs invoke the opener.
- Unit tests prove delayed auth URLs invoke the opener and still update registry state to `auth_required`.
- `go test ./... -count=1` passes.
