# Codex Desktop Winget Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Codex Desktop the default Windows frontend installed with `winget install Codex -s msstore`, while preserving the simplified VS Code flow only when the user selects `极简风`.

**Architecture:** Add a persisted `frontend_mode` to state and install-mode files, then route installer scripts, onboarding backend, Vue steps, launcher, and folder-open entrypoints through that mode. Introduce `internal/codexdesktop` for detection, winget install command construction, and `codex://` launching; keep the existing VS Code code path intact behind `minimal_vscode`.

**Tech Stack:** Go, Vue 3 + Vitest, PowerShell 5.1-compatible scripts with UTF-8 BOM, Inno Setup, existing `state.Store`, existing `ui.Orchestrator`, existing Windows packaging tests.

---

## File Structure

- Create `internal/installmode/installmode.go`: read/write/sync `{app}\install-mode.json` with state.
- Create `internal/installmode/installmode_test.go`: unit coverage for mode file parsing and sync.
- Modify `internal/state/types.go`: add `FrontendMode`, `CodexDesktopState`, and mode normalization helpers.
- Modify `internal/state/store.go`: default new state to `codex_desktop`.
- Modify `internal/state/types_test.go`: state roundtrip/default mode coverage.
- Create `internal/codexdesktop/detect.go`, `detect_windows.go`, `detect_other.go`, `install.go`, `launch.go`, `winget.go`: focused Codex Desktop support.
- Create `internal/codexdesktop/*_test.go`: unit coverage for winget args, install idempotency, and launch URLs.
- Modify `internal/ui/orchestrator.go`, `internal/ui/orchestrator_real.go`, `internal/ui/server.go`: generic frontend install/configure/launch APIs with legacy VS Code wrappers.
- Modify `internal/ui/*_test.go`: server and orchestrator tests for both frontend modes.
- Modify `cmd/launcher/main.go`, `cmd/launcher/main_test.go`: sync install-mode file and launch completed installs by mode.
- Modify `cmd/open-folder/main.go`: open Codex Desktop deep link in default mode, VS Code in `minimal_vscode`.
- Modify `cmd/agentctl/cmd_doctor.go`, `cmd/agentctl/doctor_test.go`, `cmd/agentctl/cmd_test_subcommands.go`: expose frontend mode and add Codex Desktop test helpers.
- Modify `internal/ui/web/src/api.ts`, `stepConfig.ts`, `composables/useOnboarding.ts`, `components/*.vue`, and tests: mode-aware wizard.
- Create `packaging/windows/ensure-codex-desktop.ps1`: PowerShell winget installer.
- Create `packaging/windows/write-install-mode.ps1`: PowerShell mode file writer.
- Modify `packaging/windows/install.ps1`, `installer.iss`, `scripts/package-windows.sh`, `scripts/package-windows-zip.sh`: include new scripts and default to Codex Desktop.
- Modify `internal/vscode/install_test.go`: packaging assertions for new scripts and mode selection.

## Task 1: Persist Frontend Mode

**Files:**
- Modify: `internal/state/types.go`
- Modify: `internal/state/store.go`
- Modify: `internal/state/types_test.go`
- Create: `internal/installmode/installmode.go`
- Create: `internal/installmode/installmode_test.go`

- [ ] **Step 1: Add failing state tests**

Append these tests to `internal/state/types_test.go`:

```go
func TestFrontendModeNormalize(t *testing.T) {
	for _, tc := range []struct {
		in   FrontendMode
		want FrontendMode
	}{
		{"", FrontendModeCodexDesktop},
		{"bogus", FrontendModeCodexDesktop},
		{FrontendModeCodexDesktop, FrontendModeCodexDesktop},
		{FrontendModeMinimalVSCode, FrontendModeMinimalVSCode},
	} {
		if got := NormalizeFrontendMode(tc.in); got != tc.want {
			t.Fatalf("NormalizeFrontendMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFreshStateDefaultsToCodexDesktop(t *testing.T) {
	got := freshState()
	if got.FrontendMode != FrontendModeCodexDesktop {
		t.Fatalf("FrontendMode = %q, want %q", got.FrontendMode, FrontendModeCodexDesktop)
	}
}

func TestStateRoundtripFrontendModeAndCodexDesktop(t *testing.T) {
	s := State{
		SchemaVersion: CurrentSchemaVersion,
		InstallID:     "front-1",
		FrontendMode:  FrontendModeMinimalVSCode,
		CodexDesktop: CodexDesktopState{
			Installed:     true,
			Version:       "1.2.3",
			InstalledByUs: true,
		},
	}
	b, err := json.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got State
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.FrontendMode != FrontendModeMinimalVSCode {
		t.Fatalf("FrontendMode = %q", got.FrontendMode)
	}
	if !got.CodexDesktop.Installed || got.CodexDesktop.Version != "1.2.3" || !got.CodexDesktop.InstalledByUs {
		t.Fatalf("CodexDesktop roundtrip lost data: %+v", got.CodexDesktop)
	}
}
```

- [ ] **Step 2: Run state tests and verify they fail**

Run:

```bash
go test ./internal/state -run 'TestFrontendMode|TestFreshStateDefaultsToCodexDesktop|TestStateRoundtripFrontendModeAndCodexDesktop' -count=1
```

Expected: FAIL because `FrontendMode`, `CodexDesktopState`, and `NormalizeFrontendMode` do not exist.

- [ ] **Step 3: Implement state mode types**

In `internal/state/types.go`, add the mode constants after `Status` constants and add fields to `State`:

```go
type FrontendMode string

const (
	FrontendModeCodexDesktop  FrontendMode = "codex_desktop"
	FrontendModeMinimalVSCode FrontendMode = "minimal_vscode"
)

func NormalizeFrontendMode(mode FrontendMode) FrontendMode {
	switch mode {
	case FrontendModeMinimalVSCode:
		return FrontendModeMinimalVSCode
	default:
		return FrontendModeCodexDesktop
	}
}
```

Update `State`:

```go
type State struct {
	SchemaVersion int               `json:"schema_version"`
	InstallID     string            `json:"install_id"`
	CreatedAt     time.Time         `json:"created_at"`
	FrontendMode  FrontendMode      `json:"frontend_mode,omitempty"`
	Onboarding    OnboardingState   `json:"onboarding"`
	Modelserver   ModelserverState  `json:"modelserver"`
	Agentserver   AgentserverState  `json:"agentserver"`
	VSCode        VSCodeState       `json:"vscode"`
	CodexDesktop  CodexDesktopState `json:"codex_desktop"`
	Shortcuts     ShortcutsState    `json:"shortcuts"`
}
```

Add after `VSCodeState`:

```go
type CodexDesktopState struct {
	Installed     bool   `json:"installed"`
	Version       string `json:"version,omitempty"`
	InstalledByUs bool  `json:"installed_by_us"`
}
```

In `internal/state/store.go`, set the default in `freshState()`:

```go
FrontendMode: NormalizeFrontendMode(FrontendModeCodexDesktop),
```

- [ ] **Step 4: Add install-mode file tests**

Create `internal/installmode/installmode_test.go`:

```go
package installmode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/state"
)

func TestReadMissingDefaultsToCodexDesktop(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if got != state.FrontendModeCodexDesktop {
		t.Fatalf("mode = %q", got)
	}
}

func TestReadInvalidDefaultsToCodexDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install-mode.json")
	if err := os.WriteFile(path, []byte(`{"frontend_mode":"bad"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read invalid: %v", err)
	}
	if got != state.FrontendModeCodexDesktop {
		t.Fatalf("mode = %q", got)
	}
}

func TestWriteAndReadMinimalVSCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install-mode.json")
	if err := Write(path, state.FrontendModeMinimalVSCode); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != state.FrontendModeMinimalVSCode {
		t.Fatalf("mode = %q", got)
	}
}

func TestSyncStoreUsesInstallModeFile(t *testing.T) {
	dir := t.TempDir()
	modePath := filepath.Join(dir, "install-mode.json")
	statePath := filepath.Join(dir, "state.json")
	if err := Write(modePath, state.FrontendModeMinimalVSCode); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(statePath)
	if err := SyncStore(store, modePath); err != nil {
		t.Fatalf("SyncStore: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.FrontendMode != state.FrontendModeMinimalVSCode {
		t.Fatalf("FrontendMode = %q", got.FrontendMode)
	}
}
```

- [ ] **Step 5: Run install-mode tests and verify they fail**

Run:

```bash
go test ./internal/installmode -count=1
```

Expected: FAIL because package `internal/installmode` does not exist.

- [ ] **Step 6: Implement install-mode package**

Create `internal/installmode/installmode.go`:

```go
package installmode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/state"
)

type fileShape struct {
	FrontendMode state.FrontendMode `json:"frontend_mode"`
}

func Read(path string) (state.FrontendMode, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state.FrontendModeCodexDesktop, nil
	}
	if err != nil {
		return state.FrontendModeCodexDesktop, fmt.Errorf("read install mode: %w", err)
	}
	var f fileShape
	if err := json.Unmarshal(b, &f); err != nil {
		return state.FrontendModeCodexDesktop, nil
	}
	return state.NormalizeFrontendMode(f.FrontendMode), nil
}

func Write(path string, mode state.FrontendMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir install mode dir: %w", err)
	}
	b, err := json.MarshalIndent(fileShape{FrontendMode: state.NormalizeFrontendMode(mode)}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal install mode: %w", err)
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func SyncStore(store *state.Store, path string) error {
	mode, err := Read(path)
	if err != nil {
		return err
	}
	return store.Update(func(s *state.State) error {
		s.FrontendMode = state.NormalizeFrontendMode(mode)
		return nil
	})
}
```

- [ ] **Step 7: Run tests and commit**

Run:

```bash
go test ./internal/state ./internal/installmode -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/state/types.go internal/state/store.go internal/state/types_test.go internal/installmode
git commit -m "feat(state): persist frontend mode"
```

## Task 2: Add Codex Desktop Winget Support

**Files:**
- Create: `internal/codexdesktop/winget.go`
- Create: `internal/codexdesktop/detect.go`
- Create: `internal/codexdesktop/detect_windows.go`
- Create: `internal/codexdesktop/detect_other.go`
- Create: `internal/codexdesktop/install.go`
- Create: `internal/codexdesktop/launch.go`
- Create: `internal/codexdesktop/winget_test.go`
- Create: `internal/codexdesktop/install_test.go`
- Create: `internal/codexdesktop/launch_test.go`

- [ ] **Step 1: Add winget command tests**

Create `internal/codexdesktop/winget_test.go`:

```go
package codexdesktop

import (
	"errors"
	"strings"
	"testing"
)

func TestWingetInstallArgs(t *testing.T) {
	got := WingetInstallArgs()
	want := []string{"install", "Codex", "-s", "msstore", "--accept-source-agreements", "--accept-package-agreements"}
	if len(got) != len(want) {
		t.Fatalf("args len=%d want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d]=%q want %q; all=%v", i, got[i], want[i], got)
		}
	}
}

func TestClassifyWingetError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		out  string
		want string
	}{
		{name: "missing", err: ErrWingetNotFound, want: "Windows App Installer"},
		{name: "source", err: errors.New("exit 1"), out: "msstore source was not found", want: "Microsoft Store source"},
		{name: "network", err: errors.New("exit 1"), out: "network failure", want: "网络"},
		{name: "generic", err: errors.New("exit 7"), out: "plain failure", want: "winget install Codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyWingetError(tc.err, tc.out)
			if !strings.Contains(got.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", got.Error(), tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Add install and launch tests**

Create `internal/codexdesktop/install_test.go`:

```go
package codexdesktop

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEnsureInstalledSkipsWingetWhenDetected(t *testing.T) {
	calls := 0
	det, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			return Detected{Installed: true, Version: "1.0.0"}, nil
		},
		RunWinget: func(context.Context, []string) (string, error) {
			calls++
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if !det.Installed || det.Version != "1.0.0" {
		t.Fatalf("det=%+v", det)
	}
	if calls != 0 {
		t.Fatalf("winget called %d times", calls)
	}
}

func TestEnsureInstalledRunsWingetThenVerifies(t *testing.T) {
	detectCalls := 0
	var gotArgs []string
	det, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			detectCalls++
			if detectCalls == 1 {
				return Detected{Installed: false}, nil
			}
			return Detected{Installed: true, Version: "2.0.0"}, nil
		},
		RunWinget: func(_ context.Context, args []string) (string, error) {
			gotArgs = append([]string(nil), args...)
			return "installed", nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if !det.Installed || det.Version != "2.0.0" {
		t.Fatalf("det=%+v", det)
	}
	if strings.Join(gotArgs, " ") != "install Codex -s msstore --accept-source-agreements --accept-package-agreements" {
		t.Fatalf("args=%v", gotArgs)
	}
}

func TestEnsureInstalledClassifiesWingetFailure(t *testing.T) {
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) { return Detected{Installed: false}, nil },
		RunWinget: func(context.Context, []string) (string, error) {
			return "source msstore was not found", errors.New("exit 1")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "Microsoft Store source") {
		t.Fatalf("err=%v", err)
	}
}
```

Create `internal/codexdesktop/launch_test.go`:

```go
package codexdesktop

import (
	"context"
	"strings"
	"testing"
)

func TestThreadURLWithoutFolder(t *testing.T) {
	if got := ThreadURL(""); got != "codex://threads/new" {
		t.Fatalf("ThreadURL empty = %q", got)
	}
}

func TestThreadURLWithFolder(t *testing.T) {
	got := ThreadURL(`C:\Users\Test User\Project`)
	if !strings.HasPrefix(got, "codex://threads/new?path=") {
		t.Fatalf("url=%q", got)
	}
	if !strings.Contains(got, "Test+User") {
		t.Fatalf("folder path not encoded: %q", got)
	}
}

func TestLaunchUsesOpener(t *testing.T) {
	var opened string
	err := Launch(context.Background(), `C:\Project`, func(url string) error {
		opened = url
		return nil
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !strings.HasPrefix(opened, "codex://threads/new?path=") {
		t.Fatalf("opened=%q", opened)
	}
}
```

- [ ] **Step 3: Run codexdesktop tests and verify they fail**

Run:

```bash
go test ./internal/codexdesktop -count=1
```

Expected: FAIL because package `internal/codexdesktop` does not exist.

- [ ] **Step 4: Implement codexdesktop package**

Create `internal/codexdesktop/detect.go`:

```go
package codexdesktop

import "errors"

type Detected struct {
	Installed bool
	Version   string
}

var ErrNotFound = errors.New("Codex Desktop not found")

func Detect() (Detected, error) {
	return detectPlatform()
}
```

Create `internal/codexdesktop/detect_other.go`:

```go
//go:build !windows

package codexdesktop

func detectPlatform() (Detected, error) {
	return Detected{Installed: false}, ErrNotFound
}
```

Create `internal/codexdesktop/detect_windows.go`:

```go
//go:build windows

package codexdesktop

import (
	"os/exec"
	"strings"
)

func detectPlatform() (Detected, error) {
	script := `$paths = @('Registry::HKEY_CURRENT_USER\Software\Classes\codex\shell\open\command','Registry::HKEY_LOCAL_MACHINE\Software\Classes\codex\shell\open\command'); foreach ($p in $paths) { if (Test-Path $p) { Write-Output 'url-scheme'; exit 0 } }; $pkg = Get-AppxPackage | Where-Object { $_.Name -like '*Codex*' -or $_.PackageFullName -like '*Codex*' } | Select-Object -First 1; if ($pkg) { Write-Output $pkg.Version; exit 0 }; exit 1`
	out, err := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script).CombinedOutput()
	if err != nil {
		return Detected{Installed: false}, ErrNotFound
	}
	version := strings.TrimSpace(string(out))
	if version == "url-scheme" {
		version = ""
	}
	return Detected{Installed: true, Version: version}, nil
}
```

Create `internal/codexdesktop/winget.go`:

```go
package codexdesktop

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrWingetNotFound = errors.New("winget not found")

func WingetInstallArgs() []string {
	return []string{
		"install",
		"Codex",
		"-s",
		"msstore",
		"--accept-source-agreements",
		"--accept-package-agreements",
	}
}

func RequireWinget() error {
	if _, err := exec.LookPath("winget"); err != nil {
		return ErrWingetNotFound
	}
	return nil
}

func ClassifyWingetError(err error, output string) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(output)
	if errors.Is(err, ErrWingetNotFound) {
		return errors.New("未找到 winget；请安装或更新 Windows App Installer / Windows Package Manager 后重试")
	}
	if strings.Contains(lower, "source") || strings.Contains(lower, "msstore") {
		return fmt.Errorf("Microsoft Store source 不可用；请检查 Store 源、网络或企业策略。winget 输出: %s", strings.TrimSpace(output))
	}
	if strings.Contains(lower, "network") || strings.Contains(lower, "internet") || strings.Contains(lower, "connection") {
		return fmt.Errorf("网络不可用，无法通过 winget 安装 Codex Desktop。winget 输出: %s", strings.TrimSpace(output))
	}
	return fmt.Errorf("winget install Codex -s msstore 失败: %w。输出: %s", err, strings.TrimSpace(output))
}
```

Create `internal/codexdesktop/install.go`:

```go
package codexdesktop

import (
	"context"
	"fmt"
	"os/exec"
)

type Options struct {
	Detect    func() (Detected, error)
	RunWinget func(context.Context, []string) (string, error)
}

func EnsureInstalled(ctx context.Context, opts Options) (Detected, error) {
	detect := opts.Detect
	if detect == nil {
		detect = Detect
	}
	run := opts.RunWinget
	if run == nil {
		run = runWinget
	}
	if det, err := detect(); err == nil && det.Installed {
		return det, nil
	}
	out, err := run(ctx, WingetInstallArgs())
	if err != nil {
		return Detected{}, ClassifyWingetError(err, out)
	}
	det, err := detect()
	if err != nil || !det.Installed {
		return Detected{}, fmt.Errorf("Codex Desktop 安装后仍未检测到；winget 输出: %s", out)
	}
	return det, nil
}

func runWinget(ctx context.Context, args []string) (string, error) {
	if err := RequireWinget(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "winget", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
```

Create `internal/codexdesktop/launch.go`:

```go
package codexdesktop

import (
	"context"
	"net/url"

	"github.com/agentserver/agentserver-pkg/internal/browser"
)

type Opener func(string) error

func ThreadURL(folder string) string {
	if folder == "" {
		return "codex://threads/new"
	}
	q := url.Values{}
	q.Set("path", folder)
	return "codex://threads/new?" + q.Encode()
}

func Launch(ctx context.Context, folder string, opener Opener) error {
	_ = ctx
	if opener == nil {
		opener = browser.Open
	}
	return opener(ThreadURL(folder))
}
```

- [ ] **Step 5: Run tests and commit**

Run:

```bash
go test ./internal/codexdesktop -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/codexdesktop
git commit -m "feat: add codex desktop winget support"
```

## Task 3: Mode-Aware Backend Onboarding

**Files:**
- Modify: `internal/ui/orchestrator.go`
- Modify: `internal/ui/orchestrator_real.go`
- Modify: `internal/ui/server.go`
- Modify: `internal/ui/server_test.go`
- Modify: `internal/ui/orchestrator_real_test.go`

- [ ] **Step 1: Add failing server tests for generic frontend endpoints**

Append to `internal/ui/server_test.go`:

```go
func TestServerStateIncludesFrontendMode(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s SanitizedState
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.FrontendMode != "codex_desktop" {
		t.Fatalf("FrontendMode=%q", s.FrontendMode)
	}
	if s.FrontendName != "Codex Desktop" {
		t.Fatalf("FrontendName=%q", s.FrontendName)
	}
}

func TestServerFrontendInstallReportsErrorsOnSSE(t *testing.T) {
	srv := httptest.NewServer(NewServer(frontendInstallErrorOrchestrator{}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/step/frontend_install", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	eventsResp, err := http.Get(srv.URL + "/api/events?stream=" + body["stream_id"])
	if err != nil {
		t.Fatal(err)
	}
	defer eventsResp.Body.Close()

	scanner := bufio.NewScanner(eventsResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev ProgressEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Stage == "error" && strings.Contains(ev.Msg, "winget missing") {
			return
		}
	}
	t.Fatal("expected frontend install error event on SSE stream")
}

type frontendInstallErrorOrchestrator struct{ noopOrchestrator }

func (frontendInstallErrorOrchestrator) EnsureFrontend(context.Context, chan<- ProgressEvent) error {
	return errors.New("winget missing")
}
```

- [ ] **Step 2: Add failing orchestrator tests for Codex Desktop mode**

Append to `internal/ui/orchestrator_real_test.go`:

```go
func TestEnsureFrontendCodexDesktopSkipsVSCode(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := &realOrchestrator{d: Deps{
		State: store,
		CodexDesktopEnsure: func(ctx context.Context) (codexdesktop.Detected, error) {
			return codexdesktop.Detected{Installed: true, Version: "9.9.9"}, nil
		},
	}}
	if err := r.EnsureFrontend(context.Background(), nil); err != nil {
		t.Fatalf("EnsureFrontend: %v", err)
	}
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("codex_desktop_installed") {
		t.Fatalf("codex_desktop_installed not marked")
	}
	if s.VSCode.Path != "" {
		t.Fatalf("VSCode.Path should remain empty in Codex Desktop mode, got %q", s.VSCode.Path)
	}
}

func TestConfigureCodexDesktopWritesSharedConfigOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "")
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Set("modelserver_api_key", "desktop-token"); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := &realOrchestrator{d: Deps{
		State:           store,
		Secrets:         sec,
		CodexConfigPath: filepath.Join(dir, ".codex", "config.toml"),
	}}
	if err := r.ConfigureFrontend(context.Background()); err != nil {
		t.Fatalf("ConfigureFrontend: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `model_provider = "modelserver"`) {
		t.Fatalf("config missing modelserver provider:\n%s", b)
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "desktop-token" {
		t.Fatalf("OPENAI_API_KEY=%q", got)
	}
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("codex_desktop_configured") {
		t.Fatalf("codex_desktop_configured not marked")
	}
}

func TestLaunchAndShutdownCodexDesktopUsesDeepLink(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var opened string
	r := &realOrchestrator{d: Deps{
		State: store,
		CodexDesktopOpen: func(url string) error {
			opened = url
			return nil
		},
	}}
	if err := r.LaunchAndShutdown(context.Background()); err != nil {
		t.Fatalf("LaunchAndShutdown: %v", err)
	}
	if opened != "codex://threads/new" {
		t.Fatalf("opened=%q", opened)
	}
}
```

Add import:

```go
"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
```

- [ ] **Step 3: Run backend tests and verify they fail**

Run:

```bash
go test ./internal/ui -run 'TestServerStateIncludesFrontendMode|TestServerFrontendInstallReportsErrorsOnSSE|TestEnsureFrontendCodexDesktop|TestConfigureCodexDesktop|TestLaunchAndShutdownCodexDesktop' -count=1
```

Expected: FAIL because generic frontend methods and Codex Desktop deps do not exist.

- [ ] **Step 4: Update orchestrator contract and state response**

In `internal/ui/orchestrator.go`, add methods and fields:

```go
	EnsureFrontend(ctx context.Context, progress chan<- ProgressEvent) error
	ConfigureFrontend(ctx context.Context) error
```

Keep `EnsureVSCode` and `ConfigureVSCode` on the interface as compatibility methods for existing tests and wrappers.

Update `SanitizedState`:

```go
	FrontendMode          string   `json:"frontend_mode"`
	FrontendName          string   `json:"frontend_name"`
	CodexDesktopInstalled bool     `json:"codex_desktop_installed,omitempty"`
	CodexDesktopVersion   string   `json:"codex_desktop_version,omitempty"`
```

Update `noopOrchestrator.State`:

```go
return SanitizedState{
	SchemaVersion:    1,
	InstallID:        "noop",
	OnboardingStatus: string(state.StatusPending),
	FrontendMode:     string(state.FrontendModeCodexDesktop),
	FrontendName:     "Codex Desktop",
}, nil
```

Add noop methods:

```go
func (noopOrchestrator) EnsureFrontend(context.Context, chan<- ProgressEvent) error { return nil }
func (noopOrchestrator) ConfigureFrontend(context.Context) error                    { return nil }
```

Import `internal/state`.

- [ ] **Step 5: Add Codex Desktop deps and mode dispatch**

In `internal/ui/orchestrator_real.go`, add imports:

```go
"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
```

Add to `Deps`:

```go
	CodexDesktopEnsure func(context.Context) (codexdesktop.Detected, error)
	CodexDesktopOpen   func(string) error
```

Add helpers near `State`:

```go
func frontendName(mode state.FrontendMode) string {
	if state.NormalizeFrontendMode(mode) == state.FrontendModeMinimalVSCode {
		return "极简界面"
	}
	return "Codex Desktop"
}

func (r *realOrchestrator) frontendMode() (state.FrontendMode, error) {
	s, err := r.d.State.Load()
	if err != nil {
		return state.FrontendModeCodexDesktop, err
	}
	return state.NormalizeFrontendMode(s.FrontendMode), nil
}
```

Update `State()` return fields:

```go
mode := state.NormalizeFrontendMode(s.FrontendMode)
return SanitizedState{
	SchemaVersion:          s.SchemaVersion,
	InstallID:              s.InstallID,
	OnboardingStatus:       string(s.Onboarding.Status),
	CompletedSteps:         append([]string(nil), s.Onboarding.CompletedSteps...),
	LastError:              s.Onboarding.LastError,
	FrontendMode:           string(mode),
	FrontendName:           frontendName(mode),
	ModelserverProjectID:   s.Modelserver.ProjectID,
	AgentserverWorkspaceID: s.Agentserver.WorkspaceID,
	VSCodePath:             s.VSCode.Path,
	VSCodeVersion:          s.VSCode.Version,
	CodexDesktopInstalled:  s.CodexDesktop.Installed,
	CodexDesktopVersion:    s.CodexDesktop.Version,
}, nil
```

Add dispatch methods:

```go
func (r *realOrchestrator) EnsureFrontend(ctx context.Context, ch chan<- ProgressEvent) error {
	mode, err := r.frontendMode()
	if err != nil {
		return err
	}
	if mode == state.FrontendModeMinimalVSCode {
		return r.EnsureVSCode(ctx, ch)
	}
	return r.EnsureCodexDesktop(ctx, ch)
}

func (r *realOrchestrator) ConfigureFrontend(ctx context.Context) error {
	mode, err := r.frontendMode()
	if err != nil {
		return err
	}
	if mode == state.FrontendModeMinimalVSCode {
		return r.ConfigureVSCode(ctx)
	}
	return r.ConfigureCodexDesktop(ctx)
}
```

Add Codex Desktop methods:

```go
func (r *realOrchestrator) EnsureCodexDesktop(ctx context.Context, ch chan<- ProgressEvent) error {
	ensure := r.d.CodexDesktopEnsure
	if ensure == nil {
		ensure = func(ctx context.Context) (codexdesktop.Detected, error) {
			return codexdesktop.EnsureInstalled(ctx, codexdesktop.Options{})
		}
	}
	if ch != nil {
		ch <- ProgressEvent{Stage: "checking", Msg: "正在检查 Codex Desktop..."}
	}
	det, err := ensure(ctx)
	if err != nil {
		return err
	}
	if ch != nil {
		ch <- ProgressEvent{Stage: "verified", Msg: "已检测到 Codex Desktop"}
	}
	return r.d.State.Update(func(s *state.State) error {
		s.CodexDesktop.Installed = true
		s.CodexDesktop.Version = det.Version
		s.CodexDesktop.InstalledByUs = true
		s.Onboarding.AddCompleted("codex_desktop_installed")
		return nil
	})
}

func (r *realOrchestrator) ConfigureCodexDesktop(ctx context.Context) error {
	if err := r.configureSharedCodex(ctx); err != nil {
		return err
	}
	return r.d.State.Update(func(s *state.State) error {
		s.Onboarding.AddCompleted("codex_desktop_configured")
		return nil
	})
}
```

Extract shared config from `ConfigureVSCode` into:

```go
func (r *realOrchestrator) configureSharedCodex(ctx context.Context) error {
	_ = ctx
	if err := codex.UpdateConfig(r.d.CodexConfigPath, codex.ModelserverSettings()); err != nil {
		return err
	}
	if r.d.Secrets != nil {
		apiKey, err := r.d.Secrets.Get("modelserver_api_key")
		if err == nil {
			_ = env.PersistUserEnv("OPENAI_API_KEY", apiKey)
			_ = os.Setenv("OPENAI_API_KEY", apiKey)
		}
	}
	if r.d.TokenRefresherExePath != "" {
		_ = tokenrefresh.StartDaemon(r.d.TokenRefresherExePath)
	}
	return nil
}
```

Replace the matching config/env/refresher block in `ConfigureVSCode` with:

```go
if err := r.configureSharedCodex(ctx); err != nil {
	return err
}
```

Update `LaunchAndShutdown` to switch by mode:

```go
	mode := state.NormalizeFrontendMode(s.FrontendMode)
	if mode == state.FrontendModeCodexDesktop {
		open := r.d.CodexDesktopOpen
		if open == nil {
			open = func(u string) error { return codexdesktop.Launch(ctx, "", nil) }
		}
		if err := open(codexdesktop.ThreadURL("")); err != nil {
			return fmt.Errorf("launch Codex Desktop: %w", err)
		}
		if r.d.Shutdown != nil {
			r.d.Shutdown()
		}
		return nil
	}
```

Leave the existing VS Code launch branch after this block.

- [ ] **Step 6: Update server endpoints**

In `internal/ui/server.go`, add routes:

```go
mux.HandleFunc("/api/step/frontend_install", s.handleFrontendInstall)
mux.HandleFunc("/api/step/frontend_configure", s.handleFrontendConfigure)
mux.HandleFunc("/api/launch", s.handleLaunch)
```

Keep old routes but point them to the new handlers:

```go
mux.HandleFunc("/api/step/vscode_install", s.handleFrontendInstall)
mux.HandleFunc("/api/step/vscode_configure", s.handleFrontendConfigure)
mux.HandleFunc("/api/launch-vscode", s.handleLaunch)
```

Rename handlers:

```go
func (s *server) handleFrontendInstall(w http.ResponseWriter, r *http.Request) {
	streamID := s.sse.newStream()
	go func() {
		defer s.sse.close(streamID)
		ch := s.sse.channel(streamID)
		if err := s.o.EnsureFrontend(context.Background(), ch); err != nil {
			ch <- ProgressEvent{Stage: "error", Msg: err.Error()}
		}
	}()
	writeJSON(w, 200, map[string]string{"stream_id": streamID})
}

func (s *server) handleFrontendConfigure(w http.ResponseWriter, r *http.Request) {
	if err := s.o.ConfigureFrontend(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "success"})
}

func (s *server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	if err := s.o.LaunchAndShutdown(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "launching"})
}
```

- [ ] **Step 7: Run backend tests and commit**

Run:

```bash
go test ./internal/ui -run 'TestServerStateIncludesFrontendMode|TestServerFrontendInstallReportsErrorsOnSSE|TestEnsureFrontendCodexDesktop|TestConfigureCodexDesktop|TestLaunchAndShutdownCodexDesktop|TestServerVSCodeInstallReportsErrorsOnSSE|TestConfigureVSCode' -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/ui/orchestrator.go internal/ui/orchestrator_real.go internal/ui/server.go internal/ui/server_test.go internal/ui/orchestrator_real_test.go
git commit -m "feat(ui): route onboarding by frontend mode"
```

## Task 4: Mode-Aware Launcher, Folder Opening, and Agentctl

**Files:**
- Modify: `cmd/launcher/main.go`
- Modify: `cmd/launcher/main_test.go`
- Modify: `cmd/open-folder/main.go`
- Create: `cmd/open-folder/main_test.go`
- Modify: `cmd/agentctl/cmd_doctor.go`
- Modify: `cmd/agentctl/doctor_test.go`
- Modify: `cmd/agentctl/cmd_test_subcommands.go`
- Modify: `cmd/agentctl/main.go`

- [ ] **Step 1: Add failing launcher tests**

Append to `cmd/launcher/main_test.go`:

```go
func TestLaunchCompletedCodexDesktopWritesConfigAndOpensDeepLink(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		CodexConfigFile: filepath.Join(dir, ".codex", "config.toml"),
	}
	var opened string
	err := launchCompletedCodexDesktop(context.Background(), p, nil, "", func(url string) error {
		opened = url
		return nil
	})
	if err != nil {
		t.Fatalf("launchCompletedCodexDesktop: %v", err)
	}
	if opened != "codex://threads/new" {
		t.Fatalf("opened=%q", opened)
	}
	b, err := os.ReadFile(p.CodexConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `model_provider = "modelserver"`) {
		t.Fatalf("config missing modelserver provider:\n%s", b)
	}
}
```

- [ ] **Step 2: Add failing open-folder tests**

Create `cmd/open-folder/main_test.go`:

```go
package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/paths"
)

func TestOpenFolderCodexDesktopUsesFolderDeepLink(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{CodexConfigFile: filepath.Join(dir, ".codex", "config.toml")}
	var opened string
	err := openFolderCodexDesktop(context.Background(), p, `C:\Project Folder`, nil, "", func(url string) error {
		opened = url
		return nil
	})
	if err != nil {
		t.Fatalf("openFolderCodexDesktop: %v", err)
	}
	if !strings.HasPrefix(opened, "codex://threads/new?path=") {
		t.Fatalf("opened=%q", opened)
	}
	if !strings.Contains(opened, "Project+Folder") {
		t.Fatalf("path not encoded: %q", opened)
	}
}
```

- [ ] **Step 3: Run command tests and verify failure**

Run:

```bash
go test ./cmd/launcher ./cmd/open-folder -run 'TestLaunchCompletedCodexDesktop|TestOpenFolderCodexDesktop' -count=1
```

Expected: FAIL because Codex Desktop launch helpers do not exist.

- [ ] **Step 4: Implement launcher mode sync and completed launch**

In `cmd/launcher/main.go`, add imports:

```go
"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
"github.com/agentserver/agentserver-pkg/internal/installmode"
```

In `run()`, compute install dir before loading state and sync mode:

```go
exe, _ := os.Executable()
installDir := osDir(exe)
store := state.NewStore(p.StateFile)
if err := installmode.SyncStore(store, joinExe(installDir, "install-mode.json")); err != nil {
	return err
}
s, err := store.Load()
```

Replace the completed branch:

```go
if s.Onboarding.Status == state.StatusComplete {
	return launchCompletedFrontend(context.Background(), s, p, secrets.New(p.SecretsFile),
		joinExe(installDir, "token-refresher.exe"), joinExe(installDir, "agentserver-vscode.vsix"), nil)
}
```

Add helpers:

```go
func launchCompletedFrontend(ctx context.Context, s *state.State, p paths.Paths, sec secrets.Store, tokenRefresherExe string, embeddedVSIXPath string, codexOpen codexdesktop.Opener) error {
	if state.NormalizeFrontendMode(s.FrontendMode) == state.FrontendModeMinimalVSCode {
		if s.VSCode.Path == "" {
			return fmt.Errorf("VS Code path unknown; rerun onboarding")
		}
		return launchCompletedInstall(ctx, s.VSCode.Path, p, sec, tokenRefresherExe, embeddedVSIXPath)
	}
	return launchCompletedCodexDesktop(ctx, p, sec, tokenRefresherExe, codexOpen)
}

func launchCompletedCodexDesktop(ctx context.Context, p paths.Paths, sec secrets.Store, tokenRefresherExe string, opener codexdesktop.Opener) error {
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverSettings()); err != nil {
		return err
	}
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}
	return codexdesktop.Launch(ctx, "", opener)
}
```

- [ ] **Step 5: Implement open-folder mode switch**

In `cmd/open-folder/main.go`, add imports:

```go
"github.com/agentserver/agentserver-pkg/internal/codex"
"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
```

In `main()`, keep the existing `tokenRefresherExe` and `embeddedVSIXPath` calculation, then add the mode branch immediately before the existing `if s.VSCode.Path == ""` check:

```go
if state.NormalizeFrontendMode(s.FrontendMode) == state.FrontendModeCodexDesktop {
	if err := openFolderCodexDesktop(context.Background(), p, folder, secrets.New(p.SecretsFile), tokenRefresherExe, nil); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("opened %s with Codex Desktop\n", folder)
	return
}
```

Add helper:

```go
func openFolderCodexDesktop(ctx context.Context, p paths.Paths, folder string, sec secrets.Store, tokenRefresherExe string, opener codexdesktop.Opener) error {
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverSettings()); err != nil {
		return err
	}
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}
	return codexdesktop.Launch(ctx, folder, opener)
}
```

- [ ] **Step 6: Update agentctl doctor and test helpers**

In `cmd/agentctl/cmd_doctor.go`, add frontend lines:

```go
mode := state.NormalizeFrontendMode(s.FrontendMode)
fmt.Fprintf(w, "  frontend: %s\n", mode)
fmt.Fprintf(w, "  codex_desktop: installed=%t version=%s\n", s.CodexDesktop.Installed, s.CodexDesktop.Version)
```

Update `cmd/agentctl/doctor_test.go` state:

```go
FrontendMode: state.FrontendModeCodexDesktop,
CodexDesktop: state.CodexDesktopState{Installed: true, Version: "1.0.0"},
```

Add expected substrings:

```go
"frontend: codex_desktop",
"codex_desktop: installed=true",
```

In `cmd/agentctl/main.go`, add test subcommands:

```go
case "test-install-codex-desktop":
	runTestInstallCodexDesktop()
case "test-configure-codex-desktop":
	runTestConfigureCodexDesktop()
```

In `cmd/agentctl/cmd_test_subcommands.go`, add imports:

```go
"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
"github.com/agentserver/agentserver-pkg/internal/secrets"
```

Add functions:

```go
func runTestInstallCodexDesktop() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	det, err := codexdesktop.EnsureInstalled(ctx, codexdesktop.Options{})
	if err != nil {
		die(err)
	}
	store := state.NewStore(p.StateFile)
	_ = store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		s.CodexDesktop.Installed = true
		s.CodexDesktop.Version = det.Version
		s.CodexDesktop.InstalledByUs = true
		s.Onboarding.AddCompleted("codex_desktop_installed")
		return nil
	})
	fmt.Printf("Codex Desktop installed (version %s)\n", det.Version)
}

func runTestConfigureCodexDesktop() {
	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverSettings()); err != nil {
		die(err)
	}
	sec := secrets.New(p.SecretsFile)
	if err := sec.Set("modelserver_api_key", "ms-dummy-test-key"); err != nil {
		die(err)
	}
	if err := env.PersistUserEnv("OPENAI_API_KEY", "ms-dummy-test-key"); err != nil {
		die(err)
	}
	store := state.NewStore(p.StateFile)
	_ = store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		s.Onboarding.AddCompleted("codex_desktop_configured")
		return nil
	})
	fmt.Printf("wrote codex config: %s\n", p.CodexConfigFile)
}
```

Update `runTestMarkComplete` to include Codex Desktop tokens when mode is default:

```go
mode := state.NormalizeFrontendMode(s.FrontendMode)
if mode == state.FrontendModeMinimalVSCode {
	for _, st := range []string{"modelserver_login", "agentserver_login", "vscode_installed", "vscode_configured", "shortcuts_created"} {
		s.Onboarding.AddCompleted(st)
	}
} else {
	for _, st := range []string{"modelserver_login", "agentserver_login", "codex_desktop_installed", "codex_desktop_configured", "shortcuts_created"} {
		s.Onboarding.AddCompleted(st)
	}
}
```

- [ ] **Step 7: Run command tests and commit**

Run:

```bash
go test ./cmd/launcher ./cmd/open-folder ./cmd/agentctl -count=1
```

Expected: PASS.

Commit:

```bash
git add cmd/launcher cmd/open-folder cmd/agentctl
git commit -m "feat(cmd): launch frontend by mode"
```

## Task 5: Mode-Aware Vue Onboarding

**Files:**
- Modify: `internal/ui/web/src/api.ts`
- Modify: `internal/ui/web/src/stepConfig.ts`
- Modify: `internal/ui/web/src/composables/useOnboarding.ts`
- Modify: `internal/ui/web/src/components/ProgressStep.vue`
- Modify: `internal/ui/web/src/components/ActionStep.vue`
- Modify: `internal/ui/web/src/components/SuccessBanner.vue`
- Modify: `internal/ui/web/src/App.vue`
- Modify: `internal/ui/web/src/__tests__/useOnboarding.spec.ts`
- Modify: `internal/ui/web/src/__tests__/SuccessBanner.spec.ts`
- Modify: `internal/ui/web/src/__tests__/ProgressStep.spec.ts`

- [ ] **Step 1: Add failing step config tests**

Create `internal/ui/web/src/__tests__/stepConfig.spec.ts`:

```ts
import { describe, expect, it } from 'vitest';
import { stepsForMode, completedMapForMode } from '../stepConfig';

describe('stepConfig', () => {
  it('uses Codex Desktop steps by default', () => {
    expect(stepsForMode(undefined).map(s => s.id)).toEqual([
      'modelserver_login',
      'agentserver_login',
      'codex_desktop_install',
      'codex_desktop_configure',
      'finalize',
    ]);
  });

  it('uses minimal VS Code steps when selected', () => {
    expect(stepsForMode('minimal_vscode').map(s => s.id)).toEqual([
      'modelserver_login',
      'agentserver_login',
      'vscode_install',
      'vscode_configure',
      'finalize',
    ]);
  });

  it('maps completed tokens by mode', () => {
    expect(completedMapForMode('codex_desktop').codex_desktop_installed).toBe('codex_desktop_install');
    expect(completedMapForMode('codex_desktop').vscode_installed).toBeUndefined();
    expect(completedMapForMode('minimal_vscode').vscode_installed).toBe('vscode_install');
    expect(completedMapForMode('minimal_vscode').codex_desktop_installed).toBeUndefined();
  });
});
```

- [ ] **Step 2: Update useOnboarding tests for both modes**

In `internal/ui/web/src/__tests__/useOnboarding.spec.ts`, update existing mock states by adding `frontend_mode: 'minimal_vscode'` where the assertions expect VS Code step ids. Add a new test:

```ts
  it('initializes Codex Desktop steps from server mode', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1,
      install_id: 'x',
      frontend_mode: 'codex_desktop',
      frontend_name: 'Codex Desktop',
      onboarding_status: 'pending',
      completed_steps: ['modelserver_login', 'agentserver_login'],
    });
    const o = useOnboarding();
    await o.init();
    expect(o.steps.value.map(s => s.id)).toEqual([
      'modelserver_login',
      'agentserver_login',
      'codex_desktop_install',
      'codex_desktop_configure',
      'finalize',
    ]);
    expect(o.current.value?.id).toBe('codex_desktop_install');
    expect(o.frontendName.value).toBe('Codex Desktop');
  });
```

- [ ] **Step 3: Update SuccessBanner tests**

Replace `internal/ui/web/src/__tests__/SuccessBanner.spec.ts` with:

```ts
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import SuccessBanner from '../components/SuccessBanner.vue';

describe('SuccessBanner', () => {
  it('renders success text', () => {
    const w = mount(SuccessBanner, { props: { frontendName: 'Codex Desktop' } });
    expect(w.text()).toContain('全部完成');
  });

  it('emits "launch" when Codex Desktop button clicked', async () => {
    const w = mount(SuccessBanner, { props: { frontendName: 'Codex Desktop' } });
    const btn = w.findAll('button').find(b => b.text().includes('打开 Codex Desktop'));
    expect(btn).toBeDefined();
    await btn!.trigger('click');
    expect(w.emitted('launch')).toBeTruthy();
  });

  it('renders pending message when launching Codex Desktop', async () => {
    const w = mount(SuccessBanner, { props: { launching: true, frontendName: 'Codex Desktop' } });
    expect(w.text()).toContain('Codex Desktop 启动中');
  });
});
```

- [ ] **Step 4: Update ProgressStep tests**

In `internal/ui/web/src/__tests__/ProgressStep.spec.ts`, replace both occurrences of:

```ts
vi.spyOn(api, 'startVSCodeInstall').mockResolvedValue({ stream_id: 's1' });
```

with:

```ts
vi.spyOn(api, 'startFrontendInstall').mockResolvedValue({ stream_id: 's1' });
```

Update `makeOnboarding()` to include the new handle fields:

```ts
    frontendMode: ref('codex_desktop'),
    frontendName: ref('Codex Desktop'),
```

Keep `makeStep()` as `vscode_install`; this test is about generic progress behavior and should still pass for the compatibility step id.

- [ ] **Step 5: Run frontend tests and verify failure**

Run:

```bash
cd internal/ui/web && npm test -- --run stepConfig useOnboarding SuccessBanner
```

Expected: FAIL because mode-aware functions and props do not exist.

- [ ] **Step 6: Implement mode-aware API and steps**

In `internal/ui/web/src/api.ts`, extend `ServerState`:

```ts
  frontend_mode?: 'codex_desktop' | 'minimal_vscode';
  frontend_name?: string;
  codex_desktop_installed?: boolean;
  codex_desktop_version?: string;
```

Replace API helpers:

```ts
export const startFrontendInstall = () =>
  request<StreamHandle>('/api/step/frontend_install', { method: 'POST' });

export const configureFrontend = () =>
  request<{ state: 'success' }>('/api/step/frontend_configure', { method: 'POST' });

export const launchFrontend = () =>
  request<{ state: 'launching' }>('/api/launch', { method: 'POST' });
```

Keep compatibility exports:

```ts
export const startVSCodeInstall = startFrontendInstall;
export const configureVSCode = configureFrontend;
export const launchVSCode = launchFrontend;
```

Replace `internal/ui/web/src/stepConfig.ts` with:

```ts
export type StepKind = 'oauth' | 'progress' | 'action';
export type FrontendMode = 'codex_desktop' | 'minimal_vscode';

export interface StepDef {
  id: string;
  label: string;
  kind: StepKind;
  autoStart: boolean;
}

const CODEX_DESKTOP_STEPS: ReadonlyArray<StepDef> = [
  { id: 'modelserver_login',       label: '登录 modelserver',   kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login',       label: '登录 agentserver',   kind: 'oauth',    autoStart: false },
  { id: 'codex_desktop_install',   label: '安装 Codex Desktop', kind: 'progress', autoStart: true  },
  { id: 'codex_desktop_configure', label: '配置 Codex Desktop', kind: 'action',   autoStart: true  },
  { id: 'finalize',                label: '完成配置',           kind: 'action',   autoStart: false },
];

const MINIMAL_VSCODE_STEPS: ReadonlyArray<StepDef> = [
  { id: 'modelserver_login', label: '登录 modelserver', kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login', label: '登录 agentserver', kind: 'oauth',    autoStart: false },
  { id: 'vscode_install',    label: '安装极简界面',     kind: 'progress', autoStart: true  },
  { id: 'vscode_configure',  label: '准备极简界面',     kind: 'action',   autoStart: true  },
  { id: 'finalize',          label: '完成配置',         kind: 'action',   autoStart: false },
];

export function normalizeMode(mode?: string | null): FrontendMode {
  return mode === 'minimal_vscode' ? 'minimal_vscode' : 'codex_desktop';
}

export function stepsForMode(mode?: string | null): ReadonlyArray<StepDef> {
  return normalizeMode(mode) === 'minimal_vscode' ? MINIMAL_VSCODE_STEPS : CODEX_DESKTOP_STEPS;
}

export function completedMapForMode(mode?: string | null): Record<string, string> {
  if (normalizeMode(mode) === 'minimal_vscode') {
    return {
      modelserver_login: 'modelserver_login',
      agentserver_login: 'agentserver_login',
      vscode_installed: 'vscode_install',
      vscode_configured: 'vscode_configure',
      shortcuts_created: 'finalize',
    };
  }
  return {
    modelserver_login: 'modelserver_login',
    agentserver_login: 'agentserver_login',
    codex_desktop_installed: 'codex_desktop_install',
    codex_desktop_configured: 'codex_desktop_configure',
    shortcuts_created: 'finalize',
  };
}
```

- [ ] **Step 7: Implement useOnboarding mode state**

In `internal/ui/web/src/composables/useOnboarding.ts`, replace the fixed `STEPS` import with:

```ts
import { stepsForMode, completedMapForMode, normalizeMode, type StepDef, type FrontendMode } from '../stepConfig';
```

Add to `OnboardingHandle`:

```ts
  frontendMode: Ref<FrontendMode>;
  frontendName: Ref<string>;
```

Initialize:

```ts
  const frontendMode = ref<FrontendMode>('codex_desktop');
  const frontendName = ref('Codex Desktop');
  const steps: Ref<StepInstance[]> = ref(
    stepsForMode(frontendMode.value).map(s => ({ ...s, runtime: { status: 'pending' as StepStatus } })),
  );
```

Add function:

```ts
  function setFrontend(modeInput?: string | null, nameInput?: string) {
    const nextMode = normalizeMode(modeInput);
    const nextDefs = stepsForMode(nextMode);
    const currentIds = steps.value.map(s => s.id).join(',');
    const nextIds = nextDefs.map(s => s.id).join(',');
    frontendMode.value = nextMode;
    frontendName.value = nameInput || (nextMode === 'minimal_vscode' ? '极简界面' : 'Codex Desktop');
    if (currentIds !== nextIds) {
      steps.value = nextDefs.map(s => ({ ...s, runtime: { status: 'pending' as StepStatus } }));
    }
  }
```

In `syncFromServer()`, replace `COMPLETED_MAP` usage:

```ts
    const completedMap = completedMapForMode(frontendMode.value);
    for (const token of Array.from(completed.value)) {
      const id = completedMap[token];
      if (id) completedIds.add(id);
    }
```

In `refreshState()`, before setting completed:

```ts
      setFrontend(s.frontend_mode, s.frontend_name);
```

Return `frontendMode` and `frontendName`.

- [ ] **Step 8: Update components to use generic APIs**

In `ProgressStep.vue`, replace `api.startVSCodeInstall()` with:

```ts
const handle = await api.startFrontendInstall();
```

Replace error text:

```ts
streamError.value = ev.msg || '安装失败';
```

In `ActionStep.vue`, replace the branch:

```ts
if (props.step.id === 'vscode_configure' || props.step.id === 'codex_desktop_configure') {
  await api.configureFrontend();
} else if (props.step.id === 'finalize') {
  await api.finalize();
} else {
  throw new api.OnboardingError(`ActionStep doesn't know step ${props.step.id}`);
}
```

In `SuccessBanner.vue`, add prop:

```ts
frontendName: string;
```

Update text:

```vue
<p class="msg">
  配置已就绪。可双击桌面快捷方式启动，或者：
</p>
<el-button type="primary" @click="emit('launch')">打开 {{ frontendName }}</el-button>
<span>{{ frontendName }} 启动中，此窗口即将关闭…</span>
```

In `App.vue`, rename function to `launchFrontend`, use `api.launchFrontend()`, error text `启动失败:`, title `星池指挥官配置向导`, and pass:

```vue
<SuccessBanner
  v-if="onboarding.isComplete.value"
  :launching="launching"
  :frontend-name="onboarding.frontendName.value"
  @launch="launchFrontend"
/>
```

- [ ] **Step 9: Run frontend tests and commit**

Run:

```bash
cd internal/ui/web && npm test
```

Expected: PASS.

Run:

```bash
cd internal/ui/web && npm run build
```

Expected: PASS and `internal/ui/assets/dist/` updates.

Commit:

```bash
git add internal/ui/web/src internal/ui/assets/dist
git commit -m "feat(ui): make onboarding steps frontend-aware"
```

## Task 6: Windows Packaging Scripts and Installer Selection

**Files:**
- Create: `packaging/windows/ensure-codex-desktop.ps1`
- Create: `packaging/windows/write-install-mode.ps1`
- Modify: `packaging/windows/install.ps1`
- Modify: `packaging/windows/installer.iss`
- Modify: `scripts/package-windows.sh`
- Modify: `scripts/package-windows-zip.sh`
- Modify: `internal/vscode/install_test.go`

- [ ] **Step 1: Add failing packaging tests**

In `internal/vscode/install_test.go`, extend `TestWindowsInstallScriptsIncludeVSCodeInstaller` entries.

For `install.ps1`, add wants:

```go
"[switch]$MinimalVSCode",
"ensure-codex-desktop.ps1",
"write-install-mode.ps1",
"codex_desktop",
"minimal_vscode",
```

For `installer.iss`, add wants:

```go
"minimalvscode",
"ensure-codex-desktop.ps1",
"write-install-mode.ps1",
"ShouldInstallCodexDesktop",
"codex_desktop",
"minimal_vscode",
```

For `package-windows.sh`, add:

```go
"packaging/windows/ensure-codex-desktop.ps1",
"packaging/windows/write-install-mode.ps1",
```

For `package-windows-zip.sh`, add:

```go
"packaging/windows/ensure-codex-desktop.ps1",
"packaging/windows/write-install-mode.ps1",
```

In `TestWindowsPowerShellScriptsUseUTF8BOM`, add paths:

```go
"../../packaging/windows/ensure-codex-desktop.ps1",
"../../packaging/windows/write-install-mode.ps1",
```

Add a new test:

```go
func TestEnsureCodexDesktopScriptUsesWingetMsstore(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-codex-desktop.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"winget",
		"install",
		"Codex",
		"-s",
		"msstore",
		"--accept-source-agreements",
		"--accept-package-agreements",
		"Test-CodexDesktopInstalled",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-codex-desktop.ps1 missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run packaging tests and verify failure**

Run:

```bash
go test ./internal/vscode -run 'TestWindowsInstallScriptsIncludeVSCodeInstaller|TestWindowsPowerShellScriptsUseUTF8BOM|TestEnsureCodexDesktopScriptUsesWingetMsstore' -count=1
```

Expected: FAIL because new scripts and installer entries do not exist.

- [ ] **Step 3: Create PowerShell scripts with UTF-8 BOM**

Create `packaging/windows/ensure-codex-desktop.ps1` with UTF-8 BOM:

```powershell
param()

$ErrorActionPreference = 'Stop'

function Set-ScriptOutputEncoding {
    try {
        $utf8 = New-Object System.Text.UTF8Encoding $false
        [Console]::OutputEncoding = $utf8
        $script:OutputEncoding = $utf8
        & chcp.com 65001 > $null 2>$null
    } catch {
    }
}

Set-ScriptOutputEncoding

function Write-Step([string]$Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Test-CodexDesktopInstalled {
    $schemePaths = @(
        'Registry::HKEY_CURRENT_USER\Software\Classes\codex\shell\open\command',
        'Registry::HKEY_LOCAL_MACHINE\Software\Classes\codex\shell\open\command'
    )
    foreach ($p in $schemePaths) {
        if (Test-Path $p) { return $true }
    }
    try {
        $pkg = Get-AppxPackage | Where-Object {
            $_.Name -like '*Codex*' -or $_.PackageFullName -like '*Codex*'
        } | Select-Object -First 1
        if ($pkg) { return $true }
    } catch {
    }
    return $false
}

function Get-WingetPath {
    $cmd = Get-Command winget.exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    return $null
}

function Invoke-CodexDesktopWingetInstall {
    $winget = Get-WingetPath
    if (-not $winget) {
        throw "未找到 winget；请安装或更新 Windows App Installer / Windows Package Manager 后重试。"
    }
    $args = @(
        'install',
        'Codex',
        '-s',
        'msstore',
        '--accept-source-agreements',
        '--accept-package-agreements'
    )
    Write-Step "Running winget install Codex -s msstore..."
    & $winget @args
    if ($LASTEXITCODE -ne 0) {
        throw "winget install Codex -s msstore failed with exit code $LASTEXITCODE"
    }
}

Write-Step "Checking for Codex Desktop..."
if (Test-CodexDesktopInstalled) {
    Write-Step "Detected existing Codex Desktop; skipping install."
    exit 0
}

Invoke-CodexDesktopWingetInstall

Write-Step "Verifying Codex Desktop installation..."
if (-not (Test-CodexDesktopInstalled)) {
    throw "Codex Desktop 安装完成后仍未检测到。"
}
Write-Step "Codex Desktop is ready."
```

Create `packaging/windows/write-install-mode.ps1` with UTF-8 BOM:

```powershell
param(
    [Parameter(Mandatory=$true)]
    [ValidateSet('codex_desktop', 'minimal_vscode')]
    [string]$Mode,

    [string]$Path = (Join-Path $PSScriptRoot 'install-mode.json')
)

$ErrorActionPreference = 'Stop'

$dir = Split-Path -Parent $Path
if ($dir -and -not (Test-Path $dir)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}

$json = @{
    frontend_mode = $Mode
} | ConvertTo-Json -Depth 2

Set-Content -Path $Path -Value $json -Encoding UTF8
Write-Host "Wrote frontend mode: $Mode"
```

After creating via editor or patch, verify BOM:

```bash
python3 - <<'PY'
from pathlib import Path
for p in ["packaging/windows/ensure-codex-desktop.ps1", "packaging/windows/write-install-mode.ps1"]:
    b = Path(p).read_bytes()
    if not b.startswith(b"\xef\xbb\xbf"):
        Path(p).write_bytes(b"\xef\xbb\xbf" + b)
PY
```

- [ ] **Step 4: Update portable installer**

Modify `packaging/windows/install.ps1` param block:

```powershell
param(
    [switch]$Silent,
    [switch]$Uninstall,
    [switch]$MinimalVSCode
)
```

Add required files:

```powershell
'ensure-codex-desktop.ps1',
'write-install-mode.ps1',
```

Replace unconditional VS Code ensure block with:

```powershell
if ($MinimalVSCode) {
    Write-Step "Writing install mode minimal_vscode..."
    & (Join-Path $InstallDir 'write-install-mode.ps1') -Mode 'minimal_vscode' -Path (Join-Path $InstallDir 'install-mode.json')
    Write-Step "Ensuring VS Code is installed..."
    & (Join-Path $InstallDir 'ensure-vscode.ps1') -ManifestPath (Join-Path $InstallDir 'vscode-manifest.json')
} else {
    Write-Step "Writing install mode codex_desktop..."
    & (Join-Path $InstallDir 'write-install-mode.ps1') -Mode 'codex_desktop' -Path (Join-Path $InstallDir 'install-mode.json')
    Write-Step "Ensuring Codex Desktop is installed..."
    & (Join-Path $InstallDir 'ensure-codex-desktop.ps1')
}
```

Update shortcut description:

```powershell
$shortcut.Description = '星池指挥官一键启动'
```

- [ ] **Step 5: Update Inno installer**

In `packaging/windows/installer.iss`, add task:

```ini
Name: "minimalvscode"; Description: "极简风界面（安装简化 VS Code）"; GroupDescription: "界面模式"; Flags: unchecked
```

In `[Files]`, add:

```ini
Source: "ensure-codex-desktop.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "write-install-mode.ps1"; DestDir: "{app}"; Flags: ignoreversion
```

Replace `[Run]` install entries with:

```ini
Filename: "powershell"; \
    Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\write-install-mode.ps1"" -Mode codex_desktop -Path ""{app}\install-mode.json"""; \
    Flags: runhidden waituntilterminated; Check: ShouldInstallCodexDesktop
Filename: "powershell"; \
    Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\ensure-codex-desktop.ps1"""; \
    Flags: runhidden waituntilterminated; Check: ShouldInstallCodexDesktop
Filename: "powershell"; \
    Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\write-install-mode.ps1"" -Mode minimal_vscode -Path ""{app}\install-mode.json"""; \
    Flags: runhidden waituntilterminated; Tasks: minimalvscode
Filename: "powershell"; \
    Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\ensure-vscode.ps1"" -ManifestPath ""{app}\vscode-manifest.json"""; \
    Flags: runhidden waituntilterminated; Tasks: minimalvscode
```

Add `[Code]`:

```pascal
[Code]
function ShouldInstallCodexDesktop(): Boolean;
begin
  Result := not WizardIsTaskSelected('minimalvscode');
end;
```

- [ ] **Step 6: Update package scripts**

In both `scripts/package-windows.sh` and `scripts/package-windows-zip.sh`, add preflight entries:

```bash
packaging/windows/ensure-codex-desktop.ps1 \
packaging/windows/write-install-mode.ps1 \
```

In `scripts/package-windows-zip.sh`, copy both scripts into `$STAGE`:

```bash
cp packaging/windows/ensure-codex-desktop.ps1 "$STAGE/"
cp packaging/windows/write-install-mode.ps1 "$STAGE/"
```

Update portable README step 2:

```text
2) Wait for "Install complete." The installer automatically installs
   Codex Desktop with winget. To install the simplified VS Code interface
   instead, run:
     powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -MinimalVSCode
```

- [ ] **Step 7: Run packaging tests and commit**

Run:

```bash
go test ./internal/vscode -run 'TestWindowsInstallScriptsIncludeVSCodeInstaller|TestWindowsPowerShellScriptsUseUTF8BOM|TestEnsureCodexDesktopScriptUsesWingetMsstore' -count=1
```

Expected: PASS.

Commit:

```bash
git add packaging/windows/ensure-codex-desktop.ps1 packaging/windows/write-install-mode.ps1 packaging/windows/install.ps1 packaging/windows/installer.iss scripts/package-windows.sh scripts/package-windows-zip.sh internal/vscode/install_test.go
git commit -m "feat(packaging): default installer to codex desktop"
```

## Task 7: End-to-End Test Hooks and Final Verification

**Files:**
- Modify: `test/e2e/windows/e2e_test.go`

- [ ] **Step 1: Add Windows manual verification script snippets to E2E notes**

In `test/e2e/windows/e2e_test.go`, replace the VS Code assertion in the `verify:` section:

```go
out, _, _ = c.Pwsh(`& "$env:LOCALAPPDATA\Programs\Microsoft VS Code\bin\code.cmd" --version`)
if !strings.HasPrefix(strings.TrimSpace(out), "1.") {
	t.Errorf("vs code missing: %s", out)
}
```

with default Codex Desktop checks:

```go
out, _, _ = c.Pwsh(`Get-Content "$env:LOCALAPPDATA\Programs\agentserver-vscode\install-mode.json"`)
if !strings.Contains(out, "codex_desktop") {
	t.Errorf("install mode wrong: %s", out)
}
out, _, _ = c.Pwsh(`winget list Codex -s msstore`)
if !strings.Contains(strings.ToLower(out), "codex") {
	t.Errorf("Codex Desktop not listed by winget: %s", out)
}
out, _, _ = c.Pwsh(`Test-Path 'Registry::HKEY_CURRENT_USER\Software\Classes\codex\shell\open\command' -or Test-Path 'Registry::HKEY_LOCAL_MACHINE\Software\Classes\codex\shell\open\command'`)
if strings.TrimSpace(out) != "True" {
	t.Errorf("codex URL scheme missing: %s", out)
}
```

For manual test-machine verification, these commands should also be run after installing the EXE:

```powershell
Get-Content "$env:LOCALAPPDATA\Programs\agentserver-vscode\install-mode.json"
winget list Codex -s msstore
Test-Path 'Registry::HKEY_CURRENT_USER\Software\Classes\codex\shell\open\command'
Test-Path 'Registry::HKEY_LOCAL_MACHINE\Software\Classes\codex\shell\open\command'
```

Expected on the Windows test machine: install-mode contains `codex_desktop`, `winget list` shows Codex or the `codex://` registry scheme exists.

- [ ] **Step 2: Run focused Go tests**

Run:

```bash
go test ./internal/state ./internal/installmode ./internal/codexdesktop ./internal/ui ./cmd/launcher ./cmd/open-folder ./cmd/agentctl ./internal/vscode -count=1
```

Expected: PASS.

- [ ] **Step 3: Run frontend tests and build**

Run:

```bash
cd internal/ui/web && npm test && npm run build
```

Expected: PASS.

- [ ] **Step 4: Run full unit suite**

Run:

```bash
make test-unit
```

Expected: PASS. If race tests fail because a Windows-only command is unavailable on Linux, rerun the exact failing package with `-count=1` and fix the package-level test seam instead of skipping the new behavior.

- [ ] **Step 5: Cross-build Windows binaries**

Run:

```bash
make cross-windows
```

Expected: PASS and `dist/windows/launcher.exe`, `open-folder.exe`, `agentctl.exe`, and `onboarding-server.exe` are rebuilt.

- [ ] **Step 6: Build portable package**

Run:

```bash
bash scripts/package-windows-zip.sh
```

Expected: PASS and the zip contains `ensure-codex-desktop.ps1`, `write-install-mode.ps1`, `ensure-vscode.ps1`, and `install.ps1`.

- [ ] **Step 7: Build EXE installer when Inno Setup is available**

Run:

```bash
bash scripts/package-windows.sh
```

Expected: PASS when Inno Setup or Wine Inno Setup is installed. If unavailable, record the exact "Inno Setup not found" output and continue with the portable package plus Go packaging tests.

- [ ] **Step 8: Windows test machine default-mode smoke**

On the Windows test machine after uninstalling prior installs, run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
Get-Content "$env:LOCALAPPDATA\Programs\agentserver-vscode\install-mode.json"
winget list Codex -s msstore
& "$env:LOCALAPPDATA\Programs\agentserver-vscode\agentctl.exe" doctor
```

Expected: install-mode is `codex_desktop`, Codex is installed or detected, and doctor prints `frontend: codex_desktop`.

- [ ] **Step 9: Windows test machine minimal VS Code smoke**

On the Windows test machine after uninstalling prior installs, run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -MinimalVSCode
Get-Content "$env:LOCALAPPDATA\Programs\agentserver-vscode\install-mode.json"
& "$env:LOCALAPPDATA\Programs\agentserver-vscode\agentctl.exe" doctor
```

Expected: install-mode is `minimal_vscode`, VS Code is installed/detected, and doctor prints `frontend: minimal_vscode`.

- [ ] **Step 10: Commit final verification updates**

Commit any E2E test or generated UI asset updates:

```bash
git add test/e2e/windows docs/superpowers/specs internal/ui/assets/dist
git commit -m "test: cover codex desktop install mode"
```

If there are no file changes after verification, skip this commit and note that no final verification commit was needed.

## Self-Review Checklist

- Spec requirement "default uses `winget install Codex -s msstore`": Task 2 and Task 6.
- Spec requirement "check installed first": Task 2 `EnsureInstalled` and Task 6 `Test-CodexDesktopInstalled`.
- Spec requirement "only `极简风` installs VS Code": Task 6 Inno tasks and portable `-MinimalVSCode`.
- Spec requirement "wizard switches to Codex Desktop wording": Task 5.
- Spec requirement "Codex Desktop config writes shared `~/.codex/config.toml`": Task 3.
- Spec requirement "desktop shortcut still opens onboarding": unchanged shortcut target in Task 6, launcher mode sync in Task 4.
- Spec requirement "completion opens Codex Desktop": Task 3 and Task 4.
- Spec requirement "old VS Code endpoints remain compatible": Task 3 server wrappers.
- Spec requirement "Windows E2E covers default and minimal modes": Task 7.
