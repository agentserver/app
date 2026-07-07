# Remote Control Toggle Design

## Context

The completed commander dashboard already manages local slave agents and opens the user-facing frontend. The Loom driver daemon is configured during onboarding and completed launches, but users currently have no explicit dashboard control for enabling or disabling the driver daemon. Driver discovery also uses the fixed display name `星池指挥官`, which makes remote commander entries indistinguishable across machines.

## Goals

- Add a commander dashboard section labeled `远程控制`.
- Show the remote commander URL `https://loom.nj.cs.ac.cn:10062/commander` as a safe external link.
- Add an on/off control for the local driver daemon.
- Persist the user's driver daemon intent so disabling remote control survives dashboard restarts and driver reconfiguration.
- Register the driver with the installation machine's computer name instead of the fixed name `星池指挥官`.
- Keep the new control safe by requiring authenticated mutations, serializing daemon operations, redacting raw filesystem errors from the UI, and avoiding broad process termination.

## Non-Goals

- Do not change local slave management semantics.
- Do not change Loom server URLs or credentials.
- Do not add remote deletion or workspace management for the driver entry.
- Do not require a live Loom server for unit tests.

## Recommended Architecture

Add a small driver-control layer behind the existing completed console controller.

- `internal/console` exposes driver daemon state and toggle operations through `Controller`.
- `internal/ui` adds a protected console endpoint for reading and changing driver daemon state.
- `internal/loom` owns process-level start, stop, and status helpers for `driver-agent.exe serve-daemon --config <driver.yaml>`.
- `cmd/launcher` wires the completed console controller with driver paths, config paths, and a small persisted state file under the existing launcher state directory.
- The Vue dashboard renders a `远程控制` panel with the commander link and a switch/button tied to the new API.

This keeps daemon control with the same console security model as update, model switching, and slave actions, while keeping process logic out of the UI layer.

## Data Model

Add a persisted driver control state file, separate from `driver.yaml`, containing:

- `enabled`: whether commander should auto-start and keep the driver daemon available.
- `updated_at`: last state change timestamp for diagnostics.
- `last_error_code` and `last_error_message`: sanitized diagnostics safe to expose to the local dashboard.
- tracked process metadata for the most recently started MCP and daemon processes, if available: PID, full executable path, expected argument shape, and process creation time when the platform exposes it.

When the file is missing, default to `enabled: true` to preserve current behavior for existing installs. If disabled, completed launch paths should still refresh `driver.yaml` and Codex MCP config, but must not auto-start the driver daemon.

The state file must live under the existing per-user launcher state directory. On POSIX, write a same-directory temp file with mode `0o600`, then publish with rename/link semantics consistent with existing state helpers. On Windows, rely on the per-user state directory ACL instead of broadening access. Corrupt state files should not silently enable remote control; return a sanitized error and allow an explicit `POST { "enabled": false }` or `POST { "enabled": true }` to overwrite the corrupt file with a valid state.

## API Shape

Add `/api/console/driver-daemon`:

- `GET`: returns `{ "enabled": bool, "running": bool, "commander_url": "https://loom.nj.cs.ac.cn:10062/commander", "last_error_code": string, "last_error_message": string }`. It uses the same loopback-only completed console surface as existing console reads and must not add cross-origin access.
- `POST`: accepts `{ "enabled": bool }`, requires the existing console mutation token, serializes all driver-daemon mutations with a backend mutex, applies the requested operation, and returns the updated state.

Enable and disable persist intent differently:

- Enabling remote control starts the MCP server and daemon first, then persists `enabled: true` only after the start operation succeeds. A failed enable leaves the previous disabled intent intact and stores a sanitized `daemon_start_failed` diagnostic.
- Disabling remote control persists `enabled: false` before attempting to stop processes. A failed stop keeps the disabled intent so the next launcher start will not auto-start remote control, and stores a sanitized `daemon_stop_failed` diagnostic.

Errors use the existing JSON error shape for failed mutations. GET responses must never expose raw `err.Error()` strings, absolute install paths, token paths, command lines, or credential material. Missing driver binary or config returns `running: false` and a sanitized `driver_unavailable` message.

## UI Behavior

Add a panel below the connection summary and before model/update controls:

- Heading: `远程控制`.
- Link text shows `https://loom.nj.cs.ac.cn:10062/commander`, sourced from a single backend constant with that value as the default.
- The link renders with `target="_blank"` and `rel="noopener noreferrer"`. If a backend open-url action is added later, it must reuse the existing trusted console mutation protection rather than allowing arbitrary URL input.
- Control text reflects current state:
  - Enabled and running: remote control is open.
  - Enabled but not running: starting failed or daemon unavailable.
  - Disabled: remote control is closed locally.
- The toggle is disabled while a request is in flight and duplicate clicks are ignored.
- Errors appear in the dashboard's existing error alert list.

## Driver Display Name

Driver config generation must use the machine identity already initialized by the launcher:

1. Load or ensure `paths.MachineFile` through `slave.MachineStore`.
2. Use `Machine.ComputerName` for `loom.DriverConfig.DisplayName`.
3. Fall back to `COMPUTERNAME`, hostname, then `local-computer-<stable suffix>` only when the machine file is unavailable, after trimming and length-capping the value through the same normalization path used for machine identity. The stable suffix should come from the install or machine identity when available so fallback names are still distinguishable.

Use a machine-specific description such as `<computer name> 本地协作驱动。` so commander entries remain distinguishable in both name and description.

## Daemon Process Semantics

`internal/loom` should continue deduplicating starts by executable and config path. New status and stop helpers should use the same key format to avoid divergent process tracking.

Stopping remote control should:

- Stop the tracked background MCP server and daemon processes for the same config where possible.
- Verify any persisted PID still belongs to the expected full `driver-agent.exe` executable path before terminating it.
- Never kill by process name alone; matching must be scoped to the exact executable path and the exact `serve-mcp` or `serve-daemon --config <driver.yaml>` command shape.
- Treat persisted PIDs without matching executable path, argument shape, and process creation time as stale diagnostics, not kill targets.
- Prefer in-process handles from `driverBackgroundProcesses` when available; persisted metadata is a restart fallback and must be revalidated immediately before termination.
- Treat already-stopped processes as success.
- Not delete `driver.yaml`, credentials, Codex MCP settings, or installed support prompts.

Starting remote control should:

- Start the MCP server first, then `serve-daemon`, matching current `StartDriverDaemon` behavior.
- Return success when an equivalent daemon is already tracked.
- Persist `enabled: true` only after start succeeds, or atomically with a successful start result, so a failed enable does not leave the UI claiming remote control is enabled.
- Persist the started process metadata needed for later status and stop operations.
- Execute start/stop under a controller-level mutex so concurrent requests from multiple tabs cannot interleave into an inconsistent state.
- Treat `updated_at` as diagnostic only. Stale-PID decisions must rely on OS-reported process creation time compared with persisted process creation time, not wall-clock deltas.

## Error Handling

- Missing driver binary/config on `GET` is not fatal; report unavailable/not running using sanitized diagnostics.
- Missing driver binary/config on enable returns a clear sanitized error and leaves the persisted enabled flag unchanged if start cannot proceed.
- Stop failures are surfaced in the UI using sanitized diagnostics, but the disabled intent remains persisted.
- API mutation endpoints keep the existing console token protection.

## Test Plan

- Go unit tests for the driver control store defaults, persistence, and invalid JSON handling.
- Go unit tests for console controller driver state, enable, disable, missing dependency errors, and start/stop callback calls.
- Go unit tests that stop failures still persist disabled intent and prevent later auto-start.
- Go unit tests that concurrent enable/disable calls are serialized into a coherent final state.
- Go unit tests that process termination refuses stale PID metadata, non-matching executable paths, and non-matching command shapes.
- Go unit tests that GET diagnostics do not leak install directories, config paths, command lines, tokens, or raw OS errors.
- Go unit tests for state file atomic writes, restrictive permissions on POSIX, and per-user state directory placement on Windows paths.
- Go unit tests that corrupt state fails closed but an explicit POST rewrites it.
- Go HTTP tests for `GET /api/console/driver-daemon`, protected `POST`, and response shape.
- Go regression tests that completed driver config uses the machine computer name, not `星池指挥官`.
- Vue API tests for `getConsoleDriverDaemon` and `setConsoleDriverDaemon`.
- Vue dashboard tests for the `远程控制` panel, commander link, `rel="noopener noreferrer"`, toggle calls, duplicate-click guard, and sanitized error display.
- Targeted package tests first, then broader Go and frontend test suites before completion.
