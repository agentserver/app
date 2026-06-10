# Local Slave Start/Auth UX Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hide Windows slave startup console windows and automatically open the slave authentication URL after startup.

**Architecture:** Keep the behavior in `internal/slave` because that package owns process startup and auth URL detection. Add an optional opener callback to `ManagerDeps`, wire it from `cmd/launcher` to `browser.Open`, and keep the dashboard fallback link unchanged.

**Tech Stack:** Go, Windows process startup via existing `internal/process.HideWindow`, existing browser opener in `internal/browser`, existing slave registry tests.

---

## File Structure

- Modify `internal/slave/process_test.go`: add regression tests for hidden process startup and automatic auth URL opening.
- Modify `internal/slave/process.go`: hide spawned slave process windows and invoke the optional auth URL opener.
- Modify `cmd/launcher/main.go`: wire `OpenAuthURL` to `browser.Open` in the completed slave manager dependencies.

### Task 1: Add Regression Tests

**Files:**
- Modify: `internal/slave/process_test.go`

- [x] **Step 1: Add tests for immediate and delayed auth URL opening**

Add tests near `TestManagerCreateWritesConfigAndStartsProcess`:

```go
func TestManagerCreateAndStartOpensImmediateAuthURL(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	opened := make(chan string, 1)
	manager := NewManager(ManagerDeps{
		Machines:    NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry:    NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:      &fakeRunner{pid: 4321, authURL: authURL},
		SlaveExe:    filepath.Join(dir, "slave-agent.exe"),
		OpenAuthURL: func(url string) { opened <- url },
	})
	if _, err := manager.Machines.Ensure("61414-PC"); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder, Name: "worker"}); err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}

	select {
	case got := <-opened:
		if got != authURL {
			t.Fatalf("opened auth URL=%q, want %q", got, authURL)
		}
	case <-time.After(time.Second):
		t.Fatal("auth URL was not opened")
	}
}

func TestManagerDelayedAuthURLOpensBrowser(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	authURLs := make(chan string, 1)
	opened := make(chan string, 1)
	manager := NewManager(ManagerDeps{
		Machines:    NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry:    NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:      &fakeRunner{pid: 4321, authURLs: authURLs},
		SlaveExe:    filepath.Join(dir, "slave-agent.exe"),
		OpenAuthURL: func(url string) { opened <- url },
	})
	if _, err := manager.Machines.Ensure("61414-PC"); err != nil {
		t.Fatal(err)
	}

	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder, Name: "worker"})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	authURLs <- authURL

	select {
	case got := <-opened:
		if got != authURL {
			t.Fatalf("opened auth URL=%q, want %q", got, authURL)
		}
	case <-time.After(time.Second):
		t.Fatal("auth URL was not opened")
	}
	waitForSlave(t, manager.Registry, sl.ID, func(sl Slave) bool {
		return sl.Status == StatusAuthRequired && sl.AuthURL == authURL
	})
}
```

- [x] **Step 2: Add a static regression test for hidden process startup**

Add near `TestExecRunnerStartPassesConfigArgAndLogsStdoutAndStderr`:

```go
func TestExecRunnerStartHidesSlaveProcessWindow(t *testing.T) {
	body, err := os.ReadFile("process.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	command := strings.Index(s, "cmd := exec.Command(req.Exe, req.ConfigPath)")
	hide := strings.Index(s, "process.HideWindow(cmd)")
	start := strings.Index(s, "if err := cmd.Start(); err != nil")
	if command < 0 || hide < 0 || start < 0 {
		t.Fatal("execRunner.Start should create, hide, then start the slave process")
	}
	if command > hide || hide > start {
		t.Fatal("execRunner.Start should call process.HideWindow before cmd.Start")
	}
}
```

- [x] **Step 3: Extend `fakeRunner` to emit delayed auth URLs**

Change the test fake to:

```go
type fakeRunner struct {
	pid           int
	authURL       string
	authURLs      <-chan string
	startedConfig string
	stopped       map[int]bool
	startCalls    int
	startErr      error
	stopErr       error
	onStart       func()
}
```

and return:

```go
return StartResult{PID: f.pid, AuthURL: f.authURL, AuthURLs: f.authURLs}, nil
```

- [x] **Step 4: Run tests and verify they fail**

Run:

```bash
go test ./internal/slave -run 'TestManagerCreateAndStartOpensImmediateAuthURL|TestManagerDelayedAuthURLOpensBrowser|TestExecRunnerStartHidesSlaveProcessWindow' -count=1
```

Expected: FAIL because `ManagerDeps.OpenAuthURL` does not exist and `process.HideWindow(cmd)` is not called in `execRunner.Start`.

### Task 2: Implement Hidden Process Startup

**Files:**
- Modify: `internal/slave/process.go`

- [x] **Step 1: Import the existing process helper**

Add:

```go
"github.com/agentserver/agentserver-pkg/internal/process"
```

- [x] **Step 2: Hide the slave process window before start**

Change startup to:

```go
cmd := exec.Command(req.Exe, req.ConfigPath)
cmd.Dir = req.WorkDir
process.HideWindow(cmd)
```

- [x] **Step 3: Run hidden-window test**

Run:

```bash
go test ./internal/slave -run TestExecRunnerStartHidesSlaveProcessWindow -count=1
```

Expected: PASS.

### Task 3: Implement Automatic Auth URL Opening

**Files:**
- Modify: `internal/slave/process.go`
- Modify: `cmd/launcher/main.go`

- [x] **Step 1: Add opener dependency**

Change `ManagerDeps` to include:

```go
OpenAuthURL func(string)
```

- [x] **Step 2: Add helper to open auth URLs asynchronously**

Add a manager method:

```go
func (m *Manager) openAuthURL(url string) {
	if url == "" || m.d.OpenAuthURL == nil {
		return
	}
	go m.d.OpenAuthURL(url)
}
```

- [x] **Step 3: Open immediate auth URL**

In `start`, after registry update succeeds:

```go
if res.AuthURL != "" {
	m.openAuthURL(res.AuthURL)
}
```

- [x] **Step 4: Open delayed auth URL**

In the `monitor` auth URL case:

```go
m.recordAuthURL(id, res.PID, url)
m.openAuthURL(url)
```

- [x] **Step 5: Wire launcher dependency**

In `completedSlaveManagerDeps`, set:

```go
OpenAuthURL: func(url string) { _ = browser.Open(url) },
```

- [x] **Step 6: Run auth URL tests**

Run:

```bash
go test ./internal/slave -run 'TestManagerCreateAndStartOpensImmediateAuthURL|TestManagerDelayedAuthURLOpensBrowser' -count=1
```

Expected: PASS.

### Task 4: Verification

**Files:**
- Verify only.

- [x] **Step 1: Run focused tests**

Run:

```bash
go test ./internal/slave -count=1
```

Expected: PASS.

- [x] **Step 2: Run full Go tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [x] **Step 3: Run frontend tests and build**

Run:

```bash
npm test
npm run build
```

from `internal/ui/web`.

Expected: PASS.

- [x] **Step 4: Check diff cleanliness**

Run:

```bash
git diff --check
```

Expected: no output.
