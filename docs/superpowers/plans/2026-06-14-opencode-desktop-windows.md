# OpenCode Desktop Windows Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `opencode_desktop` as a third Windows frontend mode, install OpenCode Desktop from the official NSIS installer, and configure it to use the existing local model proxy for `code.ai.cs.ac.cn`.

**Architecture:** Extend the existing frontend-mode dispatch rather than creating a parallel installer path. Add focused packages for OpenCode config writing and OpenCode Desktop detection/launch/install, then wire those packages into onboarding, completed launch, Explorer open-folder, UI steps, and Windows packaging.

**Tech Stack:** Go, PowerShell, Inno Setup, Vue/TypeScript, Vitest, Windows registry/protocol launching, OpenCode JSON/JSONC config.

---

## File Structure

- Modify `internal/state/types.go` and `internal/state/types_test.go` for the new `opencode_desktop` mode and persisted state.
- Modify `internal/installmode/installmode.go` and tests so installer mode files accept the third mode.
- Modify `internal/paths/paths.go` for OpenCode config paths.
- Create `internal/opencode/config.go` and `internal/opencode/config_test.go` for OpenCode JSON config merging.
- Create `internal/opencodedesktop/detect.go`, `install.go`, `launch.go`, Windows-specific helpers, and tests.
- Modify `internal/ui/orchestrator.go`, `internal/ui/orchestrator_real.go`, and tests for ensure/configure/launch dispatch.
- Modify `cmd/launcher/main.go` and tests for completed-mode OpenCode launch.
- Modify `cmd/open-folder/main.go` and tests for OpenCode right-click launch.
- Modify `internal/ui/web/src/*` files and tests for OpenCode onboarding steps and API types.
- Add `packaging/windows/ensure-opencode-desktop.ps1`.
- Modify `packaging/windows/install.ps1`, `installer.iss`, `write-install-mode.ps1`, scripts under `scripts/`, and packaging tests under `internal/vscode/install_test.go`.

## Task 1: State, Install Mode, And Paths

**Files:**
- Modify: `internal/state/types.go`
- Modify: `internal/state/types_test.go`
- Modify: `internal/installmode/installmode.go`
- Modify: `internal/installmode/installmode_test.go`
- Modify: `internal/paths/paths.go`

- [ ] **Step 1: Write failing state tests**

Add tests that prove the new mode normalizes and round-trips:

```go
func TestNormalizeFrontendModeAcceptsOpenCodeDesktop(t *testing.T) {
	got := NormalizeFrontendMode(FrontendMode("opencode_desktop"))
	if got != FrontendMode("opencode_desktop") {
		t.Fatalf("NormalizeFrontendMode(opencode_desktop) = %q", got)
	}
}

func TestStateRoundTripIncludesOpenCodeDesktopState(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *State) error {
		s.FrontendMode = FrontendMode("opencode_desktop")
		s.OpenCodeDesktop.Installed = true
		s.OpenCodeDesktop.Path = `C:\Users\alice\AppData\Local\Programs\OpenCode\OpenCode.exe`
		s.OpenCodeDesktop.Version = "1.2.3"
		s.OpenCodeDesktop.InstalledByUs = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.FrontendMode != FrontendMode("opencode_desktop") {
		t.Fatalf("FrontendMode = %q", got.FrontendMode)
	}
	if !got.OpenCodeDesktop.Installed || got.OpenCodeDesktop.Path == "" || got.OpenCodeDesktop.Version != "1.2.3" {
		t.Fatalf("OpenCodeDesktop state not persisted: %+v", got.OpenCodeDesktop)
	}
}
```

- [ ] **Step 2: Verify RED**

Run:

```sh
go test ./internal/state
```

Expected: compile failure because `OpenCodeDesktop` state and constant do not exist.

- [ ] **Step 3: Implement state**

Add:

```go
const (
	FrontendModeCodexDesktop    FrontendMode = "codex_desktop"
	FrontendModeOpenCodeDesktop FrontendMode = "opencode_desktop"
	FrontendModeMinimalVSCode   FrontendMode = "minimal_vscode"
)

func NormalizeFrontendMode(mode FrontendMode) FrontendMode {
	switch mode {
	case FrontendModeOpenCodeDesktop:
		return FrontendModeOpenCodeDesktop
	case FrontendModeMinimalVSCode:
		return FrontendModeMinimalVSCode
	default:
		return FrontendModeCodexDesktop
	}
}
```

and:

```go
OpenCodeDesktop OpenCodeDesktopState `json:"opencode_desktop"`

type OpenCodeDesktopState struct {
	Installed     bool   `json:"installed"`
	Version       string `json:"version,omitempty"`
	Path          string `json:"path,omitempty"`
	InstalledByUs bool   `json:"installed_by_us"`
}
```

- [ ] **Step 4: Verify GREEN**

Run:

```sh
go test ./internal/state
```

Expected: PASS.

- [ ] **Step 5: Write failing installmode and path tests**

Add:

```go
func TestWriteAndReadOpenCodeDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install-mode.json")
	if err := Write(path, state.FrontendModeOpenCodeDesktop); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != state.FrontendModeOpenCodeDesktop {
		t.Fatalf("mode = %q, want %q", got, state.FrontendModeOpenCodeDesktop)
	}
}
```

Create `internal/paths/paths_test.go` with this path assertion:

```go
func TestDefaultIncludesOpenCodeConfigPath(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p.OpenCodeConfigDir) != "opencode" {
		t.Fatalf("OpenCodeConfigDir = %q", p.OpenCodeConfigDir)
	}
	if filepath.Base(p.OpenCodeConfigFile) != "opencode.jsonc" {
		t.Fatalf("OpenCodeConfigFile = %q", p.OpenCodeConfigFile)
	}
}
```

- [ ] **Step 6: Verify RED**

Run:

```sh
go test ./internal/installmode ./internal/paths
```

Expected: compile failure for missing `FrontendModeOpenCodeDesktop` in installmode tests and missing OpenCode path fields.

- [ ] **Step 7: Implement installmode and paths**

No extra installmode production logic is needed after Task 1 state normalization; add paths:

```go
OpenCodeConfigDir  string
OpenCodeConfigFile string
```

and initialize:

```go
openCodeConfigDir := filepath.Join(home, ".config", "opencode")
OpenCodeConfigDir:  openCodeConfigDir,
OpenCodeConfigFile: filepath.Join(openCodeConfigDir, "opencode.jsonc"),
```

- [ ] **Step 8: Verify and commit**

Run:

```sh
go test ./internal/state ./internal/installmode ./internal/paths
```

Expected: PASS.

Commit:

```sh
git add internal/state internal/installmode internal/paths
git commit -m "feat: add opencode frontend mode state"
```

## Task 2: OpenCode Config Writer

**Files:**
- Create: `internal/opencode/config.go`
- Create: `internal/opencode/config_test.go`

- [ ] **Step 1: Write failing config tests**

Create tests for new config creation and merge preservation:

```go
func TestUpdateConfigCreatesModelserverProxyProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode", "opencode.jsonc")
	err := UpdateConfig(path, Settings{
		BaseURL:   "http://127.0.0.1:53452/v1",
		APIKeyEnv: "AGENTSERVER_CODEX_LOCAL_API_KEY",
		Model:    "gpt-5.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	readJSONFile(t, path, &got)
	if got["model"] != "modelserver/gpt-5.5" {
		t.Fatalf("model = %v", got["model"])
	}
	provider := got["provider"].(map[string]any)["modelserver"].(map[string]any)
	if provider["npm"] != "@ai-sdk/openai" {
		t.Fatalf("npm = %v", provider["npm"])
	}
	options := provider["options"].(map[string]any)
	if options["baseURL"] != "http://127.0.0.1:53452/v1" {
		t.Fatalf("baseURL = %v", options["baseURL"])
	}
	if options["apiKey"] != "{env:AGENTSERVER_CODEX_LOCAL_API_KEY}" {
		t.Fatalf("apiKey = %v", options["apiKey"])
	}
}

func TestUpdateConfigPreservesUnrelatedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	existing := `{
	  "$schema": "https://opencode.ai/config.json",
	  "theme": "system",
	  "provider": {
	    "anthropic": {
	      "models": {
	        "claude": {"name": "Claude"}
	      }
	    },
	    "modelserver": {
	      "name": "old"
	    }
	  }
	}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateConfig(path, DefaultProxySettings()); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	readJSONFile(t, path, &got)
	if got["theme"] != "system" {
		t.Fatalf("theme was not preserved: %#v", got["theme"])
	}
	providers := got["provider"].(map[string]any)
	if _, ok := providers["anthropic"]; !ok {
		t.Fatalf("anthropic provider was removed: %#v", providers)
	}
	if providers["modelserver"].(map[string]any)["name"] != "modelserver" {
		t.Fatalf("modelserver provider was not overwritten: %#v", providers["modelserver"])
	}
}
```

- [ ] **Step 2: Verify RED**

Run:

```sh
go test ./internal/opencode
```

Expected: package or symbols do not exist.

- [ ] **Step 3: Implement minimal writer**

Implement:

```go
type Settings struct {
	BaseURL   string
	APIKeyEnv string
	Model    string
}

func DefaultProxySettings() Settings {
	return Settings{
		BaseURL:   "http://127.0.0.1:53452/v1",
		APIKeyEnv: "AGENTSERVER_CODEX_LOCAL_API_KEY",
		Model:    "gpt-5.5",
	}
}
```

`UpdateConfig(path string, s Settings) error` should:

- parse existing JSON/JSONC using `github.com/tailscale/hujson` if already available; otherwise add and use a small dependency-free fallback that accepts ordinary JSON and returns a clear parse error for unsupported JSONC
- set `$schema` if empty
- set `model` to `modelserver/<model>`
- ensure `provider` is a map and replace only `provider.modelserver`
- write through `path + ".tmp"` then rename, mode `0600`

- [ ] **Step 4: Verify GREEN**

Run:

```sh
go test ./internal/opencode
```

Expected: PASS.

- [ ] **Step 5: Add invalid existing config test**

Add:

```go
func TestUpdateConfigReportsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	if err := os.WriteFile(path, []byte(`{"provider":`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := UpdateConfig(path, DefaultProxySettings())
	if err == nil || !strings.Contains(err.Error(), "parse opencode config") {
		t.Fatalf("err = %v, want parse opencode config", err)
	}
}
```

- [ ] **Step 6: Verify and commit**

Run:

```sh
go test ./internal/opencode
```

Expected: PASS.

Commit:

```sh
git add internal/opencode go.mod go.sum
git commit -m "feat: write opencode model proxy config"
```

## Task 3: OpenCode Desktop Detection, Install, And Launch

**Files:**
- Create: `internal/opencodedesktop/detect.go`
- Create: `internal/opencodedesktop/detect_windows.go`
- Create: `internal/opencodedesktop/install.go`
- Create: `internal/opencodedesktop/launch.go`
- Create: `internal/opencodedesktop/opencodedesktop_test.go`

- [ ] **Step 1: Write failing detection and launch tests**

Create tests around parsing and command construction:

```go
func TestParseDetectionOutput(t *testing.T) {
	out := `{"installed":true,"path":"C:\\Users\\alice\\AppData\\Local\\Programs\\OpenCode\\OpenCode.exe","version":"1.2.3"}`
	got, err := parseDetectOutput([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Installed || got.Path == "" || got.Version != "1.2.3" {
		t.Fatalf("detected = %+v", got)
	}
}

func TestLaunchUsesDetectedExecutableAndFolderWorkingDirectory(t *testing.T) {
	var gotName string
	var gotDir string
	runner := func(cmd *exec.Cmd) error {
		gotName = cmd.Path
		gotDir = cmd.Dir
		return nil
	}
	err := Launch(context.Background(), LaunchOptions{
		Detected: Detected{Installed: true, Path: `C:\OpenCode\OpenCode.exe`},
		Folder:   `C:\work\repo`,
		Run:      runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotName != `C:\OpenCode\OpenCode.exe` || gotDir != `C:\work\repo` {
		t.Fatalf("launch path=%q dir=%q", gotName, gotDir)
	}
}
```

- [ ] **Step 2: Verify RED**

Run:

```sh
go test ./internal/opencodedesktop
```

Expected: package or symbols missing.

- [ ] **Step 3: Implement portable types and launch**

Implement:

```go
type Detected struct {
	Installed bool
	Path      string
	Version   string
}

type LaunchOptions struct {
	Detected Detected
	Folder   string
	Run      func(*exec.Cmd) error
	OpenURL  func(string) error
}
```

`Launch` should:

- use detected path when present
- set `cmd.Dir = folder` when folder is non-empty
- fallback to `opencode://` via `OpenURL` when no executable is available
- return a clear error when neither exe nor opener exists

- [ ] **Step 4: Verify GREEN**

Run:

```sh
go test ./internal/opencodedesktop
```

Expected: PASS.

- [ ] **Step 5: Write failing install planning test**

Add:

```go
func TestEnsureInstalledUsesDetectFastPath(t *testing.T) {
	calledInstall := false
	got, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			return Detected{Installed: true, Path: `C:\OpenCode\OpenCode.exe`, Version: "1.2.3"}, nil
		},
		RunInstaller: func(context.Context) error {
			calledInstall = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calledInstall {
		t.Fatal("installer should not run on fast path")
	}
	if got.Version != "1.2.3" {
		t.Fatalf("got = %+v", got)
	}
}
```

- [ ] **Step 6: Implement EnsureInstalled**

Implement `Options` with `Detect`, `RunInstaller`, and default local installer runner. `EnsureInstalled` should detect, run installer once, then detect again and error if still not installed.

- [ ] **Step 7: Verify and commit**

Run:

```sh
go test ./internal/opencodedesktop
```

Expected: PASS.

Commit:

```sh
git add internal/opencodedesktop
git commit -m "feat: add opencode desktop helpers"
```

## Task 4: Orchestrator, Launcher, And Open-Folder Wiring

**Files:**
- Modify: `internal/ui/orchestrator.go`
- Modify: `internal/ui/orchestrator_real.go`
- Modify: `internal/ui/orchestrator_real_test.go`
- Modify: `cmd/launcher/main.go`
- Modify: `cmd/launcher/main_test.go`
- Modify: `cmd/open-folder/main.go`
- Modify: `cmd/open-folder/main_test.go`

- [ ] **Step 1: Write failing orchestrator tests**

Add tests that `EnsureFrontend` and `ConfigureFrontend` dispatch OpenCode:

```go
func TestEnsureFrontendOpenCodeDesktop(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeOpenCodeDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	called := false
	r := NewRealOrchestrator(Deps{
		State: store,
		OpenCodeDesktopEnsure: func(context.Context) (opencodedesktop.Detected, error) {
			called = true
			return opencodedesktop.Detected{Installed: true, Path: `C:\OpenCode\OpenCode.exe`, Version: "1.2.3"}, nil
		},
	}).(*realOrchestrator)
	if err := r.EnsureFrontend(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("OpenCode ensure was not called")
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Onboarding.HasCompleted("opencode_desktop_installed") || !got.OpenCodeDesktop.Installed {
		t.Fatalf("state = %+v", got)
	}
}
```

Add configure test that asserts `opencode.UpdateConfig` wrote the provider and completed `opencode_desktop_configured`.

- [ ] **Step 2: Verify RED**

Run:

```sh
go test ./internal/ui
```

Expected: compile failure for missing deps and OpenCode dispatch.

- [ ] **Step 3: Implement orchestrator wiring**

Extend `Deps`:

```go
OpenCodeConfigPath     string
OpenCodeDesktopEnsure  func(context.Context) (opencodedesktop.Detected, error)
OpenCodeDesktopLaunch  func(context.Context, opencodedesktop.LaunchOptions) error
```

Add `EnsureOpenCodeDesktop`, `ConfigureOpenCodeDesktop`, and update dispatch. `ConfigureOpenCodeDesktop` calls `configureSharedCodex(ctx)` and then `opencode.UpdateConfig`.

- [ ] **Step 4: Verify GREEN**

Run:

```sh
go test ./internal/ui
```

Expected: PASS.

- [ ] **Step 5: Write failing launcher/open-folder tests**

Launcher completed mode should call OpenCode launcher when `FrontendModeOpenCodeDesktop`:

```go
func TestLaunchCompletedFrontendOpenCodeDesktopWritesConfigAndLaunches(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		CodexConfigFile:    filepath.Join(dir, ".codex", "config.toml"),
		OpenCodeConfigFile: filepath.Join(dir, ".config", "opencode", "opencode.jsonc"),
	}
	s := &state.State{FrontendMode: state.FrontendModeOpenCodeDesktop}
	s.OpenCodeDesktop.Path = `C:\OpenCode\OpenCode.exe`
	launched := false
	err := launchCompletedFrontend(context.Background(), s, p, nil, "", "", "", nil, func(ctx context.Context, opts opencodedesktop.LaunchOptions) error {
		launched = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !launched {
		t.Fatal("OpenCode was not launched")
	}
	if _, err := os.Stat(p.OpenCodeConfigFile); err != nil {
		t.Fatalf("opencode config not written: %v", err)
	}
}
```

Open-folder should dispatch by mode and pass folder as working directory.

- [ ] **Step 6: Verify RED**

Run:

```sh
go test ./cmd/launcher ./cmd/open-folder
```

Expected: compile failure due new function signature or missing OpenCode path.

- [ ] **Step 7: Implement launcher/open-folder wiring**

Add OpenCode path to onboarding deps in `serveOnboarding`. Update completed launch signatures to accept an OpenCode launcher dependency for tests. In production use `opencodedesktop.Launch`.

Update `cmd/open-folder` dispatch:

```go
switch state.NormalizeFrontendMode(s.FrontendMode) {
case state.FrontendModeOpenCodeDesktop:
	return openFolderOpenCodeDesktop(...)
case state.FrontendModeCodexDesktop:
	return openFolderCodexDesktop(...)
default:
	return openFolderVSCode(...)
}
```

- [ ] **Step 8: Verify and commit**

Run:

```sh
go test ./internal/ui ./cmd/launcher ./cmd/open-folder
```

Expected: PASS.

Commit:

```sh
git add internal/ui cmd/launcher cmd/open-folder
git commit -m "feat: wire opencode desktop frontend mode"
```

## Task 5: Web UI Mode And Steps

**Files:**
- Modify: `internal/ui/web/src/api.ts`
- Modify: `internal/ui/web/src/stepConfig.ts`
- Modify: `internal/ui/web/src/composables/useOnboarding.ts`
- Modify: `internal/ui/web/src/components/ActionStep.vue`
- Modify: `internal/ui/web/src/__tests__/*.spec.ts`

- [ ] **Step 1: Write failing web tests**

Add tests:

```ts
it('returns OpenCode Desktop steps for opencode mode', () => {
  expect(stepsForMode('opencode_desktop').map(s => s.id)).toEqual([
    'modelserver_login',
    'agentserver_login',
    'opencode_desktop_install',
    'opencode_desktop_configure',
    'finalize',
  ]);
});

it('maps completed OpenCode steps', () => {
  expect(completedMapForMode('opencode_desktop').opencode_desktop_installed).toBe('opencode_desktop_install');
  expect(completedMapForMode('opencode_desktop').opencode_desktop_configured).toBe('opencode_desktop_configure');
});
```

Add a `useOnboarding` test that `frontend_name` defaults to `OpenCode Desktop` for `opencode_desktop`.

- [ ] **Step 2: Verify RED**

Run:

```sh
npm --prefix internal/ui/web test -- stepConfig useOnboarding
```

Expected: tests fail because `opencode_desktop` normalizes to Codex Desktop.

- [ ] **Step 3: Implement web mode support**

Update:

```ts
export type FrontendMode = 'codex_desktop' | 'opencode_desktop' | 'minimal_vscode';
```

Add OpenCode step definitions and completed map. Update `normalizeMode`, default frontend name, and API types. Update `ActionStep` to include `opencode_desktop_configure`.

- [ ] **Step 4: Verify and commit**

Run:

```sh
npm --prefix internal/ui/web test
```

Expected: PASS.

Commit:

```sh
git add internal/ui/web
git commit -m "feat: add opencode desktop onboarding steps"
```

## Task 6: Windows Packaging

**Files:**
- Create: `packaging/windows/ensure-opencode-desktop.ps1`
- Modify: `packaging/windows/write-install-mode.ps1`
- Modify: `packaging/windows/install.ps1`
- Modify: `packaging/windows/installer.iss`
- Modify: `scripts/windows-package-common.sh`
- Modify: `scripts/package-windows-zip.sh`
- Modify: `internal/vscode/install_test.go`

- [ ] **Step 1: Write failing packaging tests**

Add assertions to `internal/vscode/install_test.go`:

```go
func TestWindowsInstallScriptSupportsOpenCodeDesktopMode(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"[switch]$OpenCodeDesktop",
		"opencode_desktop",
		"ensure-opencode-desktop.ps1",
		"opencode-desktop-installer.exe",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("install.ps1 missing %q", want)
		}
	}
}

func TestWriteInstallModeAllowsOpenCodeDesktop(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/write-install-mode.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "'opencode_desktop'") {
		t.Fatal("write-install-mode.ps1 must allow opencode_desktop")
	}
}

func TestWindowsPackagingBundlesOpenCodeDesktopInstaller(t *testing.T) {
	body, err := os.ReadFile("../../scripts/windows-package-common.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"OPENCODE_DESKTOP_URL=\"https://opencode.ai/download/stable/windows-x64-nsis\"",
		"verify_opencode_desktop_installer()",
		"$OPENCODE_DESKTOP_CACHE::opencode-desktop-installer.exe",
		"packaging/windows/ensure-opencode-desktop.ps1",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("windows-package-common.sh missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Verify RED**

Run:

```sh
go test ./internal/vscode
```

Expected: failures for missing OpenCode installer support.

- [ ] **Step 3: Implement PowerShell install mode support**

Update `write-install-mode.ps1`:

```powershell
[ValidateSet('codex_desktop', 'opencode_desktop', 'minimal_vscode')]
```

Update `install.ps1`:

- add `[switch]$OpenCodeDesktop`
- throw when both OpenCode and Minimal VS Code are selected
- add required payloads
- write `opencode_desktop` and run `ensure-opencode-desktop.ps1` in OpenCode mode

- [ ] **Step 4: Add `ensure-opencode-desktop.ps1`**

Implement PowerShell helper with:

- `Test-OpenCodeDesktopInstalled`
- `Wait-OpenCodeDesktopInstalled`
- `Test-OpenCodeDesktopInstallerFile`
- `Invoke-OpenCodeDesktopLocalInstaller`

Verification must check MZ, minimum size, valid Authenticode signature, and avoid Microsoft-only signer assumptions.

- [ ] **Step 5: Implement Inno and script packaging**

Update Inno to include:

- OpenCode installer file
- helper script
- `opencodedesktop` task
- mutually exclusive frontend task behavior
- OpenCode install branch

Update `scripts/windows-package-common.sh` to define URL/cache, download every build, verify installer, and include payload.

- [ ] **Step 6: Verify and commit**

Run:

```sh
go test ./internal/vscode
```

Expected: PASS.

Commit:

```sh
git add packaging/windows scripts internal/vscode/install_test.go
git commit -m "feat: package opencode desktop for windows"
```

## Task 7: Full Verification And PR Prep

**Files:**
- Modify generated UI assets only after `make ui-build`
- No new source files unless verification exposes a bug

- [ ] **Step 1: Run focused Go tests**

Run:

```sh
go test ./internal/state ./internal/installmode ./internal/paths ./internal/opencode ./internal/opencodedesktop ./internal/ui ./cmd/launcher ./cmd/open-folder ./internal/vscode
```

Expected: PASS.

- [ ] **Step 2: Run UI tests and build assets**

Run:

```sh
npm --prefix internal/ui/web test
make ui-build
```

Expected: PASS and `internal/ui/assets/dist` updated.

- [ ] **Step 3: Run full Go tests**

Run:

```sh
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Run race tests**

Run:

```sh
go test -race ./...
```

Expected: PASS. If a pre-existing flaky Windows/Wine test fails on Linux, capture the exact package and error before deciding whether to proceed.

- [ ] **Step 5: Build Windows artifacts**

Run:

```sh
make cross-windows
make package-windows-zip
```

Expected: Windows binaries and portable zip are produced. The package script downloads and verifies `opencode-desktop-installer.exe`.

- [ ] **Step 6: Commit generated assets**

If `make ui-build` updated committed UI assets:

```sh
git add internal/ui/assets/dist
git commit -m "build: refresh onboarding assets"
```

- [ ] **Step 7: Final status**

Run:

```sh
git status --short --branch
git log --oneline --max-count=8
```

Expected: branch contains the spec commit, plan commit, implementation commits, and no uncommitted source changes unless intentionally reported.
