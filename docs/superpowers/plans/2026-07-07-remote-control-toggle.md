# Remote Control Toggle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task in the current Codex session. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a secure `远程控制` commander panel that can enable/disable the local Loom driver daemon and register the driver by installation computer name.

**Architecture:** Add a small driver-daemon control path through `internal/console`, `internal/ui`, `cmd/launcher`, `internal/loom`, and the Vue dashboard. Console owns persisted user intent and sanitized API state; Loom owns exact process start/stop/status; launcher wires concrete paths and avoids auto-start when disabled.

**Tech Stack:** Go HTTP handlers and unit tests, Vue 3 + Vitest dashboard tests, existing `internal/loom`, `internal/console`, `internal/slave`, and `cmd/launcher` patterns.

---

## File Map

- Create `internal/console/driver_daemon.go`: driver state types, persisted store, sanitized error helpers, controller methods, mutex serialization.
- Create `internal/console/driver_daemon_test.go`: store and controller TDD coverage.
- Modify `internal/console/state.go`: extend `Deps` and `Controller` with driver daemon dependencies.
- Modify `internal/ui/console.go`: add console controller interface methods and noop implementations.
- Modify `internal/ui/server.go`: add `/api/console/driver-daemon` GET/POST handlers.
- Modify `internal/ui/server_test.go`: HTTP endpoint and token protection tests.
- Modify `internal/loom/config.go`: add status/stop helpers, process metadata, safe tracked-process termination.
- Create `internal/loom/process_match_unix.go` and `internal/loom/process_match_windows.go`: exact executable/argv validation for persisted PIDs.
- Modify `internal/loom/config_test.go`: start/stop/status and stale PID safety tests.
- Modify `cmd/launcher/main.go`: wire driver controller, use machine name for driver config, and honor disabled auto-start.
- Modify `cmd/launcher/main_test.go`: display-name, disabled auto-start, and driver state path tests.
- Modify `internal/ui/orchestrator_real.go` and tests so onboarding-time driver config also receives `MachineFile` and uses the same machine-name helper.
- Modify `internal/ui/web/src/api.ts`: driver daemon API types/functions.
- Modify `internal/ui/web/src/components/Dashboard.vue`: `远程控制` panel, link, toggle, sanitized errors.
- Modify `internal/ui/web/src/__tests__/api.spec.ts`: API tests.
- Modify `internal/ui/web/src/__tests__/Dashboard.spec.ts`: UI behavior tests.

## Task 1: Driver State Store

**Files:**
- Create: `internal/console/driver_daemon.go`
- Create: `internal/console/driver_daemon_test.go`

- [ ] **Step 1: Write failing default/persistence tests**

Add tests covering missing file defaults, atomic persistence, corrupt-state fail-closed behavior, explicit overwrite, and POSIX mode:

```go
func TestDriverDaemonStoreMissingDefaultsEnabled(t *testing.T) {
    store := NewDriverDaemonStore(filepath.Join(t.TempDir(), "driver-daemon.json"))
    st, err := store.Load()
    if err != nil {
        t.Fatalf("Load: %v", err)
    }
    if !st.Enabled {
        t.Fatalf("Enabled=false, want true for missing state")
    }
}

func TestDriverDaemonStoreCorruptStateFailsClosedAndCanBeOverwritten(t *testing.T) {
    path := filepath.Join(t.TempDir(), "driver-daemon.json")
    if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
        t.Fatal(err)
    }
    store := NewDriverDaemonStore(path)
    st, err := store.Load()
    if err == nil || st.Enabled {
        t.Fatalf("Load err=%v enabled=%v, want corrupt fail-closed", err, st.Enabled)
    }
    if err := store.Save(DriverDaemonPersistedState{Enabled: false}); err != nil {
        t.Fatalf("Save overwrite corrupt: %v", err)
    }
    st, err = store.Load()
    if err != nil || st.Enabled {
        t.Fatalf("Load after overwrite err=%v enabled=%v, want disabled", err, st.Enabled)
    }
}
```

- [ ] **Step 2: Run store tests red**

Run: `go test -count=1 ./internal/console -run 'TestDriverDaemonStore'`

Expected: compile failure because `NewDriverDaemonStore` and `DriverDaemonPersistedState` do not exist.

- [ ] **Step 3: Implement minimal store**

Implement:

```go
const DefaultCommanderURL = "https://loom.nj.cs.ac.cn:10062/commander"

type DriverDaemonPersistedState struct {
    Enabled          bool                  `json:"enabled"`
    UpdatedAt        string                `json:"updated_at,omitempty"`
    LastErrorCode    string                `json:"last_error_code,omitempty"`
    LastErrorMessage string                `json:"last_error_message,omitempty"`
    Processes        []DriverProcessRecord `json:"processes,omitempty"`
}

type DriverProcessRecord struct {
    PID       int      `json:"pid,omitempty"`
    Exe       string   `json:"exe,omitempty"`
    Args      []string `json:"args,omitempty"`
    CreatedAt string   `json:"created_at,omitempty"`
}
```

Use `os.CreateTemp`, `Chmod(0o600)`, `json.MarshalIndent`, and `os.Rename`, matching `internal/console/instance.go`.

- [ ] **Step 4: Run store tests green**

Run: `go test -count=1 ./internal/console -run 'TestDriverDaemonStore'`

Expected: PASS.

## Task 2: Loom Runtime Control

**Files:**
- Modify: `internal/loom/config.go`
- Create: `internal/loom/process_match_unix.go`
- Create: `internal/loom/process_match_windows.go`
- Modify: `internal/loom/config_test.go`

- [ ] **Step 1: Write failing process control tests**

Add tests for:

```go
func TestStopDriverDaemonStopsTrackedMCPAndDaemonForConfig(t *testing.T) { /* POSIX shell helper */ }
func TestStopDriverDaemonIgnoresDifferentConfig(t *testing.T) { /* start two configs, stop one */ }
func TestStopDriverDaemonRefusesPersistedPIDWithNonMatchingExecutable(t *testing.T) { /* use current process or sleep with wrong exe */ }
func TestStopDriverDaemonRefusesPersistedPIDWithNonMatchingArgv(t *testing.T) { /* same exe, wrong subcommand or --config */ }
func TestStopDriverDaemonRefusesPersistedPIDWithMismatchedCreationTime(t *testing.T) { /* matching exe/argv, fabricated CreatedAt */ }
func TestDriverDaemonRunningReportsTrackedDaemon(t *testing.T) { /* start then assert running */ }
func TestDriverDaemonRunningReportsFalseForPersistedPIDWithNonMatchingExecutable(t *testing.T) { /* status revalidates too */ }
```

The non-matching executable test must assert the process remains alive after stop returns.

- [ ] **Step 2: Run loom tests red**

Run: `go test -count=1 ./internal/loom -run 'Test(StopDriverDaemon|DriverDaemonRunning)'`

Expected: compile failure because runtime control helpers do not exist.

- [ ] **Step 3: Implement runtime helpers**

Add exported helpers:

```go
type DriverProcessMetadata struct {
    PID       int
    Exe       string
    Args      []string
    CreatedAt string
}

func DriverDaemonRunning(exe, configPath string, persisted []DriverProcessMetadata) bool
func StartDriverDaemonManaged(exe, configPath string) ([]DriverProcessMetadata, error)
func StopDriverDaemon(exe, configPath string, persisted []DriverProcessMetadata) error
```

Implementation rules:
- Reuse the existing `driverBackgroundProcesses` key format.
- Stop in-process tracked entries by closing stdin for MCP and killing daemon handles.
- For persisted PIDs, only terminate when executable path and argv match `serve-mcp --config <config>` or `serve-daemon --config <config>`.
- For persisted PIDs, require OS-reported process creation time to equal persisted `CreatedAt`; creation-time mismatch is stale and must never be killed.
- Treat stale or mismatched persisted PIDs as success without killing.
- Implement process metadata helpers in `process_match_unix.go` and `process_match_windows.go`: Linux reads `/proc/<pid>/exe`, `/proc/<pid>/cmdline`, `/proc/sys/kernel/random/boot_id`, and `/proc/<pid>/stat` start time and persists `linux:<boot_id>:<starttime_ticks>`; Windows reads executable path and process creation time from process handles, persists `windows:<filetime_100ns_decimal>`, and uses the safest available command-line check, treating unverifiable argv as stale instead of killable.
- Keep existing `StartDriverDaemon` as a compatibility wrapper around `StartDriverDaemonManaged`.

- [ ] **Step 4: Run loom tests green**

Run: `go test -count=1 ./internal/loom -run 'Test(StartDriver|StopDriverDaemon|DriverDaemonRunning)'`

Expected: PASS.

## Task 3: Console Controller API

**Files:**
- Modify: `internal/console/driver_daemon.go`
- Modify: `internal/console/state.go`
- Create/modify: `internal/console/driver_daemon_test.go`

- [ ] **Step 1: Write failing controller tests**

Add tests:

```go
func TestControllerDriverDaemonStateReturnsSanitizedUnavailable(t *testing.T) { /* raw path not in output */ }
func TestControllerSetDriverDaemonDisabledPersistsIntentBeforeStopError(t *testing.T) { /* stop fails, enabled false */ }
func TestControllerSetDriverDaemonEnableDoesNotPersistOnStartError(t *testing.T) { /* stays disabled */ }
func TestControllerSetDriverDaemonEnableSuccessPersistsProcessMetadata(t *testing.T) { /* PID/exe/argv/CreatedAt saved */ }
func TestControllerDriverDaemonMutationsAreSerialized(t *testing.T) { /* concurrent calls, no interleaving */ }
func TestControllerDriverDaemonStateNeverIncludesRawError(t *testing.T) { /* temp path and fake token redacted */ }
func TestControllerDriverDaemonRunningUnknownReturnsSanitizedStatus(t *testing.T) { /* status errors do not become raw errors */ }
```

- [ ] **Step 2: Run controller tests red**

Run: `go test -count=1 ./internal/console -run 'TestControllerDriverDaemon'`

Expected: compile failure for missing controller methods/deps.

- [ ] **Step 3: Implement controller methods**

Extend `Deps`:

```go
DriverDaemonStore   *DriverDaemonStore
DriverDaemonRuntime DriverDaemonRuntime
```

Add interface:

```go
type DriverDaemonRuntime interface {
    Running(context.Context, []DriverProcessRecord) (bool, error)
    Start(context.Context) ([]DriverProcessRecord, error)
    Stop(context.Context, []DriverProcessRecord) error
}
```

Add methods:

```go
func (c *Controller) DriverDaemonState(ctx context.Context) (DriverDaemonState, error)
func (c *Controller) SetDriverDaemonEnabled(ctx context.Context, enabled bool) (DriverDaemonState, error)
```

Use a controller mutex for all mutations. Return only `last_error_code` and safe fixed messages such as `driver unavailable`, `daemon start failed`, `daemon stop failed`, and `state invalid`.

- [ ] **Step 4: Run controller tests green**

Run: `go test -count=1 ./internal/console -run 'Test(DriverDaemonStore|ControllerDriverDaemon)'`

Expected: PASS.

## Task 4: HTTP Endpoints

**Files:**
- Modify: `internal/ui/console.go`
- Modify: `internal/ui/server.go`
- Modify: `internal/ui/server_test.go`

- [ ] **Step 1: Write failing endpoint tests**

Add tests:

```go
func TestServerConsoleDriverDaemonGetEndpoint(t *testing.T) { /* commander_url and enabled/running */ }
func TestServerConsoleDriverDaemonPostRequiresConsoleToken(t *testing.T) { /* forbidden without token */ }
func TestServerConsoleDriverDaemonPostTogglesEnabled(t *testing.T) { /* fake called */ }
func TestServerConsoleDriverDaemonDoesNotLeakRawPaths(t *testing.T) { /* body excludes temp dir */ }
func TestServerConsoleDriverDaemonRejectsUnsupportedMethods(t *testing.T) { /* Allow: GET, POST */ }
func TestServerConsoleDriverDaemonPostRejectsCrossOriginEvenWithToken(t *testing.T) { /* Origin mismatch rejected */ }
```

- [ ] **Step 2: Run endpoint tests red**

Run: `go test -count=1 ./internal/ui -run 'TestServerConsoleDriverDaemon'`

Expected: 404 or compile failure because endpoint/interface methods do not exist.

- [ ] **Step 3: Implement endpoint**

Add route:

```go
mux.HandleFunc("/api/console/driver-daemon", s.handleConsoleDriverDaemon)
```

`GET` calls `s.c.DriverDaemonState`. `POST` must call the shared `s.requirePostTrustedConsoleMutation` helper, decode `{ "enabled": bool }`, call `SetDriverDaemonEnabled`, and return JSON. Reject other methods with `Allow: GET, POST`.

- [ ] **Step 4: Run endpoint tests green**

Run: `go test -count=1 ./internal/ui -run 'TestServerConsoleDriverDaemon|TestConsoleMutation'`

Expected: PASS.

## Task 5: Launcher Wiring and Machine Name

**Files:**
- Modify: `cmd/launcher/main.go`
- Modify: `cmd/launcher/main_test.go`
- Modify: `internal/ui/orchestrator_real.go`
- Modify: `internal/ui/orchestrator_real_test.go`

- [ ] **Step 1: Write failing launcher tests**

Add tests:

```go
func TestConfigureCompletedLoomDriverUsesMachineComputerName(t *testing.T) { /* display_name: TEST-PC */ }
func TestConfigureCompletedLoomDriverUsesMachineSpecificDescription(t *testing.T) { /* description contains TEST-PC */ }
func TestConfigureCompletedLoomDriverFallbackDisplayNameHasStableSuffix(t *testing.T) { /* local-computer-<suffix> */ }
func TestConfigureCompletedLoomDriverDoesNotAutoStartWhenDriverDisabled(t *testing.T) { /* fake start not called */ }
func TestConfigureCompletedLoomDriverDoesNotAutoStartWhenDriverStateCorrupt(t *testing.T) { /* corrupt state fails closed */ }
func TestServeCompletedConsoleWiresDriverDaemonStatePath(t *testing.T) { /* InstallRoot/driver-daemon.json */ }
```

For start/stop injection, introduce package-level variables `startCompletedLoomDriverDaemon` and `driverDaemonEnabledForCompletedConfig` so tests can assert whether auto-start was attempted; reset both variables in `t.Cleanup`.

- [ ] **Step 2: Run launcher tests red**

Run: `go test -count=1 ./cmd/launcher -run 'TestConfigureCompletedLoomDriver.*(Machine|Disabled)|TestServeCompletedConsoleWiresDriver'`

Expected: failures because display name is still `星池指挥官` and disabled state is not wired.

- [ ] **Step 3: Implement launcher wiring**

Add:

```go
func driverDaemonStatePath(p paths.Paths) string {
    if strings.TrimSpace(p.InstallRoot) == "" {
        return ""
    }
    return filepath.Join(p.InstallRoot, "driver-daemon.json")
}
```

Use `slave.NewMachineStore(p.MachineFile).Ensure(completedComputerName())` to derive display name. Wire `console.NewController` with a `DriverDaemonStore` and runtime implementation that calls `loom.StartDriverDaemonManaged`, `loom.StopDriverDaemon`, and `loom.DriverDaemonRunning`. Only auto-start during completed driver configuration when the loaded driver state is enabled. If loading driver state returns a corrupt/unreadable-state error, treat auto-start as disabled and surface only sanitized diagnostics through the console API.

Update the computer-name fallback helper so the terminal fallback is `local-computer-<stable suffix>`, where the suffix comes from the install ID when available. Keep `InstallRoot/driver-daemon.json` under the existing per-user install root (`~/.agentserver-app` on Unix-like systems and the current user's home-derived path on Windows).

- [ ] **Step 4: Update onboarding-time driver config**

Extend `ui.Deps` with `MachineFile string`, pass `in.Paths.MachineFile` from `cmd/launcher/main.go`, and use the same machine-name helper before calling `loom.WriteDriverConfig` in `internal/ui/orchestrator_real.go`. Add an orchestrator regression test that `discovery.display_name` contains `TEST-PC` when a machine file exists.
Also assert `discovery.description` contains `TEST-PC 本地协作驱动。`.

- [ ] **Step 5: Run launcher/UI orchestrator tests green**

Run: `go test -count=1 ./cmd/launcher ./internal/ui -run 'TestConfigureCompletedLoomDriver|TestPollAgentserverLoginRefreshesLoomDriverConfigAndMCP|TestConfigureSharedCodex'`

Expected: PASS.

## Task 6: Vue API and Dashboard

**Files:**
- Modify: `internal/ui/web/src/api.ts`
- Modify: `internal/ui/web/src/components/Dashboard.vue`
- Modify: `internal/ui/web/src/__tests__/api.spec.ts`
- Modify: `internal/ui/web/src/__tests__/Dashboard.spec.ts`

- [ ] **Step 1: Write failing frontend API tests**

Add:

```ts
it('driver daemon APIs call console endpoint with token', async () => {
  const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
    ok: true,
    status: 200,
    json: async () => ({ enabled: true, running: true, commander_url: 'https://loom.nj.cs.ac.cn:10062/commander' }),
  } as Response);
  await api.getConsoleDriverDaemon();
  await api.setConsoleDriverDaemon(false);
  expect(fetchSpy).toHaveBeenCalledWith('/api/console/driver-daemon', undefined);
  expect(fetchSpy).toHaveBeenCalledWith('/api/console/driver-daemon', expect.objectContaining({ method: 'POST' }));
});
```

- [ ] **Step 2: Write failing dashboard tests**

Add tests for:

```ts
expect(w.text()).toContain('远程控制');
expect(w.find('a[href="https://loom.nj.cs.ac.cn:10062/commander"]').attributes('rel')).toBe('noopener noreferrer');
await w.find('[data-test="driver-daemon-toggle"]').trigger('click');
expect(api.setConsoleDriverDaemon).toHaveBeenCalledWith(false);
```

Also test duplicate-click guard and sanitized error display.
Add an assertion that a second click while the first `setConsoleDriverDaemon` promise is pending does not trigger a second call, and that the link uses `driverDaemonState.commander_url` rather than a second hard-coded frontend constant.

- [ ] **Step 3: Run frontend tests red**

Run: `npm test -- --run src/__tests__/api.spec.ts src/__tests__/Dashboard.spec.ts`

Working directory: `internal/ui/web`

Expected: failures because API functions and UI elements do not exist.

- [ ] **Step 4: Implement frontend API/UI**

Add types:

```ts
export interface ConsoleDriverDaemonState {
  enabled: boolean;
  running: boolean;
  commander_url: string;
  last_error_code?: string;
  last_error_message?: string;
}
```

Add API functions and dashboard refs: `driverDaemonState`, `driverDaemonError`, `loadingDriverDaemon`, `togglingDriverDaemon`. Load state on mount; render a `remote-control-panel`; use an Element Plus switch or button with `data-test="driver-daemon-toggle"`.

- [ ] **Step 5: Run frontend tests green**

Run: `npm test -- --run src/__tests__/api.spec.ts src/__tests__/Dashboard.spec.ts`

Expected: PASS.

## Task 7: Integration Verification and Security Review

**Files:**
- All changed files.

- [ ] **Step 1: Run targeted Go tests**

Run:

```bash
go test -count=1 ./internal/loom ./internal/console ./internal/ui ./cmd/launcher
```

Expected: PASS.

- [ ] **Step 2: Run targeted frontend tests and build**

Run from `internal/ui/web`:

```bash
npm test -- --run src/__tests__/api.spec.ts src/__tests__/Dashboard.spec.ts
npm run build
```

Expected: PASS.

- [ ] **Step 3: Run broader verification**

Run:

```bash
go test -count=1 ./internal/appversion ./internal/ui ./cmd/launcher ./internal/loom ./internal/slave ./internal/console
GOOS=windows go test -run '^$' ./internal/loom
```

Expected: PASS.

- [ ] **Step 4: Codex security self-review**

Check:

- No unauthenticated mutation endpoint was added.
- No raw error string containing paths/tokens reaches JSON responses.
- No process stop path kills by process name alone.
- Disabled intent is persisted before stop and remains disabled after stop failure.
- Enable failure does not persist `enabled: true`.
- Commander URL is fixed to the requested trusted URL and rendered with `noopener noreferrer`.

- [ ] **Step 5: Fresh Claude final review**

Run a fresh Claude Code subprocess with no provided diff. Ask it to explore the repo and report P0/P1 only as blockers. Fix any valid P0/P1 findings and repeat this task.
