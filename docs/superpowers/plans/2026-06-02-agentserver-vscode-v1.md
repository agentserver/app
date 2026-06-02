# agentserver-vscode v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现一个 Windows x64 安装包,自动化部署 VS Code + codex + modelserver/agentserver 账号,面向非专业用户。

**Architecture:** 单一 Go 仓库交叉编译多个二进制 (launcher / onboarding-server / agentctl / open-folder),配合独立 VS Code 扩展 (TS),用 Inno Setup 打包成 `.exe`。首启时本地 web UI 引导,后续直接 exec VS Code。所有逻辑封在 `internal/*` 子包,每包单一职责。**v1 不含 loom**,留 v2。

**Tech Stack:** Go 1.22+ · TypeScript (VS Code extension) · Inno Setup 6 · zalando/go-keyring · pelletier/go-toml/v2 · golang.org/x/sys/windows (registry/COM) · @vscode/test-electron · GitHub Actions

**Spec:** `docs/superpowers/specs/2026-06-02-agentserver-vscode-installer-design.md` (committed)

**Phases:**
- P0  仓库 bootstrap
- P1  state + secrets (基础)
- P2  download (断点续传)
- P3  oauth (device-code)
- P4  modelserver + agentserver clients
- P5  codex/config + env/windows
- P6  vscode/{detect,install,settings,extensions}
- P7  shortcut/windows
- P8  ui server + onboarding cmd
- P9  launcher + open-folder + agentctl
- P10 VS Code 扩展
- P11 Inno Setup 打包
- P12 integration tests (fakeserver + flow tests)
- P13 Windows E2E

每个 phase 结束时:`go test ./...` 全绿 + git commit。

---

## P0 仓库 bootstrap

### Task P0.1: 初始化 Go module

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `README.md`

- [ ] **Step 1: 初始化 module**

```bash
cd /root/agentserver-pkg
go mod init github.com/agentserver/agentserver-pkg
```

Expected: 生成 `go.mod` 含 `module github.com/agentserver/agentserver-pkg`,go version。

- [ ] **Step 2: 写 .gitignore**

```gitignore
# Build artifacts
/dist/
/bin/
/out/

# Cache
/.cache/
/cache/

# Test artifacts
/coverage.out
*.test
*.prof

# IDE
.idea/
.vscode/
*.swp

# Local state
state.json
*.bak.*

# Secrets
.env
fixtures/account.env
test/e2e/windows/fixtures/account.env

# Packaging output
/packaging/windows/Output/

# Node
node_modules/
extensions/*/out/
extensions/*/*.vsix

# OS
.DS_Store
Thumbs.db
```

- [ ] **Step 3: 写 README.md**

```markdown
# agentserver-vscode

Windows installer that sets up VS Code + codex pre-configured against
modelserver (`code.cs.ac.cn`) and agentserver (`agent.cs.ac.cn`).

See `docs/superpowers/specs/2026-06-02-agentserver-vscode-installer-design.md`
for the v1 design spec.

## Building (Linux dev)

```
make build      # cross-compile Windows binaries to dist/
make test       # unit + integration tests
make package    # requires Wine + Inno Setup for full pipeline
```
```

- [ ] **Step 4: Commit**

```bash
git add go.mod .gitignore README.md
git commit -m "chore: initialize Go module + .gitignore + README"
```

### Task P0.2: Makefile

**Files:**
- Create: `Makefile`

- [ ] **Step 1: Write Makefile**

```makefile
.PHONY: all build test test-unit test-integration lint clean cross-windows package help

GO        ?= go
GOFLAGS   ?= -trimpath
LDFLAGS   ?= -s -w
GOOS_WIN  := windows
GOARCH    := amd64

CMDS      := launcher onboarding-server agentctl open-folder
DIST      := dist

all: build

help:
	@echo "make build              - build native binaries to dist/<os>/"
	@echo "make cross-windows      - cross-compile windows/amd64 to dist/windows/"
	@echo "make test               - go test -race ./..."
	@echo "make test-unit          - unit tests only (-short)"
	@echo "make test-integration   - integration tests (test/integration)"
	@echo "make lint               - go vet + staticcheck"
	@echo "make ext-build          - build VS Code extension .vsix"
	@echo "make package            - build Windows .exe installer (requires Inno Setup)"
	@echo "make clean              - rm dist/ and out/"

build:
	@mkdir -p $(DIST)/$(shell go env GOOS)
	@for cmd in $(CMDS); do \
		echo "==> building $$cmd"; \
		$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
		  -o $(DIST)/$(shell go env GOOS)/$$cmd ./cmd/$$cmd ; \
	done

cross-windows:
	@mkdir -p $(DIST)/windows
	@for cmd in $(CMDS); do \
		echo "==> cross-building $$cmd (windows/amd64)"; \
		GOOS=$(GOOS_WIN) GOARCH=$(GOARCH) \
		  $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
		  -o $(DIST)/windows/$$cmd.exe ./cmd/$$cmd ; \
	done

test:
	$(GO) test -race -count=1 ./...

test-unit:
	$(GO) test -race -short -count=1 ./...

test-integration:
	$(GO) test -race -count=1 -tags=integration ./test/integration/...

lint:
	$(GO) vet ./...
	@which staticcheck >/dev/null 2>&1 || $(GO) install honnef.co/go/tools/cmd/staticcheck@latest
	staticcheck ./...

ext-build:
	cd extensions/agentserver-vscode && npm ci && npm run package

package: cross-windows ext-build
	bash scripts/package-windows.sh

clean:
	rm -rf $(DIST) out coverage.out
```

- [ ] **Step 2: Verify Makefile parses**

Run: `make help`
Expected: 打印 help 内容,无错误。

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "chore: add Makefile with build/test/package targets"
```

### Task P0.3: GitHub Actions CI 骨架

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/e2e-windows.yml`

- [ ] **Step 1: Write ci.yml**

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  test-linux:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - run: go vet ./...
      - run: go test -race -short -count=1 ./...

  test-integration:
    runs-on: ubuntu-latest
    needs: test-linux
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - run: go test -race -count=1 -tags=integration ./test/integration/...

  build-windows:
    runs-on: ubuntu-latest
    needs: test-linux
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - run: make cross-windows
      - uses: actions/upload-artifact@v4
        with:
          name: windows-binaries
          path: dist/windows/

  test-extension:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      - run: cd extensions/agentserver-vscode && npm ci && npm test
```

- [ ] **Step 2: Write e2e-windows.yml**

```yaml
name: Windows E2E

on:
  workflow_dispatch:
  push:
    tags:
      - 'v*'

jobs:
  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - name: Build Windows binaries
        run: make cross-windows
      - name: Run E2E
        env:
          E2E_SSH_HOST: 10.128.185.173
          E2E_SSH_PORT: '2222'
          E2E_SSH_USER: '61414'
          E2E_SSH_PASSWORD: ${{ secrets.E2E_SSH_PASSWORD }}
          TEST_MS_USER: ${{ secrets.TEST_MS_USER }}
          TEST_MS_PASS: ${{ secrets.TEST_MS_PASS }}
          TEST_AS_USER: ${{ secrets.TEST_AS_USER }}
          TEST_AS_PASS: ${{ secrets.TEST_AS_PASS }}
        run: go test -count=1 -timeout=30m -tags=e2e ./test/e2e/windows/...
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/
git commit -m "ci: add CI workflows for tests, Windows build, and E2E"
```

### Task P0.4: 占位 cmd 目录让 make build 跑通

**Files:**
- Create: `cmd/launcher/main.go`
- Create: `cmd/onboarding-server/main.go`
- Create: `cmd/agentctl/main.go`
- Create: `cmd/open-folder/main.go`

- [ ] **Step 1: 每个 cmd 写一个最小可编译 main**

`cmd/launcher/main.go`:
```go
package main

import "fmt"

func main() {
	fmt.Println("agentserver-vscode launcher (stub)")
}
```

`cmd/onboarding-server/main.go`:
```go
package main

import "fmt"

func main() {
	fmt.Println("onboarding-server (stub)")
}
```

`cmd/agentctl/main.go`:
```go
package main

import "fmt"

func main() {
	fmt.Println("agentctl (stub)")
}
```

`cmd/open-folder/main.go`:
```go
package main

import "fmt"

func main() {
	fmt.Println("open-folder (stub)")
}
```

- [ ] **Step 2: 验证 build 通**

Run: `make build`
Expected: `dist/linux/launcher`、`onboarding-server`、`agentctl`、`open-folder` 四个二进制。

Run: `make cross-windows`
Expected: `dist/windows/*.exe` 四个二进制。

- [ ] **Step 3: 验证 test 通**

Run: `make test`
Expected: PASS,没有 test 但也不报错。

- [ ] **Step 4: Commit**

```bash
git add cmd/
git commit -m "chore: stub cmd entrypoints to keep build green"
```

---

## P1 internal/state + internal/secrets (基础设施)

### Task P1.1: state.State 数据结构

**Files:**
- Create: `internal/state/types.go`
- Test: `internal/state/types_test.go`

- [ ] **Step 1: Write the failing test**

`internal/state/types_test.go`:
```go
package state

import (
	"encoding/json"
	"testing"
)

func TestStateRoundtrip(t *testing.T) {
	s := State{
		SchemaVersion: 1,
		InstallID:     "abc-123",
		Onboarding: OnboardingState{
			Status:          StatusPending,
			CompletedSteps:  []string{"modelserver_login"},
		},
		Modelserver: ModelserverState{
			BaseURL:      "https://code.cs.ac.cn",
			ProjectID:    "proj-1",
			APIKeySuffix: "abcd",
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
	if got.SchemaVersion != 1 || got.InstallID != "abc-123" {
		t.Errorf("roundtrip lost data: %+v", got)
	}
	if len(got.Onboarding.CompletedSteps) != 1 ||
		got.Onboarding.CompletedSteps[0] != "modelserver_login" {
		t.Errorf("steps wrong: %+v", got.Onboarding.CompletedSteps)
	}
}

func TestAddCompletedDedup(t *testing.T) {
	o := &OnboardingState{}
	o.AddCompleted("a")
	o.AddCompleted("a")
	o.AddCompleted("b")
	if len(o.CompletedSteps) != 2 {
		t.Errorf("expected 2 unique steps, got %v", o.CompletedSteps)
	}
}

func TestHasCompleted(t *testing.T) {
	o := OnboardingState{CompletedSteps: []string{"x", "y"}}
	if !o.HasCompleted("x") || o.HasCompleted("z") {
		t.Errorf("HasCompleted wrong")
	}
}
```

- [ ] **Step 2: Run test, expect compile failure**

Run: `go test ./internal/state/...`
Expected: FAIL — `undefined: State`.

- [ ] **Step 3: Write types**

`internal/state/types.go`:
```go
// Package state holds the persisted onboarding state.
// Sensitive secrets are NOT stored here; they live in keyring.
package state

import "time"

const CurrentSchemaVersion = 1

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusComplete   Status = "complete"
	StatusFailed     Status = "failed"
)

type State struct {
	SchemaVersion int              `json:"schema_version"`
	InstallID     string           `json:"install_id"`
	CreatedAt     time.Time        `json:"created_at"`
	Onboarding    OnboardingState  `json:"onboarding"`
	Modelserver   ModelserverState `json:"modelserver"`
	Agentserver   AgentserverState `json:"agentserver"`
	VSCode        VSCodeState      `json:"vscode"`
	Shortcuts     ShortcutsState   `json:"shortcuts"`
}

type OnboardingState struct {
	Status         Status   `json:"status"`
	CompletedSteps []string `json:"completed_steps"`
	LastError      string   `json:"last_error,omitempty"`
}

func (o *OnboardingState) AddCompleted(step string) {
	for _, s := range o.CompletedSteps {
		if s == step {
			return
		}
	}
	o.CompletedSteps = append(o.CompletedSteps, step)
}

func (o OnboardingState) HasCompleted(step string) bool {
	for _, s := range o.CompletedSteps {
		if s == step {
			return true
		}
	}
	return false
}

type ModelserverState struct {
	BaseURL         string    `json:"base_url"`
	UserID          string    `json:"user_id,omitempty"`
	ProjectID       string    `json:"project_id,omitempty"`
	APIKeySuffix    string    `json:"api_key_suffix,omitempty"`
	APIKeyCreatedAt time.Time `json:"api_key_created_at,omitempty"`
}

type AgentserverState struct {
	BaseURL              string `json:"base_url"`
	UserID               string `json:"user_id,omitempty"`
	WorkspaceID          string `json:"workspace_id,omitempty"`
	WorkspaceAPIKeySuffix string `json:"workspace_api_key_suffix,omitempty"`
}

type VSCodeState struct {
	Path             string `json:"path,omitempty"`
	Version          string `json:"version,omitempty"`
	InstalledByUs    bool   `json:"installed_by_us"`
	UserDataDir      string `json:"user_data_dir,omitempty"`
	ExtensionsDir    string `json:"extensions_dir,omitempty"`
	ExtensionVersion string `json:"extension_version,omitempty"`
}

type ShortcutsState struct {
	DesktopCreated     bool `json:"desktop_created"`
	ContextMenuInstalled bool `json:"context_menu_installed"`
}
```

- [ ] **Step 4: Run tests, expect PASS**

Run: `go test -race ./internal/state/...`
Expected: PASS — TestStateRoundtrip, TestAddCompletedDedup, TestHasCompleted。

- [ ] **Step 5: Commit**

```bash
git add internal/state/types.go internal/state/types_test.go
git commit -m "feat(state): add State types and step tracking helpers"
```

### Task P1.2: state.Store (atomic file IO + flock)

**Files:**
- Create: `internal/state/store.go`
- Test: `internal/state/store_test.go`

- [ ] **Step 1: Write the failing test**

`internal/state/store_test.go`:
```go
package state

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestStoreLoadMissing(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	s, err := store.Load()
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if s.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("expected fresh state with schema %d, got %d",
			CurrentSchemaVersion, s.SchemaVersion)
	}
	if s.InstallID == "" {
		t.Errorf("expected generated install_id")
	}
}

func TestStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store := NewStore(path)

	s, _ := store.Load()
	s.Onboarding.AddCompleted("modelserver_login")
	if err := store.Save(s); err != nil {
		t.Fatalf("save: %v", err)
	}

	store2 := NewStore(path)
	loaded, err := store2.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !loaded.Onboarding.HasCompleted("modelserver_login") {
		t.Errorf("step not persisted")
	}
}

func TestStoreUpdateConcurrent(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = store.Update(func(s *State) error {
				s.Onboarding.AddCompleted("step")
				return nil
			})
		}(i)
	}
	wg.Wait()
	s, _ := store.Load()
	if len(s.Onboarding.CompletedSteps) != 1 {
		t.Errorf("dedup failed under concurrency: %v", s.Onboarding.CompletedSteps)
	}
}

func TestStoreCorruptionRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Write garbage
	if err := writeBytes(path, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	s, err := store.Load()
	if err != nil {
		t.Fatalf("expected recovery, got err: %v", err)
	}
	if s.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("expected fresh state after corruption")
	}
	// Backup file should exist
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) == 0 {
		t.Errorf("expected backup file")
	}
}

func writeBytes(path string, b []byte) error {
	return writeFile(path, b)
}
```

- [ ] **Step 2: Run test, expect compile failure**

Run: `go test ./internal/state/...`
Expected: FAIL — `undefined: NewStore`, `undefined: writeFile`.

- [ ] **Step 3: Implement store**

`internal/state/store.go`:
```go
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load reads state.json. If missing, returns a fresh State. If corrupt,
// renames the bad file to <path>.corrupt-<ts> and returns a fresh State.
func (s *Store) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (*State, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return freshState(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil || st.SchemaVersion == 0 {
		backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().Unix())
		_ = os.Rename(s.path, backup)
		return freshState(), nil
	}
	return &st, nil
}

// Save writes state.json atomically.
func (s *Store) Save(st *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(st)
}

func (s *Store) saveLocked(st *State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return writeFile(s.path, b)
}

// Update is a read-modify-write under the store mutex.
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	return s.saveLocked(st)
}

func freshState() *State {
	return &State{
		SchemaVersion: CurrentSchemaVersion,
		InstallID:     uuid.NewString(),
		CreatedAt:     time.Now().UTC(),
		Onboarding:    OnboardingState{Status: StatusPending},
		Modelserver:   ModelserverState{BaseURL: "https://code.cs.ac.cn"},
		Agentserver:   AgentserverState{BaseURL: "https://agent.cs.ac.cn"},
	}
}

// writeFile atomically writes b to path via tmp + rename.
func writeFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

- [ ] **Step 4: Add uuid dep**

```bash
go get github.com/google/uuid@latest
go mod tidy
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `go test -race ./internal/state/...`
Expected: PASS — all 4 tests including concurrent and corruption recovery.

- [ ] **Step 6: Commit**

```bash
git add internal/state/ go.mod go.sum
git commit -m "feat(state): atomic store with mutex + corruption recovery"
```

### Task P1.3: secrets keyring 封装 (跨平台,带文件回退)

**Files:**
- Create: `internal/secrets/secrets.go`
- Test: `internal/secrets/secrets_test.go`

- [ ] **Step 1: Write the failing test**

`internal/secrets/secrets_test.go`:
```go
package secrets

import (
	"path/filepath"
	"testing"
)

func TestFileFallbackRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := newFileStore(filepath.Join(dir, "secrets.json"))
	if err := s.Set("k1", "v1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("k1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v1" {
		t.Errorf("got %q want v1", got)
	}
	if err := s.Delete("k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("k1"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestFileFallbackMissing(t *testing.T) {
	dir := t.TempDir()
	s := newFileStore(filepath.Join(dir, "missing.json"))
	if _, err := s.Get("nope"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFileFallbackPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.json")
	s := newFileStore(path)
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	info, err := stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode().Perm()
	// On Windows the umask may differ; only enforce on Unix.
	if mode > 0o600 {
		t.Errorf("secrets file too permissive: %v", mode)
	}
}
```

- [ ] **Step 2: Run test, expect failure**

Run: `go test ./internal/secrets/...`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

`internal/secrets/secrets.go`:
```go
// Package secrets stores sensitive values in the system keyring,
// falling back to a chmod 600 file under the user state dir.
package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/zalando/go-keyring"
)

const serviceName = "agentserver-vscode"

var ErrNotFound = errors.New("secret not found")

type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// New returns a Store backed by the system keyring; if unavailable, it
// falls back to a chmod 600 JSON file at fallbackPath.
func New(fallbackPath string) Store {
	if keyringAvailable() {
		return &keyringStore{}
	}
	return newFileStore(fallbackPath)
}

func keyringAvailable() bool {
	_, err := keyring.Get(serviceName, "__probe__")
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return true
	}
	// On Linux without libsecret, returns an error mentioning secret-tool.
	return false
}

type keyringStore struct{}

func (k *keyringStore) Get(key string) (string, error) {
	v, err := keyring.Get(serviceName, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (k *keyringStore) Set(key, value string) error {
	return keyring.Set(serviceName, key, value)
}

func (k *keyringStore) Delete(key string) error {
	err := keyring.Delete(serviceName, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// ---- File fallback ----

type fileStore struct {
	path string
	mu   sync.Mutex
}

func newFileStore(path string) *fileStore {
	return &fileStore{path: path}
}

func (f *fileStore) load() (map[string]string, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("secrets file corrupt: %w", err)
	}
	return m, nil
}

func (f *fileStore) save(m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(f.path, b, 0o600); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(f.path, 0o600)
	}
	return nil
}

func (f *fileStore) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *fileStore) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[key] = value
	return f.save(m)
}

func (f *fileStore) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	delete(m, key)
	return f.save(m)
}

func stat(p string) (os.FileInfo, error) { return os.Stat(p) }
```

- [ ] **Step 4: Add keyring dep**

```bash
go get github.com/zalando/go-keyring@latest
go mod tidy
```

- [ ] **Step 5: Run tests**

Run: `go test -race ./internal/secrets/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/secrets/ go.mod go.sum
git commit -m "feat(secrets): keyring wrapper with chmod 600 file fallback"
```

---

## P2 internal/download (断点续传)

### Task P2.1: download.Plan + URL/ETag/Range helpers

**Files:**
- Create: `internal/download/types.go`
- Test: `internal/download/types_test.go`

- [ ] **Step 1: Write failing test**

`internal/download/types_test.go`:
```go
package download

import "testing"

func TestProgressEventString(t *testing.T) {
	e := ProgressEvent{
		Downloaded: 1024 * 1024,
		Total:      10 * 1024 * 1024,
		SpeedBps:   2 * 1024 * 1024,
	}
	got := e.String()
	want := "1.0 MiB / 10.0 MiB @ 2.0 MiB/s"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMetaRoundtrip(t *testing.T) {
	m := Meta{URL: "https://x", ETag: `"abc"`, TotalSize: 1234, SHA256: "deadbeef"}
	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalMeta(b)
	if err != nil {
		t.Fatal(err)
	}
	if got != m {
		t.Errorf("roundtrip: %+v vs %+v", got, m)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/download/...`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement types**

`internal/download/types.go`:
```go
// Package download implements resumable HTTP file downloads.
package download

import (
	"encoding/json"
	"fmt"
)

// ProgressEvent is pushed on the progress channel during download.
type ProgressEvent struct {
	Downloaded int64
	Total      int64
	SpeedBps   int64
	Stage      string // e.g. "head", "download", "verify"
	Msg        string
}

func (e ProgressEvent) String() string {
	return fmt.Sprintf("%s / %s @ %s/s",
		humanBytes(e.Downloaded), humanBytes(e.Total), humanBytes(e.SpeedBps))
}

// Meta accompanies a .part file so we can verify resumability later.
type Meta struct {
	URL       string `json:"url"`
	ETag      string `json:"etag"`
	TotalSize int64  `json:"total_size"`
	SHA256    string `json:"sha256"`
}

func (m Meta) Marshal() ([]byte, error) { return json.MarshalIndent(m, "", "  ") }

func UnmarshalMeta(b []byte) (Meta, error) {
	var m Meta
	err := json.Unmarshal(b, &m)
	return m, err
}

func humanBytes(n int64) string {
	const k = 1024.0
	if n < int64(k) {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	v := float64(n) / k
	for _, u := range units {
		if v < k {
			return fmt.Sprintf("%.1f %s", v, u)
		}
		v /= k
	}
	return fmt.Sprintf("%.1f PiB", v)
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/download/...`

- [ ] **Step 5: Commit**

```bash
git add internal/download/
git commit -m "feat(download): add ProgressEvent + Meta types"
```

### Task P2.2: Resumable downloader

**Files:**
- Create: `internal/download/resumable.go`
- Test: `internal/download/resumable_test.go`

- [ ] **Step 1: Write the failing test (comprehensive)**

`internal/download/resumable_test.go`:
```go
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// rangeServer serves body[start:end] for Range requests.
func rangeServer(body []byte, etag string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		rng := r.Header.Get("Range")
		if rng == "" {
			w.WriteHeader(200)
			w.Write(body)
			return
		}
		// e.g. "bytes=5-"
		var start int
		fmt.Sscanf(rng, "bytes=%d-", &start)
		if start >= len(body) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d",
			start, len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(body[start:])
	})
}

func TestFreshDownload(t *testing.T) {
	body := []byte("hello world hello world hello world")
	srv := httptest.NewServer(rangeServer(body, `"v1"`))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "f.bin")
	err := DownloadResumable(context.Background(), srv.URL, dst, sha256hex(body), nil)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != string(body) {
		t.Errorf("body mismatch")
	}
}

func TestResumeFromPartial(t *testing.T) {
	body := []byte("AAAAABBBBBCCCCCDDDDD")
	srv := httptest.NewServer(rangeServer(body, `"v1"`))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "f.bin")
	part := dst + ".part"
	meta := dst + ".meta"

	// Seed a partial download (first 10 bytes) and matching meta
	if err := os.WriteFile(part, body[:10], 0o644); err != nil {
		t.Fatal(err)
	}
	m := Meta{URL: srv.URL, ETag: `"v1"`, TotalSize: int64(len(body)), SHA256: sha256hex(body)}
	mb, _ := m.Marshal()
	os.WriteFile(meta, mb, 0o644)

	err := DownloadResumable(context.Background(), srv.URL, dst, sha256hex(body), nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != string(body) {
		t.Errorf("body mismatch after resume: %q", got)
	}
}

func TestETagChangeRestarts(t *testing.T) {
	body := []byte("12345678")
	srv := httptest.NewServer(rangeServer(body, `"v2"`))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "f.bin")
	// Pretend we had a partial from a previous etag
	os.WriteFile(dst+".part", []byte("OLDOLD"), 0o644)
	m := Meta{URL: srv.URL, ETag: `"v1"`, TotalSize: 99, SHA256: "x"}
	mb, _ := m.Marshal()
	os.WriteFile(dst+".meta", mb, 0o644)

	err := DownloadResumable(context.Background(), srv.URL, dst, sha256hex(body), nil)
	if err != nil {
		t.Fatalf("etag-change: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != string(body) {
		t.Errorf("expected fresh download, got %q", got)
	}
}

func TestSHA256MismatchDeletes(t *testing.T) {
	body := []byte("good body")
	srv := httptest.NewServer(rangeServer(body, `"v1"`))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "f.bin")
	err := DownloadResumable(context.Background(), srv.URL, dst, "deadbeef" /*wrong*/, nil)
	if err == nil {
		t.Fatal("expected sha256 mismatch error")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Errorf("unexpected err: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("dst should not exist on sha256 fail")
	}
}

func TestNoRangeSupport(t *testing.T) {
	body := []byte("0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Accept-Ranges, always 200 even on Range
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(200)
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "f.bin")
	os.WriteFile(dst+".part", []byte("XYZ"), 0o644)
	m := Meta{URL: srv.URL, TotalSize: int64(len(body)), SHA256: sha256hex(body)}
	mb, _ := m.Marshal()
	os.WriteFile(dst+".meta", mb, 0o644)

	err := DownloadResumable(context.Background(), srv.URL, dst, sha256hex(body), nil)
	if err != nil {
		t.Fatalf("no-range: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != string(body) {
		t.Errorf("expected truncate+refresh, got %q", got)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/download/...`
Expected: FAIL — `undefined: DownloadResumable`.

- [ ] **Step 3: Implement**

`internal/download/resumable.go`:
```go
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// DownloadResumable downloads url to dst, resuming a prior .part if compatible.
// On completion, the file is sha256-verified against expectedSHA256 and renamed.
// progress (if non-nil) receives periodic events.
func DownloadResumable(ctx context.Context, url, dst, expectedSHA256 string,
	progress chan<- ProgressEvent) error {

	part := dst + ".part"
	metaPath := dst + ".meta"

	// 1. HEAD to discover ETag and Content-Length.
	headReq, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		return fmt.Errorf("HEAD %s: %w", url, err)
	}
	headResp.Body.Close()
	if headResp.StatusCode/100 != 2 {
		return fmt.Errorf("HEAD %s: status %d", url, headResp.StatusCode)
	}
	etag := headResp.Header.Get("ETag")
	totalSize := headResp.ContentLength
	acceptsRange := headResp.Header.Get("Accept-Ranges") == "bytes"

	// 2. Decide whether to resume.
	var offset int64
	prevMeta, _ := loadMeta(metaPath)
	partInfo, partErr := os.Stat(part)
	canResume := acceptsRange && partErr == nil &&
		prevMeta.URL == url && prevMeta.ETag == etag && etag != ""
	if canResume {
		offset = partInfo.Size()
		if offset >= totalSize && totalSize > 0 {
			offset = 0 // file already as long as expected, but missing meta; restart
		}
	} else {
		_ = os.Remove(part)
		_ = os.Remove(metaPath)
		offset = 0
	}

	// 3. Range GET.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent:
		// good
	case resp.StatusCode == http.StatusOK:
		// server ignored Range; truncate
		offset = 0
		_ = os.Remove(part)
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part)
		return errors.New("server returned 416; clearing and please retry")
	default:
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	// 4. Write meta (so resume works on next call).
	newMeta := Meta{URL: url, ETag: etag, TotalSize: totalSize, SHA256: expectedSHA256}
	if mb, err := newMeta.Marshal(); err == nil {
		_ = os.WriteFile(metaPath, mb, 0o644)
	}

	// 5. Append-write .part with progress.
	flag := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flag, 0o644)
	if err != nil {
		return fmt.Errorf("open part: %w", err)
	}
	written := offset
	start := time.Now()
	buf := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			f.Close()
			return ctx.Err()
		default:
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			written += int64(n)
			if progress != nil {
				speed := int64(0)
				if d := time.Since(start).Seconds(); d > 0 {
					speed = int64(float64(written-offset) / d)
				}
				select {
				case progress <- ProgressEvent{
					Downloaded: written, Total: totalSize, SpeedBps: speed,
					Stage: "download",
				}:
				default:
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return fmt.Errorf("read body: %w", rerr)
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	// 6. Verify sha256.
	if expectedSHA256 != "" {
		got, err := fileSHA256(part)
		if err != nil {
			return err
		}
		if got != expectedSHA256 {
			_ = os.Remove(part)
			_ = os.Remove(metaPath)
			return fmt.Errorf("sha256 mismatch: got %s want %s", got, expectedSHA256)
		}
	}

	// 7. Rename .part → dst, cleanup .meta.
	if err := os.Rename(part, dst); err != nil {
		return err
	}
	_ = os.Remove(metaPath)
	return nil
}

func loadMeta(path string) (Meta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, err
	}
	return UnmarshalMeta(b)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// noUnusedStrconvImport ensures strconv stays referenced if we trim above.
var _ = strconv.Itoa
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/download/...`
Expected: PASS — all 5 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/download/
git commit -m "feat(download): resumable HTTP download with ETag + sha256 verify"
```

---

## P3 internal/oauth (RFC 8628 device-code)

### Task P3.1: oauth.Config + types

**Files:**
- Create: `internal/oauth/types.go`
- Test: `internal/oauth/types_test.go`

- [ ] **Step 1: Write failing test**

`internal/oauth/types_test.go`:
```go
package oauth

import "testing"

func TestConfigEndpoints(t *testing.T) {
	c := Config{
		Endpoint:  "https://x.example.com/",
		AuthPath:  "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token",
	}
	if got := c.AuthURL(); got != "https://x.example.com/api/oauth2/device/auth" {
		t.Errorf("auth url %q", got)
	}
	if got := c.TokenURL(); got != "https://x.example.com/api/oauth2/token" {
		t.Errorf("token url %q", got)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/oauth/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/oauth/types.go`:
```go
// Package oauth implements the OAuth 2.0 Device Authorization Grant (RFC 8628).
package oauth

import (
	"strings"
	"time"
)

type Config struct {
	Endpoint  string // e.g. "https://code.cs.ac.cn"
	AuthPath  string // e.g. "/api/oauth2/device/auth"
	TokenPath string // e.g. "/api/oauth2/token"
	ClientID  string
	Scope     string
}

func (c Config) AuthURL() string  { return joinURL(c.Endpoint, c.AuthPath) }
func (c Config) TokenURL() string { return joinURL(c.Endpoint, c.TokenPath) }

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// DeviceCodeChallenge is what the server returns to /device/auth.
type DeviceCodeChallenge struct {
	DeviceCode              string        `json:"device_code"`
	UserCode                string        `json:"user_code"`
	VerificationURI         string        `json:"verification_uri"`
	VerificationURIComplete string        `json:"verification_uri_complete"`
	ExpiresIn               int           `json:"expires_in"`
	Interval                int           `json:"interval"`
	RetrievedAt             time.Time     `json:"-"`
}

func (c DeviceCodeChallenge) ExpiresAt() time.Time {
	if c.RetrievedAt.IsZero() {
		return time.Now().Add(time.Duration(c.ExpiresIn) * time.Second)
	}
	return c.RetrievedAt.Add(time.Duration(c.ExpiresIn) * time.Second)
}

func (c DeviceCodeChallenge) PollInterval() time.Duration {
	iv := c.Interval
	if iv <= 0 {
		iv = 5
	}
	return time.Duration(iv) * time.Second
}

type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/oauth/...`

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/
git commit -m "feat(oauth): add Config + DeviceCodeChallenge + Token types"
```

### Task P3.2: RequestDeviceCode + PollToken

**Files:**
- Create: `internal/oauth/devicecode.go`
- Test: `internal/oauth/devicecode_test.go`

- [ ] **Step 1: Write failing test**

`internal/oauth/devicecode_test.go`:
```go
package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func fakeHydra(t *testing.T, tokenAfterPolls int32) *httptest.Server {
	t.Helper()
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/device/auth", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.FormValue("client_id") != "test-client" {
			t.Errorf("missing client_id form value")
		}
		json.NewEncoder(w).Encode(DeviceCodeChallenge{
			DeviceCode:              "dev-xyz",
			UserCode:                "ABCD-EFGH",
			VerificationURI:         "http://example/verify",
			VerificationURIComplete: "http://example/verify?u=ABCD-EFGH",
			ExpiresIn:               60,
			Interval:                1,
		})
	})
	mux.HandleFunc("/api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&polls, 1)
		if n <= tokenAfterPolls {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		json.NewEncoder(w).Encode(Token{AccessToken: "AT", TokenType: "Bearer", ExpiresIn: 3600})
	})
	return httptest.NewServer(mux)
}

func TestRequestDeviceCode(t *testing.T) {
	srv := fakeHydra(t, 0)
	defer srv.Close()
	cfg := Config{Endpoint: srv.URL, AuthPath: "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token", ClientID: "test-client", Scope: "openid"}
	ch, err := RequestDeviceCode(context.Background(), cfg)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if ch.UserCode != "ABCD-EFGH" {
		t.Errorf("got user_code %q", ch.UserCode)
	}
	if ch.RetrievedAt.IsZero() {
		t.Errorf("RetrievedAt not set")
	}
}

func TestPollTokenSuccess(t *testing.T) {
	srv := fakeHydra(t, 2) // succeed on 3rd poll
	defer srv.Close()
	cfg := Config{Endpoint: srv.URL, AuthPath: "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token", ClientID: "test-client"}
	ch, err := RequestDeviceCode(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ch.Interval = 1 // ensure fast polling
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tok, err := PollToken(ctx, cfg, ch)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if tok.AccessToken != "AT" {
		t.Errorf("got %+v", tok)
	}
}

func TestPollTokenExpires(t *testing.T) {
	srv := fakeHydra(t, 100) // never succeeds
	defer srv.Close()
	cfg := Config{Endpoint: srv.URL, AuthPath: "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token", ClientID: "test-client"}
	ch, _ := RequestDeviceCode(context.Background(), cfg)
	ch.Interval = 1
	ch.ExpiresIn = 2 // expire fast
	ch.RetrievedAt = time.Now()
	_, err := PollToken(context.Background(), cfg, ch)
	if err == nil || err.Error() != "device code expired" {
		t.Errorf("want device code expired, got %v", err)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/oauth/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/oauth/devicecode.go`:
```go
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RequestDeviceCode POSTs to AuthURL and returns the challenge.
func RequestDeviceCode(ctx context.Context, cfg Config) (DeviceCodeChallenge, error) {
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	if cfg.Scope != "" {
		form.Set("scope", cfg.Scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.AuthURL(),
		strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceCodeChallenge{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return DeviceCodeChallenge{}, fmt.Errorf("device auth: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return DeviceCodeChallenge{}, fmt.Errorf("device auth: status %d", resp.StatusCode)
	}
	var ch DeviceCodeChallenge
	if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
		return DeviceCodeChallenge{}, fmt.Errorf("decode device auth: %w", err)
	}
	ch.RetrievedAt = time.Now()
	if ch.VerificationURIComplete == "" {
		// Some implementations only return VerificationURI + UserCode
		ch.VerificationURIComplete = ch.VerificationURI
	}
	return ch, nil
}

const grantTypeDeviceCode = "urn:ietf:params:oauth:grant-type:device_code"

type tokenErr struct {
	Code string `json:"error"`
	Desc string `json:"error_description"`
}

// PollToken polls TokenURL at challenge.Interval until success, expiry,
// or ctx is cancelled.
func PollToken(ctx context.Context, cfg Config, ch DeviceCodeChallenge) (Token, error) {
	interval := ch.PollInterval()
	deadline := ch.ExpiresAt()

	for {
		now := time.Now()
		if now.After(deadline) {
			return Token{}, errors.New("device code expired")
		}
		tok, errCode, err := tokenOnce(ctx, cfg, ch.DeviceCode)
		if err != nil {
			return Token{}, err
		}
		switch errCode {
		case "":
			return tok, nil
		case "authorization_pending":
			// keep polling
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return Token{}, errors.New("user denied authorization")
		case "expired_token":
			return Token{}, errors.New("device code expired")
		default:
			return Token{}, fmt.Errorf("oauth token error: %s", errCode)
		}
		select {
		case <-ctx.Done():
			return Token{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func tokenOnce(ctx context.Context, cfg Config, deviceCode string) (Token, string, error) {
	form := url.Values{}
	form.Set("grant_type", grantTypeDeviceCode)
	form.Set("client_id", cfg.ClientID)
	form.Set("device_code", deviceCode)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL(),
		strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Token{}, "", fmt.Errorf("token poll: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var tok Token
		if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
			return Token{}, "", err
		}
		return tok, "", nil
	}
	// 400 with JSON {error: "..."} is normal device-flow signalling
	var te tokenErr
	if err := json.NewDecoder(resp.Body).Decode(&te); err != nil {
		return Token{}, "", fmt.Errorf("token poll: status %d", resp.StatusCode)
	}
	if te.Code == "" {
		return Token{}, "", fmt.Errorf("token poll: status %d (no error code)", resp.StatusCode)
	}
	return Token{}, te.Code, nil
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/oauth/...`
Expected: PASS — 3 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/
git commit -m "feat(oauth): implement RequestDeviceCode + PollToken"
```

---

## P4 modelserver + agentserver clients

### Task P4.1: modelserver client — types + ListProjects

**Files:**
- Create: `internal/modelserver/types.go`
- Create: `internal/modelserver/client.go`
- Test: `internal/modelserver/client_test.go`

- [ ] **Step 1: Write failing test**

`internal/modelserver/client_test.go`:
```go
package modelserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListProjects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects" {
			t.Errorf("path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer AT" {
			t.Errorf("auth %q", got)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []Project{{ID: "p1", Name: "default"}, {ID: "p2", Name: "other"}},
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	ps, err := c.ListProjects(context.Background(), "AT")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != "p1" {
		t.Errorf("got %+v", ps)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/modelserver/...`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement types + client skeleton**

`internal/modelserver/types.go`:
```go
package modelserver

import "time"

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type APIKey struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Name          string    `json:"name"`
	KeySuffix     string    `json:"key_suffix"`
	Status        string    `json:"status"`
	Secret        string    `json:"-"` // populated from create response wrapper
	CreatedAt     time.Time `json:"created_at"`
}
```

`internal/modelserver/client.go`:
```go
// Package modelserver wraps the relevant HTTP endpoints of code.cs.ac.cn.
package modelserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    http.DefaultClient,
	}
}

func (c *Client) ListProjects(ctx context.Context, accessToken string) ([]Project, error) {
	var wrap struct {
		Data []Project `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/projects", accessToken, nil, &wrap); err != nil {
		return nil, err
	}
	return wrap.Data, nil
}

// do is the shared JSON request helper.
func (c *Client) do(ctx context.Context, method, path, token string,
	body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/modelserver/...`

- [ ] **Step 5: Commit**

```bash
git add internal/modelserver/
git commit -m "feat(modelserver): Client + ListProjects"
```

### Task P4.2: modelserver — CreateProject + CreateAPIKey

**Files:**
- Modify: `internal/modelserver/client.go`
- Modify: `internal/modelserver/client_test.go`

- [ ] **Step 1: Add failing tests**

Append to `internal/modelserver/client_test.go`:
```go
func TestCreateProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/projects" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "default" {
			t.Errorf("name %q", body["name"])
		}
		json.NewEncoder(w).Encode(map[string]Project{
			"data": {ID: "new-1", Name: "default"},
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	p, err := c.CreateProject(context.Background(), "AT", "default")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "new-1" {
		t.Errorf("got %+v", p)
	}
}

func TestCreateAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects/p1/keys" {
			t.Errorf("path %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": APIKey{ID: "k1", ProjectID: "p1", Name: "x", KeySuffix: "wxyz", Status: "active"},
			"key":  "ms-1234567890abcdef",
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	k, err := c.CreateAPIKey(context.Background(), "AT", "p1", "x")
	if err != nil {
		t.Fatal(err)
	}
	if k.Secret != "ms-1234567890abcdef" || k.ID != "k1" {
		t.Errorf("got %+v", k)
	}
}

func TestPickOrCreateProject_FoundDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": []Project{{ID: "p1", Name: "default"}},
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	p, err := c.PickOrCreateProject(context.Background(), "AT", "default")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "p1" {
		t.Errorf("expected existing, got %+v", p)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/modelserver/...`
Expected: FAIL — `undefined: CreateProject`, `CreateAPIKey`, `PickOrCreateProject`.

- [ ] **Step 3: Add methods**

Append to `internal/modelserver/client.go`:
```go
func (c *Client) CreateProject(ctx context.Context, token, name string) (Project, error) {
	var wrap struct {
		Data Project `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"name": name}, &wrap); err != nil {
		return Project{}, err
	}
	return wrap.Data, nil
}

func (c *Client) CreateAPIKey(ctx context.Context, token, projectID, name string) (APIKey, error) {
	var wrap struct {
		Data APIKey `json:"data"`
		Key  string `json:"key"`
	}
	if err := c.do(ctx, http.MethodPost,
		"/api/v1/projects/"+projectID+"/keys", token,
		map[string]any{"name": name}, &wrap); err != nil {
		return APIKey{}, err
	}
	wrap.Data.Secret = wrap.Key
	return wrap.Data, nil
}

// PickOrCreateProject finds a project named `name`; if none, creates it.
func (c *Client) PickOrCreateProject(ctx context.Context, token, name string) (Project, error) {
	ps, err := c.ListProjects(ctx, token)
	if err != nil {
		return Project{}, err
	}
	for _, p := range ps {
		if p.Name == name {
			return p, nil
		}
	}
	return c.CreateProject(ctx, token, name)
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/modelserver/...`

- [ ] **Step 5: Commit**

```bash
git add internal/modelserver/
git commit -m "feat(modelserver): CreateProject/CreateAPIKey/PickOrCreateProject"
```

### Task P4.3: agentserver client — workspace + ws api key

**Files:**
- Create: `internal/agentserver/types.go`
- Create: `internal/agentserver/client.go`
- Test: `internal/agentserver/client_test.go`

- [ ] **Step 1: Write failing test**

`internal/agentserver/client_test.go`:
```go
package agentserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetOrCreateDefaultWorkspace_Existing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/workspaces" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []Workspace{{ID: "ws-1", Name: "default"}},
			})
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	c := New(srv.URL)
	ws, err := c.GetOrCreateDefaultWorkspace(context.Background(), "AT", "default")
	if err != nil {
		t.Fatal(err)
	}
	if ws.ID != "ws-1" {
		t.Errorf("got %+v", ws)
	}
}

func TestGetOrCreateDefaultWorkspace_Creates(t *testing.T) {
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/workspaces":
			json.NewEncoder(w).Encode(map[string]any{"data": []Workspace{}})
		case r.Method == "POST" && r.URL.Path == "/api/workspaces":
			created = true
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "default" {
				t.Errorf("name %q", body["name"])
			}
			json.NewEncoder(w).Encode(map[string]Workspace{"data": {ID: "ws-new", Name: "default"}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := New(srv.URL)
	ws, err := c.GetOrCreateDefaultWorkspace(context.Background(), "AT", "default")
	if err != nil {
		t.Fatal(err)
	}
	if !created || ws.ID != "ws-new" {
		t.Errorf("got %+v, created=%v", ws, created)
	}
}

func TestCreateWorkspaceAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/workspaces/ws-1/api-keys" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": WorkspaceAPIKey{ID: "k1", WorkspaceID: "ws-1", Name: "x", KeySuffix: "abcd"},
			"key":  "ws-sk-aaaaaa",
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	k, err := c.CreateWorkspaceAPIKey(context.Background(), "AT", "ws-1", "x")
	if err != nil {
		t.Fatal(err)
	}
	if k.Secret != "ws-sk-aaaaaa" {
		t.Errorf("got %+v", k)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/agentserver/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/agentserver/types.go`:
```go
package agentserver

import "time"

type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkspaceAPIKey struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	KeySuffix   string    `json:"key_suffix"`
	Status      string    `json:"status"`
	Secret      string    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
}
```

`internal/agentserver/client.go`:
```go
// Package agentserver wraps the relevant HTTP endpoints of agent.cs.ac.cn.
package agentserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    http.DefaultClient,
	}
}

func (c *Client) ListWorkspaces(ctx context.Context, token string) ([]Workspace, error) {
	var wrap struct {
		Data []Workspace `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/workspaces", token, nil, &wrap); err != nil {
		return nil, err
	}
	return wrap.Data, nil
}

func (c *Client) CreateWorkspace(ctx context.Context, token, name string) (Workspace, error) {
	var wrap struct {
		Data Workspace `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/workspaces", token,
		map[string]string{"name": name}, &wrap); err != nil {
		return Workspace{}, err
	}
	return wrap.Data, nil
}

func (c *Client) GetOrCreateDefaultWorkspace(ctx context.Context, token, name string) (Workspace, error) {
	ws, err := c.ListWorkspaces(ctx, token)
	if err != nil {
		return Workspace{}, err
	}
	for _, w := range ws {
		if w.Name == name {
			return w, nil
		}
	}
	return c.CreateWorkspace(ctx, token, name)
}

func (c *Client) CreateWorkspaceAPIKey(ctx context.Context, token, workspaceID, name string) (WorkspaceAPIKey, error) {
	var wrap struct {
		Data WorkspaceAPIKey `json:"data"`
		Key  string          `json:"key"`
	}
	if err := c.do(ctx, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/api-keys", token,
		map[string]any{"name": name}, &wrap); err != nil {
		return WorkspaceAPIKey{}, err
	}
	wrap.Data.Secret = wrap.Key
	return wrap.Data, nil
}

func (c *Client) do(ctx context.Context, method, path, token string,
	body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, b)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/agentserver/...`

- [ ] **Step 5: Commit**

```bash
git add internal/agentserver/
git commit -m "feat(agentserver): Client with workspace + workspace api key ops"
```

---

## P5 codex/config + env/windows

### Task P5.1: codex.UpdateConfig (toml merge with backup)

**Files:**
- Create: `internal/codex/config.go`
- Test: `internal/codex/config_test.go`

- [ ] **Step 1: Write failing test**

`internal/codex/config_test.go`:
```go
package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateConfig_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := UpdateConfig(path, Settings{
		Provider: "modelserver", Model: "gpt-5.5",
		BaseURL: "https://code.ai.cs.ac.cn/v1", EnvKey: "OPENAI_API_KEY",
		WireAPI: "responses",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`model = "gpt-5.5"`,
		`[model_providers.modelserver]`,
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestUpdateConfig_MergeKeepsOtherProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	prior := `model_provider = "old"
model = "gpt-4"
some_other_key = "stays"

[model_providers.old]
name = "old"
base_url = "https://old/v1"
`
	if err := os.WriteFile(path, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	err := UpdateConfig(path, Settings{
		Provider: "modelserver", Model: "gpt-5.5",
		BaseURL: "https://code.ai.cs.ac.cn/v1", EnvKey: "OPENAI_API_KEY",
		WireAPI: "responses",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	// Must keep [model_providers.old] and the unrelated key
	for _, want := range []string{
		`[model_providers.old]`,
		`some_other_key = "stays"`,
		`[model_providers.modelserver]`,
		`model_provider = "modelserver"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in merged config:\n%s", want, s)
		}
	}
	// Backup created
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) == 0 {
		t.Errorf("expected backup")
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/codex/...`
Expected: FAIL.

- [ ] **Step 3: Add toml dep**

```bash
go get github.com/pelletier/go-toml/v2@latest
go mod tidy
```

- [ ] **Step 4: Implement**

`internal/codex/config.go`:
```go
// Package codex writes/merges ~/.codex/config.toml for the codex CLI.
package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Settings struct {
	Provider string // e.g. "modelserver"
	Model    string // e.g. "gpt-5.5"
	BaseURL  string // e.g. "https://code.ai.cs.ac.cn/v1"
	EnvKey   string // e.g. "OPENAI_API_KEY"
	WireAPI  string // e.g. "responses"
}

// UpdateConfig merges Settings into the config.toml at `path`, preserving
// any unrelated top-level keys and any [model_providers.X] tables other
// than ours. The original is backed up to path.bak.<unix-ts> first.
func UpdateConfig(path string, s Settings) error {
	if s.Provider == "" {
		return errors.New("Settings.Provider required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir codex dir: %w", err)
	}

	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(b, &root); err != nil {
			return fmt.Errorf("parse existing config.toml: %w", err)
		}
		// backup
		backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
		_ = os.WriteFile(backup, b, 0o644)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config.toml: %w", err)
	}

	root["model_provider"] = s.Provider
	if s.Model != "" {
		root["model"] = s.Model
	}
	providers, _ := root["model_providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	providers[s.Provider] = map[string]any{
		"name":     s.Provider,
		"base_url": s.BaseURL,
		"env_key":  s.EnvKey,
		"wire_api": s.WireAPI,
	}
	root["model_providers"] = providers

	out, err := toml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal config.toml: %w", err)
	}
	return os.WriteFile(path, out, 0o644)
}
```

- [ ] **Step 5: Run, expect PASS**

Run: `go test -race ./internal/codex/...`

- [ ] **Step 6: Commit**

```bash
git add internal/codex/ go.mod go.sum
git commit -m "feat(codex): toml merge for ~/.codex/config.toml with backup"
```

### Task P5.2: env/windows — setx + WM_SETTINGCHANGE

**Files:**
- Create: `internal/env/persist.go` (cross-OS dispatch)
- Create: `internal/env/persist_windows.go`
- Create: `internal/env/persist_other.go`
- Test: `internal/env/persist_test.go`
- Test (Windows only): `internal/env/persist_windows_test.go`

- [ ] **Step 1: Write OS-neutral failing test**

`internal/env/persist_test.go`:
```go
package env

import "testing"

func TestPersistUserEnv_NoEmptyKey(t *testing.T) {
	if err := PersistUserEnv("", "v"); err == nil {
		t.Errorf("expected error for empty key")
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/env/...`
Expected: FAIL.

- [ ] **Step 3: Implement dispatch + stubs**

`internal/env/persist.go`:
```go
// Package env persists user-level environment variables on Windows and
// broadcasts WM_SETTINGCHANGE so already-running processes can refresh.
//
// On non-Windows platforms PersistUserEnv is a stub (returns nil); the v1
// installer is Windows-only.
package env

import "errors"

func PersistUserEnv(key, value string) error {
	if key == "" {
		return errors.New("env.PersistUserEnv: key required")
	}
	return persistUserEnv(key, value)
}
```

`internal/env/persist_windows.go`:
```go
//go:build windows

package env

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func persistUserEnv(key, value string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`,
		registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(key, value); err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}
	return broadcastSettingChange("Environment")
}

const (
	HWND_BROADCAST   = uintptr(0xFFFF)
	WM_SETTINGCHANGE = 0x001A
	SMTO_ABORTIFHUNG = 0x0002
)

func broadcastSettingChange(lparam string) error {
	user32 := windows.NewLazySystemDLL("user32.dll")
	sendMessageTimeout := user32.NewProc("SendMessageTimeoutW")
	lp, _ := windows.UTF16PtrFromString(lparam)
	var result uintptr
	r1, _, e1 := sendMessageTimeout.Call(
		HWND_BROADCAST,
		WM_SETTINGCHANGE,
		0,
		uintptr(unsafe.Pointer(lp)),
		SMTO_ABORTIFHUNG,
		5000,
		uintptr(unsafe.Pointer(&result)),
	)
	if r1 == 0 {
		return fmt.Errorf("SendMessageTimeout: %v", e1)
	}
	return nil
}
```

`internal/env/persist_other.go`:
```go
//go:build !windows

package env

// On non-Windows v1 builds, this is a no-op so unit tests on Linux pass.
func persistUserEnv(key, value string) error { return nil }
```

- [ ] **Step 4: Add windows sys dep**

```bash
go get golang.org/x/sys/windows@latest
go mod tidy
```

- [ ] **Step 5: Write Windows-only test**

`internal/env/persist_windows_test.go`:
```go
//go:build windows

package env

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestPersistUserEnv_Windows(t *testing.T) {
	const key = "AGENTSERVER_VSCODE_TEST_VAR"
	const val = "hello-windows"
	if err := PersistUserEnv(key, val); err != nil {
		t.Fatalf("persist: %v", err)
	}
	defer func() {
		k, _ := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
		_ = k.DeleteValue(key)
		k.Close()
	}()
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	got, _, err := k.GetStringValue(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != val {
		t.Errorf("got %q want %q", got, val)
	}
}
```

- [ ] **Step 6: Run on Linux (must still pass with stubs)**

Run: `go test -race ./internal/env/...`
Expected: PASS — only `TestPersistUserEnv_NoEmptyKey` runs; Windows test excluded by build tag.

- [ ] **Step 7: Cross-build for Windows must compile**

Run: `GOOS=windows GOARCH=amd64 go build ./...`
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/env/ go.mod go.sum
git commit -m "feat(env): persist user env via HKCU\\Environment + WM_SETTINGCHANGE"
```

---

## P6 vscode/{detect, install, settings, extensions}

### Task P6.1: vscode/detect (cross-OS find code)

**Files:**
- Create: `internal/vscode/detect.go`
- Create: `internal/vscode/detect_windows.go`
- Create: `internal/vscode/detect_other.go`
- Test: `internal/vscode/detect_test.go`

- [ ] **Step 1: Write failing test**

`internal/vscode/detect_test.go`:
```go
package vscode

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1.96.0\nabcdef\nx64\n", "1.96.0"},
		{"  1.85.2  ", "1.85.2"},
		{"", ""},
	}
	for _, c := range cases {
		if got := parseVersion(c.in); got != c.want {
			t.Errorf("parseVersion(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestDetect_FakeExe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "code")
	script := "#!/bin/bash\necho 1.96.0\necho abcdef\necho x64\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	det, err := detectAt(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !det.Installed || det.Version != "1.96.0" || det.Path != exe {
		t.Errorf("got %+v", det)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/vscode/...`
Expected: FAIL.

- [ ] **Step 3: Implement detect.go (shared)**

`internal/vscode/detect.go`:
```go
// Package vscode covers detection, install, configuration, and extension
// management for the VS Code editor.
package vscode

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Detected struct {
	Installed bool
	Path      string
	Version   string
}

// Detect tries to locate a usable `code` command and parse its version.
// On Windows checks standard install locations + PATH.
func Detect() (Detected, error) {
	return detectPlatform()
}

func detectAt(path string) (Detected, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return Detected{}, fmt.Errorf("%s --version: %w", path, err)
	}
	v := parseVersion(string(out))
	if v == "" {
		return Detected{}, fmt.Errorf("could not parse version from: %q", out)
	}
	return Detected{Installed: true, Path: path, Version: v}, nil
}

func parseVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// First non-empty line is the version.
		return line
	}
	return ""
}
```

`internal/vscode/detect_windows.go`:
```go
//go:build windows

package vscode

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

func detectPlatform() (Detected, error) {
	// 1. Try `where code.cmd` / `code.exe` in PATH.
	for _, name := range []string{"code.cmd", "code.exe", "code"} {
		if p, err := exec.LookPath(name); err == nil {
			if det, err := detectAt(p); err == nil {
				return det, nil
			}
		}
	}
	// 2. Standard user-install location.
	candidates := []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"),
			"Programs", "Microsoft VS Code", "bin", "code.cmd"),
		filepath.Join(os.Getenv("ProgramFiles"),
			"Microsoft VS Code", "bin", "code.cmd"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"),
			"Microsoft VS Code", "bin", "code.cmd"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			if det, err := detectAt(c); err == nil {
				return det, nil
			}
		}
	}
	return Detected{Installed: false}, errors.New("VS Code not found")
}
```

`internal/vscode/detect_other.go`:
```go
//go:build !windows

package vscode

import (
	"errors"
	"os/exec"
)

func detectPlatform() (Detected, error) {
	if p, err := exec.LookPath("code"); err == nil {
		return detectAt(p)
	}
	return Detected{Installed: false}, errors.New("VS Code not found")
}
```

- [ ] **Step 4: Run, expect PASS on Linux**

Run: `go test -race ./internal/vscode/...`
Expected: PASS (TestParseVersion + TestDetect_FakeExe).

- [ ] **Step 5: Cross-build Windows OK**

Run: `GOOS=windows GOARCH=amd64 go build ./...`

- [ ] **Step 6: Commit**

```bash
git add internal/vscode/
git commit -m "feat(vscode): detect installation cross-platform"
```

### Task P6.2: vscode/install (Windows /VERYSILENT)

**Files:**
- Create: `internal/vscode/install.go`
- Create: `internal/vscode/install_windows.go`
- Create: `internal/vscode/install_other.go`
- Test: `internal/vscode/install_test.go`

- [ ] **Step 1: Write failing test (URL plan only — runtime install needs real OS)**

`internal/vscode/install_test.go`:
```go
package vscode

import "testing"

func TestPlanInstall_Windows(t *testing.T) {
	p := planInstallFor("windows", "amd64")
	if p.URL == "" || p.SHA256 == "" {
		t.Errorf("missing URL/sha: %+v", p)
	}
	if p.InstallerType != "InnoSetup" {
		t.Errorf("type %q", p.InstallerType)
	}
	if len(p.SilentArgs) == 0 {
		t.Errorf("silent args empty")
	}
}

func TestPlanInstall_Unsupported(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic for unsupported")
		}
	}()
	_ = planInstallFor("plan9", "amd64")
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/vscode/...`
Expected: FAIL — `undefined: planInstallFor`.

- [ ] **Step 3: Implement**

`internal/vscode/install.go`:
```go
package vscode

import (
	"context"
	"fmt"
	"runtime"
)

type InstallPlan struct {
	URL           string
	SHA256        string
	InstallerType string   // "InnoSetup"
	FileExt       string   // ".exe"
	SilentArgs    []string // e.g. ["/VERYSILENT", "/MERGETASKS=!runcode,addtopath"]
}

// LockedVersion is the VS Code version we ship. Bumping requires updating
// the SHA256 below (fetch from https://code.visualstudio.com/sha?build=stable).
const LockedVersion = "1.96.0"

// lockedSHA256Win64User MUST be updated when LockedVersion changes.
// Fetch with:
//   curl -s 'https://update.code.visualstudio.com/api/versions/1.96.0/win32-x64-user/stable' | jq -r .sha256hash
// Leave as REPLACE_ME until first build — DownloadResumable will fail
// loudly so a developer notices.
const lockedSHA256Win64User = "REPLACE_ME_run_curl_command_above_and_paste_hex"

func PlanInstall() InstallPlan {
	return planInstallFor(runtime.GOOS, runtime.GOARCH)
}

func planInstallFor(goos, goarch string) InstallPlan {
	if goos != "windows" || goarch != "amd64" {
		panic(fmt.Sprintf("vscode install: unsupported %s/%s in v1", goos, goarch))
	}
	return InstallPlan{
		URL: "https://update.code.visualstudio.com/" + LockedVersion +
			"/win32-x64-user/stable",
		SHA256:        lockedSHA256Win64User,
		InstallerType: "InnoSetup",
		FileExt:       ".exe",
		SilentArgs: []string{
			"/VERYSILENT",
			"/MERGETASKS=!runcode,addtopath",
			"/SUPPRESSMSGBOXES",
			"/NORESTART",
		},
	}
}

// SilentInstall runs the downloaded installer with platform-appropriate args.
func SilentInstall(ctx context.Context, downloadedPath string, plan InstallPlan) error {
	return silentInstallPlatform(ctx, downloadedPath, plan)
}
```

`internal/vscode/install_windows.go`:
```go
//go:build windows

package vscode

import (
	"context"
	"fmt"
	"os/exec"
)

func silentInstallPlatform(ctx context.Context, path string, plan InstallPlan) error {
	cmd := exec.CommandContext(ctx, path, plan.SilentArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("vscode installer %s %v: %w (%s)", path, plan.SilentArgs, err, out)
	}
	return nil
}
```

`internal/vscode/install_other.go`:
```go
//go:build !windows

package vscode

import (
	"context"
	"errors"
)

func silentInstallPlatform(ctx context.Context, path string, plan InstallPlan) error {
	return errors.New("vscode.SilentInstall: only Windows is supported in v1")
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/vscode/...`
Expected: PASS — TestPlanInstall_Windows + TestPlanInstall_Unsupported.

- [ ] **Step 5: Cross-build OK**

Run: `GOOS=windows GOARCH=amd64 go build ./... && GOOS=linux go build ./...`

- [ ] **Step 6: Commit**

```bash
git add internal/vscode/
git commit -m "feat(vscode): plan + run silent Inno Setup install on Windows"
```

### Task P6.3: vscode/settings (merge-write user settings.json)

**Files:**
- Create: `internal/vscode/settings.go`
- Test: `internal/vscode/settings_test.go`

- [ ] **Step 1: Write failing test**

`internal/vscode/settings_test.go`:
```go
package vscode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSettings_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "User", "settings.json")
	err := WriteSettings(path, SettingsInput{CodexAbsPath: `C:\bin\codex.exe`})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not valid json: %v", err)
	}
	if m["locale"] != "zh-cn" {
		t.Errorf("locale: %v", m["locale"])
	}
	if m["agentserverVscode.terminal.profileName"] != "codex" {
		t.Errorf("profile: %v", m["agentserverVscode.terminal.profileName"])
	}
	profiles := m["terminal.integrated.profiles.windows"].(map[string]any)
	codex := profiles["codex"].(map[string]any)
	args := codex["args"].([]any)
	if args[1] != `C:\bin\codex.exe` {
		t.Errorf("codex path not embedded: %v", args)
	}
}

func TestWriteSettings_PreservesUserKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "User", "settings.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	prior := `{"editor.fontSize": 14, "custom.key": "keep me"}`
	os.WriteFile(path, []byte(prior), 0o644)

	err := WriteSettings(path, SettingsInput{CodexAbsPath: `C:\bin\codex.exe`})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var m map[string]any
	json.Unmarshal(b, &m)
	if m["editor.fontSize"] != float64(14) {
		t.Errorf("editor.fontSize lost: %v", m["editor.fontSize"])
	}
	if m["custom.key"] != "keep me" {
		t.Errorf("custom.key lost: %v", m["custom.key"])
	}
	if m["locale"] != "zh-cn" {
		t.Errorf("locale not added: %v", m["locale"])
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/vscode/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/vscode/settings.go`:
```go
package vscode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type SettingsInput struct {
	CodexAbsPath string // absolute path to codex.exe
}

// WriteSettings merges agentserver-vscode defaults into path. Existing
// user keys not managed by us are preserved.
func WriteSettings(path string, in SettingsInput) error {
	if in.CodexAbsPath == "" {
		return fmt.Errorf("WriteSettings: CodexAbsPath required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	m := map[string]any{}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("parse existing settings.json: %w", err)
		}
	}
	overrides := map[string]any{
		"locale":                          "zh-cn",
		"telemetry.telemetryLevel":        "off",
		"workbench.startupEditor":         "none",
		"workbench.activityBar.location":  "hidden",
		"workbench.statusBar.visible":     true,
		"workbench.panel.defaultLocation": "bottom",
		"workbench.panel.opensMaximized":  "always",

		"agentserverVscode.panel.allowed":             []string{"terminal", "output"},
		"agentserverVscode.startup.openFolderIfEmpty": true,
		"agentserverVscode.terminal.respawnOnClose":   true,
		"agentserverVscode.terminal.profileName":      "codex",

		"terminal.integrated.defaultProfile.windows": "codex",
		"terminal.integrated.profiles.windows": map[string]any{
			"codex": map[string]any{
				"path": `C:\Windows\System32\cmd.exe`,
				"args": []string{"/k", in.CodexAbsPath},
			},
		},
	}
	for k, v := range overrides {
		m[k] = v
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/vscode/...`

- [ ] **Step 5: Commit**

```bash
git add internal/vscode/
git commit -m "feat(vscode): merge-write user settings.json with codex terminal profile"
```

### Task P6.4: vscode/extensions (install via `code --install-extension`)

**Files:**
- Create: `internal/vscode/extensions.go`
- Test: `internal/vscode/extensions_test.go`

- [ ] **Step 1: Write failing test using a fake `code` script**

`internal/vscode/extensions_test.go`:
```go
package vscode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallExtensions_RecordsCalls(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	codeExe := filepath.Join(dir, "code")
	logFile := filepath.Join(dir, "calls.log")
	script := "#!/bin/bash\necho \"$@\" >> " + logFile + "\n"
	if err := os.WriteFile(codeExe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	err := InstallExtensions(context.Background(), Installer{
		CodeExe:      codeExe,
		UserDataDir:  filepath.Join(dir, "data"),
		ExtensionsDir: filepath.Join(dir, "ext"),
		Extensions:   []string{"MS-CEINTL.vscode-language-pack-zh-hans", "/tmp/our.vsix"},
	})
	if err != nil {
		t.Fatal(err)
	}
	logged, _ := os.ReadFile(logFile)
	s := string(logged)
	if !strings.Contains(s, "MS-CEINTL.vscode-language-pack-zh-hans") {
		t.Errorf("missing zh pack call: %s", s)
	}
	if !strings.Contains(s, "/tmp/our.vsix") {
		t.Errorf("missing vsix call: %s", s)
	}
	if !strings.Contains(s, "--user-data-dir") {
		t.Errorf("missing --user-data-dir: %s", s)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/vscode/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/vscode/extensions.go`:
```go
package vscode

import (
	"context"
	"fmt"
	"os/exec"
)

type Installer struct {
	CodeExe       string
	UserDataDir   string
	ExtensionsDir string
	Extensions    []string // ids ("publisher.name") or absolute .vsix paths
}

func InstallExtensions(ctx context.Context, in Installer) error {
	if in.CodeExe == "" || in.UserDataDir == "" || in.ExtensionsDir == "" {
		return fmt.Errorf("InstallExtensions: CodeExe/UserDataDir/ExtensionsDir required")
	}
	for _, ext := range in.Extensions {
		args := []string{
			"--user-data-dir", in.UserDataDir,
			"--extensions-dir", in.ExtensionsDir,
			"--install-extension", ext,
			"--force",
		}
		cmd := exec.CommandContext(ctx, in.CodeExe, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("install %s: %w (%s)", ext, err, out)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/vscode/...`

- [ ] **Step 5: Commit**

```bash
git add internal/vscode/
git commit -m "feat(vscode): install extensions via code --install-extension"
```

---

## P7 shortcut/windows (.lnk + 注册表)

### Task P7.1: shortcut dispatch + stubs

**Files:**
- Create: `internal/shortcut/shortcut.go`
- Create: `internal/shortcut/shortcut_other.go`
- Test: `internal/shortcut/shortcut_test.go`

- [ ] **Step 1: Write failing test**

`internal/shortcut/shortcut_test.go`:
```go
package shortcut

import "testing"

func TestInputValidation(t *testing.T) {
	if err := EnsureDesktopShortcut(DesktopInput{}); err == nil {
		t.Errorf("expected error on empty input")
	}
	if err := InstallContextMenu(ContextMenuInput{}); err == nil {
		t.Errorf("expected error on empty input")
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/shortcut/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

`internal/shortcut/shortcut.go`:
```go
// Package shortcut creates the desktop .lnk and folder-context-menu
// integration. Windows is the only platform implemented in v1.
package shortcut

import "errors"

type DesktopInput struct {
	Name      string // e.g. "agentserver-vscode"
	TargetExe string // absolute path to launcher.exe
	Args      string // launcher takes none by default
	IconPath  string // absolute path to .ico
	WorkDir   string // working directory; "" → user home
}

type ContextMenuInput struct {
	MenuLabel string // localized label, e.g. "用 agentserver-vscode 打开"
	HandlerExe string // absolute path to open-folder.exe
	IconPath  string // absolute path to .ico
	RegistryKeySuffix string // e.g. "AgentserverVscode"
}

func EnsureDesktopShortcut(in DesktopInput) error {
	if in.Name == "" || in.TargetExe == "" {
		return errors.New("EnsureDesktopShortcut: Name and TargetExe required")
	}
	return ensureDesktopShortcutPlatform(in)
}

func InstallContextMenu(in ContextMenuInput) error {
	if in.MenuLabel == "" || in.HandlerExe == "" || in.RegistryKeySuffix == "" {
		return errors.New("InstallContextMenu: MenuLabel/HandlerExe/RegistryKeySuffix required")
	}
	return installContextMenuPlatform(in)
}

func UninstallAll(in ContextMenuInput, desktopName string) error {
	return uninstallAllPlatform(in, desktopName)
}
```

`internal/shortcut/shortcut_other.go`:
```go
//go:build !windows

package shortcut

import "errors"

func ensureDesktopShortcutPlatform(DesktopInput) error {
	return errors.New("shortcut: only Windows is supported in v1")
}
func installContextMenuPlatform(ContextMenuInput) error {
	return errors.New("shortcut: only Windows is supported in v1")
}
func uninstallAllPlatform(ContextMenuInput, string) error { return nil }
```

- [ ] **Step 4: Run, expect PASS on Linux**

Run: `go test -race ./internal/shortcut/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/shortcut/
git commit -m "feat(shortcut): API + non-Windows stubs"
```

### Task P7.2: shortcut/windows — desktop .lnk via COM

**Files:**
- Create: `internal/shortcut/shortcut_windows.go`
- Test: `internal/shortcut/shortcut_windows_test.go`

- [ ] **Step 1: Write Windows-only test**

`internal/shortcut/shortcut_windows_test.go`:
```go
//go:build windows

package shortcut

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestEnsureDesktopShortcut_Windows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	if err := os.MkdirAll(filepath.Join(dir, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := DesktopInput{
		Name:      "agentserver-vscode-test",
		TargetExe: `C:\Windows\System32\notepad.exe`,
		IconPath:  `C:\Windows\System32\notepad.exe`,
		WorkDir:   dir,
	}
	if err := EnsureDesktopShortcut(in); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "Desktop", "agentserver-vscode-test.lnk")
	if _, err := os.Stat(link); err != nil {
		t.Errorf("expected .lnk at %s", link)
	}
}

func TestInstallContextMenu_Windows(t *testing.T) {
	in := ContextMenuInput{
		MenuLabel: "Test menu label",
		HandlerExe: `C:\Windows\System32\notepad.exe`,
		IconPath:   `C:\Windows\System32\notepad.exe`,
		RegistryKeySuffix: "AgentserverVscodeTest",
	}
	if err := InstallContextMenu(in); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Cleanup
		registry.DeleteKey(registry.CURRENT_USER,
			`Software\Classes\Directory\shell\AgentserverVscodeTest\command`)
		registry.DeleteKey(registry.CURRENT_USER,
			`Software\Classes\Directory\shell\AgentserverVscodeTest`)
		registry.DeleteKey(registry.CURRENT_USER,
			`Software\Classes\Directory\Background\shell\AgentserverVscodeTest\command`)
		registry.DeleteKey(registry.CURRENT_USER,
			`Software\Classes\Directory\Background\shell\AgentserverVscodeTest`)
	}()
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Classes\Directory\shell\AgentserverVscodeTest`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	label, _, _ := k.GetStringValue("")
	if label != "Test menu label" {
		t.Errorf("label %q", label)
	}
}
```

- [ ] **Step 2: Implement**

`internal/shortcut/shortcut_windows.go`:
```go
//go:build windows

package shortcut

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"
)

func ensureDesktopShortcutPlatform(in DesktopInput) error {
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
	if err := os.MkdirAll(desktop, 0o755); err != nil {
		return err
	}
	linkPath := filepath.Join(desktop, in.Name+".lnk")
	return createShellLink(linkPath, in.TargetExe, in.Args, in.IconPath, in.WorkDir)
}

func createShellLink(linkPath, target, args, icon, workdir string) error {
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		// returns S_FALSE if already initialized, which is fine
		oleErr, ok := err.(*ole.OleError)
		if !ok || oleErr.Code() != 0x00000001 /* S_FALSE */ {
			return fmt.Errorf("CoInitializeEx: %w", err)
		}
	}
	defer ole.CoUninitialize()

	unknown, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return fmt.Errorf("create WScript.Shell: %w", err)
	}
	defer unknown.Release()
	shell, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer shell.Release()

	shortcut, err := oleutil.CallMethod(shell, "CreateShortcut", linkPath)
	if err != nil {
		return fmt.Errorf("CreateShortcut: %w", err)
	}
	idispatch := shortcut.ToIDispatch()
	defer idispatch.Release()
	if _, err := oleutil.PutProperty(idispatch, "TargetPath", target); err != nil {
		return err
	}
	if args != "" {
		oleutil.PutProperty(idispatch, "Arguments", args)
	}
	if workdir != "" {
		oleutil.PutProperty(idispatch, "WorkingDirectory", workdir)
	}
	if icon != "" {
		oleutil.PutProperty(idispatch, "IconLocation", icon+",0")
	}
	if _, err := oleutil.CallMethod(idispatch, "Save"); err != nil {
		return fmt.Errorf("Save .lnk: %w", err)
	}
	_ = syscall.Stat // keep imports used
	_ = unsafe.Sizeof(0)
	return nil
}

func installContextMenuPlatform(in ContextMenuInput) error {
	for _, base := range []string{
		`Software\Classes\Directory\shell\` + in.RegistryKeySuffix,
		`Software\Classes\Directory\Background\shell\` + in.RegistryKeySuffix,
	} {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, base, registry.ALL_ACCESS)
		if err != nil {
			return fmt.Errorf("create %s: %w", base, err)
		}
		if err := k.SetStringValue("", in.MenuLabel); err != nil {
			k.Close()
			return err
		}
		if in.IconPath != "" {
			_ = k.SetStringValue("Icon", in.IconPath)
		}
		k.Close()

		cmdKey := base + `\command`
		k2, _, err := registry.CreateKey(registry.CURRENT_USER, cmdKey, registry.ALL_ACCESS)
		if err != nil {
			return fmt.Errorf("create %s: %w", cmdKey, err)
		}
		// Quote handler exe + pass %V as the right-clicked folder path
		cmd := fmt.Sprintf(`"%s" "%%V"`, in.HandlerExe)
		if err := k2.SetStringValue("", cmd); err != nil {
			k2.Close()
			return err
		}
		k2.Close()
	}
	return nil
}

func uninstallAllPlatform(in ContextMenuInput, desktopName string) error {
	for _, base := range []string{
		`Software\Classes\Directory\shell\` + in.RegistryKeySuffix + `\command`,
		`Software\Classes\Directory\shell\` + in.RegistryKeySuffix,
		`Software\Classes\Directory\Background\shell\` + in.RegistryKeySuffix + `\command`,
		`Software\Classes\Directory\Background\shell\` + in.RegistryKeySuffix,
	} {
		_ = registry.DeleteKey(registry.CURRENT_USER, base)
	}
	if desktopName != "" {
		link := filepath.Join(os.Getenv("USERPROFILE"), "Desktop", desktopName+".lnk")
		_ = os.Remove(link)
	}
	return nil
}
```

- [ ] **Step 3: Add go-ole dep**

```bash
go get github.com/go-ole/go-ole@latest
go mod tidy
```

- [ ] **Step 4: Cross-build Windows OK**

Run: `GOOS=windows GOARCH=amd64 go build ./...`
Expected: no errors. (Windows-only tests skipped on Linux.)

- [ ] **Step 5: Commit**

```bash
git add internal/shortcut/ go.mod go.sum
git commit -m "feat(shortcut): Windows .lnk via COM + HKCU registry context menu"
```

---

## P8 internal/ui (web UI server) + cmd/onboarding-server

### Task P8.1: Orchestrator interface + in-memory implementation

**Files:**
- Create: `internal/ui/orchestrator.go`
- Create: `internal/ui/orchestrator_real.go`
- Test: `internal/ui/orchestrator_test.go`

- [ ] **Step 1: Write failing test for the interface contract**

`internal/ui/orchestrator_test.go`:
```go
package ui

import (
	"context"
	"testing"
)

// TestOrchestratorImplementsInterface ensures the production type satisfies
// the interface; if a method signature drifts later, this test will fail.
func TestOrchestratorImplementsInterface(t *testing.T) {
	var _ Orchestrator = (*realOrchestrator)(nil)
}

func TestNoopOrchestratorFinalize(t *testing.T) {
	o := &noopOrchestrator{}
	if err := o.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/ui/...`
Expected: FAIL — `undefined: Orchestrator`, `realOrchestrator`, `noopOrchestrator`.

- [ ] **Step 3: Write interface + a no-op stub + real-skeleton**

`internal/ui/orchestrator.go`:
```go
// Package ui exposes the onboarding web UI as an embedded SPA driven via
// HTTP JSON-RPC + Server-Sent Events.
package ui

import (
	"context"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
)

// Orchestrator is the side-effecting backend driven by the SPA.
// Each method is idempotent: calling twice after success is a no-op.
type Orchestrator interface {
	State(ctx context.Context) (SanitizedState, error)

	LoginModelserver(ctx context.Context) (oauth.DeviceCodeChallenge, error)
	PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error)

	LoginAgentserver(ctx context.Context) (oauth.DeviceCodeChallenge, error)
	PollAgentserverLogin(ctx context.Context) (agentserver.WorkspaceAPIKey, error)

	EnsureVSCode(ctx context.Context, progress chan<- ProgressEvent) error
	ConfigureVSCode(ctx context.Context) error

	Finalize(ctx context.Context) error
	Abort(ctx context.Context) error
}

// SanitizedState is the read view sent to the browser — never contains secrets.
type SanitizedState struct {
	SchemaVersion int      `json:"schema_version"`
	InstallID     string   `json:"install_id"`
	OnboardingStatus string `json:"onboarding_status"`
	CompletedSteps []string `json:"completed_steps"`
	LastError      string   `json:"last_error,omitempty"`
	ModelserverProjectID string `json:"modelserver_project_id,omitempty"`
	AgentserverWorkspaceID string `json:"agentserver_workspace_id,omitempty"`
	VSCodePath    string   `json:"vscode_path,omitempty"`
	VSCodeVersion string   `json:"vscode_version,omitempty"`
}

type ProgressEvent struct {
	Stage      string `json:"stage"`
	Downloaded int64  `json:"downloaded,omitempty"`
	Total      int64  `json:"total,omitempty"`
	SpeedBps   int64  `json:"speed_bps,omitempty"`
	Msg        string `json:"msg,omitempty"`
}

// noopOrchestrator is used in tests + smoke runs (no state mutation).
type noopOrchestrator struct{}

func (noopOrchestrator) State(context.Context) (SanitizedState, error) {
	return SanitizedState{}, nil
}
func (noopOrchestrator) LoginModelserver(context.Context) (oauth.DeviceCodeChallenge, error) {
	return oauth.DeviceCodeChallenge{UserCode: "TEST"}, nil
}
func (noopOrchestrator) PollModelserverLogin(context.Context) (modelserver.APIKey, error) {
	return modelserver.APIKey{}, nil
}
func (noopOrchestrator) LoginAgentserver(context.Context) (oauth.DeviceCodeChallenge, error) {
	return oauth.DeviceCodeChallenge{UserCode: "TEST"}, nil
}
func (noopOrchestrator) PollAgentserverLogin(context.Context) (agentserver.WorkspaceAPIKey, error) {
	return agentserver.WorkspaceAPIKey{}, nil
}
func (noopOrchestrator) EnsureVSCode(context.Context, chan<- ProgressEvent) error { return nil }
func (noopOrchestrator) ConfigureVSCode(context.Context) error                    { return nil }
func (noopOrchestrator) Finalize(context.Context) error                           { return nil }
func (noopOrchestrator) Abort(context.Context) error                              { return nil }
```

`internal/ui/orchestrator_real.go`:
```go
package ui

import (
	"context"
	"fmt"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

type Deps struct {
	State       *state.Store
	Secrets     secrets.Store
	MS          *modelserver.Client
	AS          *agentserver.Client
	MSOAuth     oauth.Config
	ASOAuth     oauth.Config
	CodexConfigPath string
	VSCodeUserDataDir string
	VSCodeExtDir      string
	EmbeddedVSIXPath  string
	CodexAbsPath      string
}

type realOrchestrator struct {
	d Deps
	// transient: in-flight device-code challenges per step
	msChallenge oauth.DeviceCodeChallenge
	asChallenge oauth.DeviceCodeChallenge
	msToken     oauth.Token
	asToken     oauth.Token
}

func NewRealOrchestrator(d Deps) Orchestrator {
	return &realOrchestrator{d: d}
}

func (r *realOrchestrator) State(ctx context.Context) (SanitizedState, error) {
	s, err := r.d.State.Load()
	if err != nil {
		return SanitizedState{}, err
	}
	return SanitizedState{
		SchemaVersion: s.SchemaVersion,
		InstallID:     s.InstallID,
		OnboardingStatus: string(s.Onboarding.Status),
		CompletedSteps:  append([]string(nil), s.Onboarding.CompletedSteps...),
		LastError:      s.Onboarding.LastError,
		ModelserverProjectID:   s.Modelserver.ProjectID,
		AgentserverWorkspaceID: s.Agentserver.WorkspaceID,
		VSCodePath:    s.VSCode.Path,
		VSCodeVersion: s.VSCode.Version,
	}, nil
}

func (r *realOrchestrator) LoginModelserver(ctx context.Context) (oauth.DeviceCodeChallenge, error) {
	ch, err := oauth.RequestDeviceCode(ctx, r.d.MSOAuth)
	if err != nil {
		return oauth.DeviceCodeChallenge{}, err
	}
	r.msChallenge = ch
	return ch, nil
}

func (r *realOrchestrator) PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error) {
	if r.msChallenge.DeviceCode == "" {
		return modelserver.APIKey{}, fmt.Errorf("no in-flight modelserver login")
	}
	tok, err := oauth.PollToken(ctx, r.d.MSOAuth, r.msChallenge)
	if err != nil {
		return modelserver.APIKey{}, err
	}
	r.msToken = tok
	proj, err := r.d.MS.PickOrCreateProject(ctx, tok.AccessToken, "default")
	if err != nil {
		return modelserver.APIKey{}, err
	}
	key, err := r.d.MS.CreateAPIKey(ctx, tok.AccessToken, proj.ID, "agentserver-vscode")
	if err != nil {
		return modelserver.APIKey{}, err
	}
	if err := r.d.Secrets.Set("modelserver_api_key", key.Secret); err != nil {
		return modelserver.APIKey{}, err
	}
	if err := r.d.State.Update(func(s *state.State) error {
		s.Modelserver.ProjectID = proj.ID
		s.Modelserver.APIKeySuffix = key.KeySuffix
		s.Onboarding.AddCompleted("modelserver_login")
		return nil
	}); err != nil {
		return modelserver.APIKey{}, err
	}
	return key, nil
}

func (r *realOrchestrator) LoginAgentserver(ctx context.Context) (oauth.DeviceCodeChallenge, error) {
	ch, err := oauth.RequestDeviceCode(ctx, r.d.ASOAuth)
	if err != nil {
		return oauth.DeviceCodeChallenge{}, err
	}
	r.asChallenge = ch
	return ch, nil
}

func (r *realOrchestrator) PollAgentserverLogin(ctx context.Context) (agentserver.WorkspaceAPIKey, error) {
	if r.asChallenge.DeviceCode == "" {
		return agentserver.WorkspaceAPIKey{}, fmt.Errorf("no in-flight agentserver login")
	}
	tok, err := oauth.PollToken(ctx, r.d.ASOAuth, r.asChallenge)
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	r.asToken = tok
	ws, err := r.d.AS.GetOrCreateDefaultWorkspace(ctx, tok.AccessToken, "default")
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	key, err := r.d.AS.CreateWorkspaceAPIKey(ctx, tok.AccessToken, ws.ID, "agentserver-vscode")
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	if err := r.d.Secrets.Set("agentserver_ws_api_key", key.Secret); err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	if err := r.d.State.Update(func(s *state.State) error {
		s.Agentserver.WorkspaceID = ws.ID
		s.Agentserver.WorkspaceAPIKeySuffix = key.KeySuffix
		s.Onboarding.AddCompleted("agentserver_login")
		return nil
	}); err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	return key, nil
}

// EnsureVSCode + ConfigureVSCode bodies appear in P9 (they call vscode/* and
// need cmd-side helpers like download cache paths). For P8 we keep stubs
// so the JSON-RPC server can wire up.
func (r *realOrchestrator) EnsureVSCode(ctx context.Context, ch chan<- ProgressEvent) error {
	return fmt.Errorf("EnsureVSCode: not wired yet (P9)")
}
func (r *realOrchestrator) ConfigureVSCode(ctx context.Context) error {
	return fmt.Errorf("ConfigureVSCode: not wired yet (P9)")
}
func (r *realOrchestrator) Finalize(ctx context.Context) error {
	return r.d.State.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		return nil
	})
}
func (r *realOrchestrator) Abort(ctx context.Context) error { return nil }
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/ui/...`

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): Orchestrator interface + real skeleton + noop"
```

### Task P8.2: HTTP server + JSON-RPC routes

**Files:**
- Create: `internal/ui/server.go`
- Test: `internal/ui/server_test.go`

- [ ] **Step 1: Write failing test**

`internal/ui/server_test.go`:
```go
package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerStateEndpoint(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}, nil))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	var s SanitizedState
	json.NewDecoder(resp.Body).Decode(&s)
}

func TestServerStepEndpoint(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}, nil))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/step/modelserver_login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["user_code"] != "TEST" {
		t.Errorf("got %+v", body)
	}
}

func TestServerStaticAsset(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}, nil))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/ui/...`
Expected: FAIL — `undefined: NewServer`.

- [ ] **Step 3: Implement server + minimal static assets**

`internal/ui/server.go`:
```go
package ui

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"sync"
	"time"
)

//go:embed assets/*
var assetsFS embed.FS

func NewServer(o Orchestrator, openBrowser func(url string)) http.Handler {
	s := &server{o: o, openBrowser: openBrowser, sse: newSSEHub()}
	mux := http.NewServeMux()
	// Static
	staticFS, _ := fs.Sub(assetsFS, "assets")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	// JSON
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/step/modelserver_login", s.handleMSLogin)
	mux.HandleFunc("/api/step/modelserver_login/status", s.handleMSStatus)
	mux.HandleFunc("/api/step/agentserver_login", s.handleASLogin)
	mux.HandleFunc("/api/step/agentserver_login/status", s.handleASStatus)
	mux.HandleFunc("/api/step/vscode_install", s.handleVSCodeInstall)
	mux.HandleFunc("/api/step/vscode_configure", s.handleVSCodeConfigure)
	mux.HandleFunc("/api/finalize", s.handleFinalize)
	mux.HandleFunc("/api/abort", s.handleAbort)

	// SSE
	mux.HandleFunc("/api/events", s.sse.handle)
	return mux
}

type server struct {
	o           Orchestrator
	openBrowser func(string)
	sse         *sseHub
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	st, err := s.o.State(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *server) handleMSLogin(w http.ResponseWriter, r *http.Request) {
	ch, err := s.o.LoginModelserver(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if s.openBrowser != nil && ch.VerificationURIComplete != "" {
		go s.openBrowser(ch.VerificationURIComplete)
	}
	writeJSON(w, 200, ch)
}

func (s *server) handleMSStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	key, err := s.o.PollModelserverLogin(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]string{"state": "waiting", "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"state": "success", "key_suffix": key.KeySuffix})
}

func (s *server) handleASLogin(w http.ResponseWriter, r *http.Request) {
	ch, err := s.o.LoginAgentserver(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if s.openBrowser != nil && ch.VerificationURIComplete != "" {
		go s.openBrowser(ch.VerificationURIComplete)
	}
	writeJSON(w, 200, ch)
}

func (s *server) handleASStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	key, err := s.o.PollAgentserverLogin(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]string{"state": "waiting", "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"state": "success", "key_suffix": key.KeySuffix})
}

func (s *server) handleVSCodeInstall(w http.ResponseWriter, r *http.Request) {
	streamID := s.sse.newStream()
	go func() {
		defer s.sse.close(streamID)
		ch := s.sse.channel(streamID)
		s.o.EnsureVSCode(context.Background(), ch)
	}()
	writeJSON(w, 200, map[string]string{"stream_id": streamID})
}

func (s *server) handleVSCodeConfigure(w http.ResponseWriter, r *http.Request) {
	if err := s.o.ConfigureVSCode(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "success"})
}

func (s *server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	if err := s.o.Finalize(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "complete"})
}

func (s *server) handleAbort(w http.ResponseWriter, r *http.Request) {
	_ = s.o.Abort(r.Context())
	writeJSON(w, 200, map[string]string{"state": "aborted"})
}

// ----------- SSE hub -----------

type sseHub struct {
	mu      sync.Mutex
	streams map[string]chan ProgressEvent
}

func newSSEHub() *sseHub {
	return &sseHub{streams: map[string]chan ProgressEvent{}}
}

func (h *sseHub) newStream() string {
	id := time.Now().Format("20060102-150405.000000000")
	h.mu.Lock()
	defer h.mu.Unlock()
	h.streams[id] = make(chan ProgressEvent, 128)
	return id
}

func (h *sseHub) channel(id string) chan<- ProgressEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.streams[id]
}

func (h *sseHub) close(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.streams[id]; ok {
		close(ch)
		delete(h.streams, id)
	}
}

func (h *sseHub) handle(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("stream")
	h.mu.Lock()
	ch, ok := h.streams[id]
	h.mu.Unlock()
	if !ok {
		http.Error(w, "no such stream", 404)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	for ev := range ch {
		w.Write([]byte("data: "))
		enc.Encode(ev)
		w.Write([]byte("\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
}
```

- [ ] **Step 4: Create minimal placeholder asset so embed compiles + 404 test passes**

`internal/ui/assets/index.html`:
```html
<!DOCTYPE html>
<html lang="zh-cn">
<head>
<meta charset="utf-8">
<title>agentserver-vscode 配置向导</title>
<link rel="stylesheet" href="/styles.css">
</head>
<body>
<div id="app">加载中…</div>
<script src="/app.js"></script>
</body>
</html>
```

`internal/ui/assets/styles.css`:
```css
body { font-family: system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; max-width: 720px; margin: 40px auto; padding: 0 20px; color: #222; }
.step { padding: 12px; border: 1px solid #ddd; margin: 8px 0; border-radius: 6px; }
.step.active { border-color: #0066cc; background: #f0f7ff; }
.step.done { color: #888; }
button { padding: 8px 16px; border: 0; background: #0066cc; color: white; border-radius: 4px; cursor: pointer; }
button:disabled { background: #aaa; }
.error { color: #b00; margin-top: 8px; }
progress { width: 100%; }
```

`internal/ui/assets/app.js`:
```javascript
// Minimal SPA: render 5 steps, drive each via /api/step/*.
const STEPS = [
  { id: 'modelserver_login',  label: '登录 modelserver',  type: 'oauth' },
  { id: 'agentserver_login',  label: '登录 agentserver',  type: 'oauth' },
  { id: 'vscode_install',     label: '安装 VS Code',       type: 'progress' },
  { id: 'vscode_configure',   label: '配置 VS Code 与 codex', type: 'action' },
  { id: 'finalize',           label: '完成配置(快捷方式 + 右键菜单)', type: 'action' },
];

async function fetchJSON(url, opts) {
  const r = await fetch(url, opts);
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}

let state = null;
async function refreshState() {
  state = await fetchJSON('/api/state');
  render();
}

function render() {
  const root = document.getElementById('app');
  root.innerHTML = '';
  const h = document.createElement('h1');
  h.textContent = 'agentserver-vscode 配置向导';
  root.appendChild(h);

  for (const s of STEPS) {
    const div = document.createElement('div');
    div.className = 'step';
    const done = state && state.completed_steps && state.completed_steps.includes(s.id);
    if (done) div.classList.add('done');
    div.innerHTML = `<b>${s.label}</b> ${done ? '✓' : ''}`;
    if (!done) {
      const btn = document.createElement('button');
      btn.textContent = '开始';
      btn.onclick = () => runStep(s);
      div.appendChild(document.createElement('br'));
      div.appendChild(btn);
    }
    root.appendChild(div);
  }
}

async function runStep(s) {
  try {
    if (s.id === 'finalize') {
      await fetchJSON('/api/finalize', { method: 'POST' });
    } else if (s.id === 'vscode_configure') {
      await fetchJSON('/api/step/vscode_configure', { method: 'POST' });
    } else if (s.id === 'vscode_install') {
      const r = await fetchJSON('/api/step/vscode_install', { method: 'POST' });
      await streamProgress(r.stream_id);
    } else if (s.id === 'modelserver_login' || s.id === 'agentserver_login') {
      const ch = await fetchJSON('/api/step/' + s.id, { method: 'POST' });
      alert('请在弹出的浏览器中完成登录。\n用户码: ' + ch.user_code);
      // Poll until success
      while (true) {
        const st = await fetchJSON('/api/step/' + s.id + '/status');
        if (st.state === 'success') break;
        await new Promise(r => setTimeout(r, 3000));
      }
    }
    await refreshState();
  } catch (e) {
    alert('失败: ' + e.message);
  }
}

function streamProgress(id) {
  return new Promise((resolve) => {
    const es = new EventSource('/api/events?stream=' + id);
    es.onmessage = e => {
      const ev = JSON.parse(e.data);
      console.log('progress', ev);
    };
    es.onerror = () => { es.close(); resolve(); };
  });
}

refreshState();
```

- [ ] **Step 5: Run, expect PASS**

Run: `go test -race ./internal/ui/...`

- [ ] **Step 6: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): HTTP server + JSON-RPC + SSE + minimal SPA"
```

### Task P8.3: cmd/onboarding-server

**Files:**
- Modify: `cmd/onboarding-server/main.go`

- [ ] **Step 1: Wire up server using a noop orchestrator (real deps come in P9)**

`cmd/onboarding-server/main.go`:
```go
// onboarding-server can be run standalone for UI debugging. The launcher
// embeds the same server with real deps in P9.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/agentserver/agentserver-pkg/internal/ui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "bind address; 0 = random port")
	flag.Parse()

	handler := ui.NewServer(ui.NewNoopOrchestrator(), nil)
	ln, err := newListener(*addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}
	fmt.Printf("onboarding-server: http://%s/\n", ln.Addr())
	log.Fatal(http.Serve(ln, handler))
}
```

`cmd/onboarding-server/listener.go`:
```go
package main

import "net"

func newListener(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
```

Add to `internal/ui/orchestrator.go`:
```go
// NewNoopOrchestrator returns an Orchestrator that does nothing — useful for
// UI debugging or smoke tests.
func NewNoopOrchestrator() Orchestrator { return noopOrchestrator{} }
```

- [ ] **Step 2: Build + smoke-test**

```bash
go build ./cmd/onboarding-server
./onboarding-server -addr 127.0.0.1:9999 &
PID=$!
sleep 1
curl -s http://127.0.0.1:9999/api/state | head -c 200
kill $PID
```

Expected: JSON response with sanitized state structure.

- [ ] **Step 3: Commit**

```bash
git add cmd/onboarding-server/ internal/ui/
git commit -m "feat(onboarding-server): standalone runner of the embedded web UI"
```

---

## P9 cmd/launcher + cmd/open-folder + cmd/agentctl + wire real orchestrator

### Task P9.1: paths helper + browser opener

**Files:**
- Create: `internal/paths/paths.go`
- Create: `internal/paths/paths_test.go`
- Create: `internal/browser/open.go`
- Create: `internal/browser/open_windows.go`
- Create: `internal/browser/open_other.go`

- [ ] **Step 1: Write failing test for paths**

`internal/paths/paths_test.go`:
```go
package paths

import "testing"

func TestPathsConsistent(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.UserHome == "" {
		t.Errorf("UserHome empty")
	}
	if p.StateFile == "" || p.SecretsFile == "" {
		t.Errorf("missing state/secrets path")
	}
	if p.CacheDir == "" {
		t.Errorf("missing cache dir")
	}
}
```

- [ ] **Step 2: Implement**

`internal/paths/paths.go`:
```go
// Package paths centralizes all on-disk locations.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Paths struct {
	UserHome string

	// Per-install state root (~/.agentserver-vscode/)
	InstallRoot string
	StateFile    string
	SecretsFile  string
	CacheDir     string
	VSCodeUserDataDir string
	VSCodeExtDir      string

	// Codex config
	CodexDir        string
	CodexConfigFile string

	// LocalAppData root (Windows) for binaries
	LocalAppDataRoot string
	CodexExePath     string
}

func Default() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("user home: %w", err)
	}
	root := filepath.Join(home, ".agentserver-vscode")
	codex := filepath.Join(home, ".codex")
	p := Paths{
		UserHome:          home,
		InstallRoot:       root,
		StateFile:         filepath.Join(root, "state.json"),
		SecretsFile:       filepath.Join(root, "secrets.json"),
		CacheDir:          filepath.Join(root, "cache"),
		VSCodeUserDataDir: filepath.Join(root, "vscode-data"),
		VSCodeExtDir:      filepath.Join(root, "vscode-extensions"),
		CodexDir:          codex,
		CodexConfigFile:   filepath.Join(codex, "config.toml"),
	}
	switch runtime.GOOS {
	case "windows":
		lad := os.Getenv("LOCALAPPDATA")
		if lad == "" {
			lad = filepath.Join(home, "AppData", "Local")
		}
		p.LocalAppDataRoot = filepath.Join(lad, "agentserver-vscode")
		p.CodexExePath = filepath.Join(p.LocalAppDataRoot, "bin", "codex.exe")
	default:
		p.LocalAppDataRoot = filepath.Join(root, "bin-root")
		p.CodexExePath = filepath.Join(p.LocalAppDataRoot, "bin", "codex")
	}
	return p, nil
}
```

`internal/browser/open.go`:
```go
// Package browser opens URLs in the user's default browser.
package browser

func Open(url string) error { return openPlatform(url) }
```

`internal/browser/open_windows.go`:
```go
//go:build windows

package browser

import "os/exec"

func openPlatform(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
```

`internal/browser/open_other.go`:
```go
//go:build !windows

package browser

import "os/exec"

func openPlatform(url string) error {
	// Best-effort on dev hosts; Linux has xdg-open, macOS has open.
	for _, prog := range []string{"xdg-open", "open"} {
		if err := exec.Command(prog, url).Start(); err == nil {
			return nil
		}
	}
	return nil
}
```

- [ ] **Step 3: Run paths test, expect PASS**

Run: `go test -race ./internal/paths/...`

- [ ] **Step 4: Commit**

```bash
git add internal/paths/ internal/browser/
git commit -m "feat: paths + cross-platform browser opener"
```

### Task P9.2: Wire EnsureVSCode + ConfigureVSCode in realOrchestrator

**Files:**
- Modify: `internal/ui/orchestrator_real.go`
- Test: `internal/ui/orchestrator_real_test.go`

- [ ] **Step 1: Write failing test using fake `code` and a HTTP test download**

`internal/ui/orchestrator_real_test.go`:
```go
package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/state"
)

func TestConfigureVSCodeWritesSettings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	// fake code that just records args
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte("#!/bin/bash\nexit 0\n"), 0o755)

	store := state.NewStore(filepath.Join(dir, "state.json"))
	store.Update(func(s *state.State) error {
		s.VSCode.Path = codeExe
		s.VSCode.UserDataDir = filepath.Join(dir, "data")
		s.VSCode.ExtensionsDir = filepath.Join(dir, "ext")
		return nil
	})
	// embedded vsix stub file
	vsix := filepath.Join(dir, "stub.vsix")
	os.WriteFile(vsix, []byte("PK\x03\x04stub"), 0o644)

	r := &realOrchestrator{d: Deps{
		State: store,
		CodexAbsPath: filepath.Join(dir, "bin", "codex"),
		VSCodeUserDataDir: filepath.Join(dir, "data"),
		VSCodeExtDir: filepath.Join(dir, "ext"),
		EmbeddedVSIXPath: vsix,
	}}
	if err := r.ConfigureVSCode(context.Background()); err != nil {
		t.Fatalf("configure: %v", err)
	}
	settings := filepath.Join(dir, "data", "User", "settings.json")
	if _, err := os.Stat(settings); err != nil {
		t.Errorf("settings not written: %v", err)
	}
}

// EnsureVSCode unit test is light because the real path needs Windows;
// here we just exercise the early-return when VS Code is already installed.
func TestEnsureVSCode_AlreadyInstalled(t *testing.T) {
	dir := t.TempDir()
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte("#!/bin/bash\necho 1.96.0\n"), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	store := state.NewStore(filepath.Join(dir, "state.json"))
	r := &realOrchestrator{d: Deps{State: store}}
	if err := r.EnsureVSCode(context.Background(), nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("vscode_installed") {
		t.Errorf("step not marked complete")
	}
}

// Used by the SSE handler indirectly; keep imports referenced.
var _ = httptest.NewServer
var _ = http.StatusOK
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/ui/...`
Expected: FAIL — EnsureVSCode/ConfigureVSCode still return stub errors.

- [ ] **Step 3: Implement real bodies**

Replace the stub `EnsureVSCode` / `ConfigureVSCode` in
`internal/ui/orchestrator_real.go` with:
```go
import (
	// (keep prior imports)
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/download"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/vscode"
)

func (r *realOrchestrator) EnsureVSCode(ctx context.Context, ch chan<- ProgressEvent) error {
	det, _ := vscode.Detect()
	if det.Installed {
		if err := r.d.State.Update(func(s *state.State) error {
			s.VSCode.Path = det.Path
			s.VSCode.Version = det.Version
			s.VSCode.InstalledByUs = false
			s.Onboarding.AddCompleted("vscode_installed")
			return nil
		}); err != nil {
			return err
		}
		return nil
	}
	plan := vscode.PlanInstall()
	cache := filepath.Join(r.d.VSCodeUserDataDir, "..", "cache",
		"vscode-"+vscode.LockedVersion+plan.FileExt)
	if err := download.DownloadResumable(ctx, plan.URL, cache, plan.SHA256,
		downloadAdapter(ch)); err != nil {
		return fmt.Errorf("download VS Code: %w", err)
	}
	if err := vscode.SilentInstall(ctx, cache, plan); err != nil {
		return fmt.Errorf("install VS Code: %w", err)
	}
	det2, err := vscode.Detect()
	if err != nil {
		return fmt.Errorf("post-install detect: %w", err)
	}
	return r.d.State.Update(func(s *state.State) error {
		s.VSCode.Path = det2.Path
		s.VSCode.Version = det2.Version
		s.VSCode.InstalledByUs = true
		s.Onboarding.AddCompleted("vscode_installed")
		return nil
	})
}

func downloadAdapter(ui chan<- ProgressEvent) chan<- download.ProgressEvent {
	if ui == nil {
		return nil
	}
	out := make(chan download.ProgressEvent, 16)
	go func() {
		for ev := range out {
			ui <- ProgressEvent{
				Stage: ev.Stage, Downloaded: ev.Downloaded, Total: ev.Total,
				SpeedBps: ev.SpeedBps, Msg: ev.Msg,
			}
		}
	}()
	return out
}

func (r *realOrchestrator) ConfigureVSCode(ctx context.Context) error {
	s, err := r.d.State.Load()
	if err != nil {
		return err
	}
	if s.VSCode.Path == "" {
		return fmt.Errorf("ConfigureVSCode: vscode.Path unknown — run EnsureVSCode first")
	}
	// Write settings.json
	settingsPath := filepath.Join(r.d.VSCodeUserDataDir, "User", "settings.json")
	if err := vscode.WriteSettings(settingsPath, vscode.SettingsInput{
		CodexAbsPath: r.d.CodexAbsPath,
	}); err != nil {
		return err
	}
	// Write/merge ~/.codex/config.toml
	if err := codex.UpdateConfig(r.d.CodexConfigPath, codex.Settings{
		Provider: "modelserver", Model: "gpt-5.5",
		BaseURL: "https://code.ai.cs.ac.cn/v1",
		EnvKey:  "OPENAI_API_KEY", WireAPI: "responses",
	}); err != nil {
		return err
	}
	// Setx OPENAI_API_KEY (no-op on non-Windows)
	apiKey, err := r.d.Secrets.Get("modelserver_api_key")
	if err == nil {
		_ = env.PersistUserEnv("OPENAI_API_KEY", apiKey)
	}
	// Install zh-hans language pack + our embedded .vsix
	if err := vscode.InstallExtensions(ctx, vscode.Installer{
		CodeExe:       s.VSCode.Path,
		UserDataDir:   r.d.VSCodeUserDataDir,
		ExtensionsDir: r.d.VSCodeExtDir,
		Extensions: []string{
			"MS-CEINTL.vscode-language-pack-zh-hans",
			r.d.EmbeddedVSIXPath,
		},
	}); err != nil {
		return err
	}
	return r.d.State.Update(func(s *state.State) error {
		s.Onboarding.AddCompleted("vscode_configured")
		return nil
	})
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./internal/ui/...`

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): wire real EnsureVSCode/ConfigureVSCode with download + install"
```

### Task P9.3: realOrchestrator.Finalize — shortcuts + state

**Files:**
- Modify: `internal/ui/orchestrator_real.go`
- Add: `Deps.LauncherExePath`, `Deps.OpenFolderExePath`, `Deps.IconPath`

- [ ] **Step 1: Update Deps + Finalize impl**

Edit `internal/ui/orchestrator_real.go`:
```go
type Deps struct {
	State           *state.Store
	Secrets         secrets.Store
	MS              *modelserver.Client
	AS              *agentserver.Client
	MSOAuth         oauth.Config
	ASOAuth         oauth.Config
	CodexConfigPath string
	VSCodeUserDataDir string
	VSCodeExtDir      string
	EmbeddedVSIXPath  string
	CodexAbsPath      string

	// Used by Finalize
	LauncherExePath   string
	OpenFolderExePath string
	IconPath          string
}

func (r *realOrchestrator) Finalize(ctx context.Context) error {
	if r.d.LauncherExePath != "" {
		if err := shortcut.EnsureDesktopShortcut(shortcut.DesktopInput{
			Name:      "agentserver-vscode",
			TargetExe: r.d.LauncherExePath,
			IconPath:  r.d.IconPath,
		}); err != nil {
			return err
		}
		if err := r.d.State.Update(func(s *state.State) error {
			s.Shortcuts.DesktopCreated = true
			return nil
		}); err != nil {
			return err
		}
	}
	if r.d.OpenFolderExePath != "" {
		if err := shortcut.InstallContextMenu(shortcut.ContextMenuInput{
			MenuLabel:         "用 agentserver-vscode 打开",
			HandlerExe:        r.d.OpenFolderExePath,
			IconPath:          r.d.IconPath,
			RegistryKeySuffix: "AgentserverVscode",
		}); err != nil {
			return err
		}
		if err := r.d.State.Update(func(s *state.State) error {
			s.Shortcuts.ContextMenuInstalled = true
			return nil
		}); err != nil {
			return err
		}
	}
	return r.d.State.Update(func(s *state.State) error {
		s.Onboarding.AddCompleted("shortcuts_created")
		s.Onboarding.Status = state.StatusComplete
		return nil
	})
}
```

Add the import `"github.com/agentserver/agentserver-pkg/internal/shortcut"`.

- [ ] **Step 2: Run all tests**

Run: `go test -race ./...`
Expected: PASS — existing tests still green.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): wire Finalize → shortcut + context menu"
```

### Task P9.4: cmd/launcher (decision tree)

**Files:**
- Modify: `cmd/launcher/main.go`

- [ ] **Step 1: Replace stub with real launcher**

`cmd/launcher/main.go`:
```go
// launcher is the user-facing entrypoint (desktop shortcut). It either:
//   - if first run: spawn onboarding-server + open browser
//   - else: exec VS Code with our user-data-dir
//
// Folder argument (right-click handler) is delegated to cmd/open-folder.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/browser"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/ui"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("launcher: %v", err)
	}
}

func run() error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.InstallRoot, 0o755); err != nil {
		return err
	}
	store := state.NewStore(p.StateFile)
	s, err := store.Load()
	if err != nil {
		return err
	}

	if s.Onboarding.Status == state.StatusComplete && s.VSCode.Path != "" {
		// Just exec VS Code with our user-data-dir (empty workspace).
		return execVSCode(s.VSCode.Path, p, "")
	}

	// Otherwise: serve onboarding UI.
	return serveOnboarding(p, store)
}

func serveOnboarding(p paths.Paths, store *state.Store) error {
	sec := secrets.New(p.SecretsFile)

	msOAuth := oauth.Config{
		Endpoint: "https://code.cs.ac.cn",
		AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
		ClientID: "agentserver-vscode",
		Scope:    "openid profile offline_access",
	}
	asOAuth := oauth.Config{
		Endpoint: "https://agent.cs.ac.cn",
		AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
		ClientID: "agentserver-vscode",
		Scope:    "openid profile agent:register",
	}

	installDir, err := os.Executable()
	if err != nil {
		return err
	}
	installDir = osDir(installDir)

	deps := ui.Deps{
		State:           store,
		Secrets:         sec,
		MS:              modelserver.New("https://code.cs.ac.cn"),
		AS:              agentserver.New("https://agent.cs.ac.cn"),
		MSOAuth:         msOAuth,
		ASOAuth:         asOAuth,
		CodexConfigPath: p.CodexConfigFile,
		VSCodeUserDataDir: p.VSCodeUserDataDir,
		VSCodeExtDir:      p.VSCodeExtDir,
		EmbeddedVSIXPath:  joinExe(installDir, "agentserver-vscode.vsix"),
		CodexAbsPath:      p.CodexExePath,
		LauncherExePath:   joinExe(installDir, "launcher.exe"),
		OpenFolderExePath: joinExe(installDir, "open-folder.exe"),
		IconPath:          joinExe(installDir, "icon.ico"),
	}

	orch := ui.NewRealOrchestrator(deps)
	handler := ui.NewServer(orch, browser.Open)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/", ln.Addr())
	fmt.Println("onboarding URL:", url)
	go func() { _ = browser.Open(url) }()
	return http.Serve(ln, handler)
}

func execVSCode(codeExe string, p paths.Paths, folder string) error {
	args := []string{
		"--user-data-dir", p.VSCodeUserDataDir,
		"--extensions-dir", p.VSCodeExtDir,
	}
	if folder != "" {
		args = append(args, folder)
	}
	cmd := exec.Command(codeExe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

// osDir returns the directory of an executable path.
func osDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}

func joinExe(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + string(os.PathSeparator) + name
}

// keep context import live for future use
var _ = context.Background
```

- [ ] **Step 2: Build**

Run: `make build`
Expected: success.

Run: `GOOS=windows GOARCH=amd64 go build ./cmd/launcher`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add cmd/launcher/
git commit -m "feat(launcher): wire decision tree (onboarding vs exec VS Code)"
```

### Task P9.5: cmd/open-folder

**Files:**
- Modify: `cmd/open-folder/main.go`

- [ ] **Step 1: Implement**

`cmd/open-folder/main.go`:
```go
// open-folder is invoked by the Explorer context menu with one argv: the
// folder path. It just execs VS Code with our user-data-dir + that folder.
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("usage: open-folder <path>")
	}
	folder := os.Args[1]

	p, err := paths.Default()
	if err != nil {
		log.Fatal(err)
	}
	s, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		log.Fatal(err)
	}
	if s.VSCode.Path == "" {
		log.Fatalf("VS Code path unknown — has onboarding run?")
	}

	cmd := exec.Command(s.VSCode.Path,
		"--user-data-dir", p.VSCodeUserDataDir,
		"--extensions-dir", p.VSCodeExtDir,
		folder,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("opened %s\n", folder)
}
```

- [ ] **Step 2: Build OK**

Run: `make build && make cross-windows`

- [ ] **Step 3: Commit**

```bash
git add cmd/open-folder/
git commit -m "feat(open-folder): exec VS Code with right-clicked folder"
```

### Task P9.6: cmd/agentctl (doctor, reconfigure, uninstall, logs)

**Files:**
- Modify: `cmd/agentctl/main.go`
- Create: `cmd/agentctl/cmd_doctor.go`
- Create: `cmd/agentctl/cmd_uninstall.go`
- Create: `cmd/agentctl/cmd_reconfigure.go`
- Create: `cmd/agentctl/cmd_logs.go`
- Test: `cmd/agentctl/doctor_test.go`

- [ ] **Step 1: Write failing test for doctor format**

`cmd/agentctl/doctor_test.go`:
```go
package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/state"
)

func TestRenderDoctor(t *testing.T) {
	s := &state.State{
		SchemaVersion: 1,
		Onboarding: state.OnboardingState{
			Status: state.StatusComplete,
			CompletedSteps: []string{"modelserver_login", "agentserver_login",
				"vscode_installed", "vscode_configured", "shortcuts_created"},
		},
		Modelserver: state.ModelserverState{ProjectID: "p1", APIKeySuffix: "wxyz"},
		Agentserver: state.AgentserverState{WorkspaceID: "ws-1"},
		VSCode: state.VSCodeState{Path: `C:\Code.exe`, Version: "1.96.0"},
	}
	var buf bytes.Buffer
	renderDoctor(&buf, s)
	out := buf.String()
	for _, want := range []string{
		"onboarding: complete",
		"modelserver: project=p1",
		"vscode: 1.96.0",
		"steps: 5/5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./cmd/agentctl/...`
Expected: FAIL.

- [ ] **Step 3: Implement**

`cmd/agentctl/main.go`:
```go
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "doctor":
		runDoctor()
	case "uninstall":
		runUninstall(os.Args[2:])
	case "reconfigure":
		runReconfigure()
	case "logs":
		runLogs()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `agentctl - maintenance CLI for agentserver-vscode

USAGE:
  agentctl doctor                 print install health
  agentctl reconfigure            relaunch the onboarding UI
  agentctl uninstall [--silent] [--vscode]
                                  remove shortcuts/registry/state; --vscode also removes VS Code
  agentctl logs                   print last 200 lines of launcher log`)
}
```

`cmd/agentctl/cmd_doctor.go`:
```go
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func runDoctor() {
	p, err := paths.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, "paths:", err)
		os.Exit(1)
	}
	s, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load state:", err)
		os.Exit(1)
	}
	renderDoctor(os.Stdout, s)
}

func renderDoctor(w io.Writer, s *state.State) {
	fmt.Fprintf(w, "agentserver-vscode doctor\n")
	fmt.Fprintf(w, "  schema_version: %d\n", s.SchemaVersion)
	fmt.Fprintf(w, "  install_id: %s\n", s.InstallID)
	fmt.Fprintf(w, "  onboarding: %s\n", s.Onboarding.Status)
	fmt.Fprintf(w, "  steps: %d/5 %v\n", len(s.Onboarding.CompletedSteps), s.Onboarding.CompletedSteps)
	fmt.Fprintf(w, "  modelserver: project=%s key=…%s\n",
		s.Modelserver.ProjectID, s.Modelserver.APIKeySuffix)
	fmt.Fprintf(w, "  agentserver: workspace=%s key=…%s\n",
		s.Agentserver.WorkspaceID, s.Agentserver.WorkspaceAPIKeySuffix)
	fmt.Fprintf(w, "  vscode: %s @ %s\n", s.VSCode.Version, s.VSCode.Path)
	if s.Onboarding.LastError != "" {
		fmt.Fprintf(w, "  last_error: %s\n", s.Onboarding.LastError)
	}
}
```

`cmd/agentctl/cmd_uninstall.go`:
```go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/shortcut"
)

func runUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	silent := fs.Bool("silent", false, "no prompts")
	removeVSCode := fs.Bool("vscode", false, "also uninstall VS Code")
	_ = fs.Parse(args)

	p, _ := paths.Default()
	if !*silent {
		fmt.Println("This will remove agentserver-vscode shortcuts, context menu, state, and secrets.")
		fmt.Print("Proceed? [y/N] ")
		var ans string
		fmt.Scanln(&ans)
		if ans != "y" && ans != "Y" {
			fmt.Println("aborted")
			return
		}
	}

	_ = shortcut.UninstallAll(shortcut.ContextMenuInput{RegistryKeySuffix: "AgentserverVscode"},
		"agentserver-vscode")
	sec := secrets.New(p.SecretsFile)
	_ = sec.Delete("modelserver_api_key")
	_ = sec.Delete("agentserver_ws_api_key")
	_ = os.RemoveAll(p.InstallRoot)
	_ = os.RemoveAll(p.LocalAppDataRoot)

	if *removeVSCode {
		fmt.Println("--vscode removal not implemented in v1; please remove manually via Apps & Features.")
	}
	fmt.Println("done.")
}
```

`cmd/agentctl/cmd_reconfigure.go`:
```go
package main

import (
	"fmt"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func runReconfigure() {
	p, _ := paths.Default()
	store := state.NewStore(p.StateFile)
	_ = store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusPending
		s.Onboarding.CompletedSteps = nil
		s.Onboarding.LastError = ""
		return nil
	})
	fmt.Println("state reset; relaunch launcher to start the onboarding UI again.")
}
```

`cmd/agentctl/cmd_logs.go`:
```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/paths"
)

func runLogs() {
	p, _ := paths.Default()
	logPath := filepath.Join(p.InstallRoot, "launcher.log")
	b, err := os.ReadFile(logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "no log:", err)
		return
	}
	fmt.Print(string(b))
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test -race ./cmd/agentctl/...`
Expected: PASS — TestRenderDoctor.

- [ ] **Step 5: Build**

Run: `make build && make cross-windows`

- [ ] **Step 6: Commit**

```bash
git add cmd/agentctl/
git commit -m "feat(agentctl): doctor / reconfigure / uninstall / logs subcommands"
```

---

## P10 VS Code extension `agentserver-vscode`

### Task P10.1: scaffold npm project + package.json

**Files:**
- Create: `extensions/agentserver-vscode/package.json`
- Create: `extensions/agentserver-vscode/tsconfig.json`
- Create: `extensions/agentserver-vscode/.vscodeignore`
- Create: `extensions/agentserver-vscode/.gitignore`
- Create: `extensions/agentserver-vscode/README.md`

- [ ] **Step 1: Write package.json**

`extensions/agentserver-vscode/package.json`:
```json
{
  "name": "agentserver-vscode",
  "displayName": "agentserver-vscode",
  "description": "agentserver-vscode integration: codex terminal + folder picker + panel pruning",
  "publisher": "agentserver",
  "version": "0.1.0",
  "engines": { "vscode": "^1.85.0" },
  "main": "./out/extension.js",
  "activationEvents": ["onStartupFinished"],
  "scripts": {
    "compile": "tsc -p ./",
    "watch":   "tsc -watch -p ./",
    "package": "vsce package --out agentserver-vscode-$npm_package_version.vsix --no-dependencies",
    "test":    "node ./out/test/runTest.js",
    "pretest": "npm run compile"
  },
  "devDependencies": {
    "@types/node":     "^20.0.0",
    "@types/vscode":   "^1.85.0",
    "@types/mocha":    "^10.0.6",
    "@vscode/test-electron": "^2.3.9",
    "@vscode/vsce":    "^2.24.0",
    "mocha":           "^10.2.0",
    "typescript":      "^5.3.3"
  },
  "contributes": {
    "configuration": {
      "title": "agentserver-vscode",
      "properties": {
        "agentserverVscode.startup.openFolderIfEmpty": {
          "type": "boolean", "default": true,
          "description": "If true, prompt to pick a folder when starting without a workspace."
        },
        "agentserverVscode.terminal.respawnOnClose": {
          "type": "boolean", "default": true,
          "description": "If true, reopen the codex terminal when closed."
        },
        "agentserverVscode.terminal.profileName": {
          "type": "string", "default": "codex",
          "description": "Name of the terminal profile to use."
        },
        "agentserverVscode.panel.hideViews": {
          "type": "array",
          "default": [
            "workbench.panel.repl",
            "workbench.debug.console",
            "workbench.panel.comments",
            "ports",
            "workbench.panel.testResults"
          ],
          "description": "Panel view IDs to keep focus away from."
        }
      }
    },
    "commands": [
      { "command": "agentserverVscode.doctor", "title": "agentserver-vscode: 诊断" },
      { "command": "agentserverVscode.reopenCodexTerminal", "title": "agentserver-vscode: 重开 codex 终端" }
    ]
  }
}
```

- [ ] **Step 2: Write tsconfig.json**

```json
{
  "compilerOptions": {
    "module": "commonjs",
    "target": "ES2021",
    "outDir": "out",
    "lib": ["ES2021"],
    "sourceMap": true,
    "rootDir": "src",
    "strict": true,
    "esModuleInterop": true
  },
  "include": ["src/**/*"]
}
```

- [ ] **Step 3: Write .vscodeignore + .gitignore**

`.vscodeignore`:
```
.vscode/**
**/*.ts
**/*.map
src/**
tsconfig.json
.gitignore
```

`.gitignore`:
```
node_modules/
out/
*.vsix
```

- [ ] **Step 4: Write README.md**

`extensions/agentserver-vscode/README.md`:
```markdown
# agentserver-vscode (extension)

VS Code extension that ships with the `agentserver-vscode` installer.

Responsibilities:
- Prompt to open a folder when none is open
- Ensure a `codex` terminal exists; reopen it if closed
- Keep focus on Terminal / Output (away from other panel views)

Not a standalone extension; expects settings injected by the installer.
```

- [ ] **Step 5: Commit**

```bash
git add extensions/agentserver-vscode/
git commit -m "feat(ext): scaffold npm project + package.json + tsconfig"
```

### Task P10.2: src/extension.ts (activate + commands)

**Files:**
- Create: `extensions/agentserver-vscode/src/extension.ts`
- Create: `extensions/agentserver-vscode/src/config.ts`
- Create: `extensions/agentserver-vscode/src/panel.ts`
- Create: `extensions/agentserver-vscode/src/terminal.ts`
- Create: `extensions/agentserver-vscode/src/folderPicker.ts`

- [ ] **Step 1: Write config.ts**

`src/config.ts`:
```ts
import * as vscode from 'vscode';

export interface ExtConfig {
  startupOpenFolderIfEmpty: boolean;
  terminalRespawnOnClose: boolean;
  terminalProfileName: string;
  panelHideViews: string[];
}

export function readConfig(): ExtConfig {
  const c = vscode.workspace.getConfiguration('agentserverVscode');
  return {
    startupOpenFolderIfEmpty: c.get<boolean>('startup.openFolderIfEmpty', true),
    terminalRespawnOnClose:   c.get<boolean>('terminal.respawnOnClose', true),
    terminalProfileName:      c.get<string>('terminal.profileName', 'codex'),
    panelHideViews:           c.get<string[]>('panel.hideViews', []),
  };
}
```

- [ ] **Step 2: Write folderPicker.ts**

`src/folderPicker.ts`:
```ts
import * as vscode from 'vscode';

/**
 * If no workspace is open, prompt for one and call vscode.openFolder.
 * Returns true if a folder open was triggered (extension host will reload).
 */
export async function maybePromptOpenFolder(): Promise<boolean> {
  if (vscode.workspace.workspaceFolders?.length) return false;
  const picked = await vscode.window.showOpenDialog({
    canSelectFolders: true,
    canSelectMany: false,
    openLabel: '打开',
    title: '选择要打开的项目文件夹',
  });
  if (!picked || picked.length === 0) return false;
  await vscode.commands.executeCommand('vscode.openFolder', picked[0], false);
  return true;
}
```

- [ ] **Step 3: Write terminal.ts**

`src/terminal.ts`:
```ts
import * as vscode from 'vscode';

let lastSpawn = 0;
const DEBOUNCE_MS = 200;

export async function openCodexTerminal(profileName: string): Promise<void> {
  const term = vscode.window.createTerminal({ name: profileName });
  term.show(false);
  lastSpawn = Date.now();
}

export function attachTerminalRespawn(
  ctx: vscode.ExtensionContext,
  profileName: string,
  enabled: () => boolean,
): void {
  ctx.subscriptions.push(
    vscode.window.onDidCloseTerminal(async (t) => {
      if (!enabled()) return;
      if (t.name !== profileName) return;
      if (Date.now() - lastSpawn < DEBOUNCE_MS) return; // avoid runaway
      // If the window itself is closing, do nothing.
      if (!vscode.window.state.focused) return;
      await openCodexTerminal(profileName);
    }),
  );
}
```

- [ ] **Step 4: Write panel.ts**

`src/panel.ts`:
```ts
import * as vscode from 'vscode';

const TERMINAL_FOCUS_CMD = 'workbench.action.terminal.focus';

/**
 * Tier (b) fallback: whenever the user switches to one of the "hidden"
 * panel views, immediately switch focus back to the terminal.
 * (VS Code lacks an official API to truly remove built-in views.)
 */
export function lockPanelToTerminal(
  ctx: vscode.ExtensionContext,
  hideViewIds: string[],
): void {
  if (hideViewIds.length === 0) return;
  const set = new Set(hideViewIds);

  // We can't subscribe to "active panel view changed" — VS Code doesn't
  // expose that event. Instead poll the activeTextEditor + activePanel
  // commands periodically, OR rely on user invoking commands. As a v1
  // pragmatic approach: re-focus terminal when configuration says so.
  // The user can also manually run "agentserver-vscode: 重开 codex 终端".
  ctx.subscriptions.push(
    vscode.commands.registerCommand('agentserverVscode.focusTerminal', async () => {
      await vscode.commands.executeCommand(TERMINAL_FOCUS_CMD);
    }),
  );

  // Tier (a): try setContext for known view IDs (best-effort).
  for (const id of set) {
    void vscode.commands.executeCommand('setContext', `${id}.visible`, false);
  }
}
```

- [ ] **Step 5: Write extension.ts**

`src/extension.ts`:
```ts
import * as vscode from 'vscode';
import { readConfig } from './config';
import { maybePromptOpenFolder } from './folderPicker';
import { attachTerminalRespawn, openCodexTerminal } from './terminal';
import { lockPanelToTerminal } from './panel';

export async function activate(ctx: vscode.ExtensionContext): Promise<void> {
  const cfg = readConfig();

  // 1. If no folder, prompt and bail (extension host will reload)
  if (cfg.startupOpenFolderIfEmpty) {
    const opened = await maybePromptOpenFolder();
    if (opened) return;
  }

  // 2. Panel lockdown
  lockPanelToTerminal(ctx, cfg.panelHideViews);

  // 3. Ensure a codex terminal exists
  if (vscode.window.terminals.length === 0) {
    await openCodexTerminal(cfg.terminalProfileName);
  }

  // 4. Respawn on close
  attachTerminalRespawn(ctx, cfg.terminalProfileName,
    () => readConfig().terminalRespawnOnClose);

  // 5. Commands
  ctx.subscriptions.push(
    vscode.commands.registerCommand('agentserverVscode.reopenCodexTerminal',
      () => openCodexTerminal(readConfig().terminalProfileName)),
    vscode.commands.registerCommand('agentserverVscode.doctor', async () => {
      const info = {
        terminals: vscode.window.terminals.map(t => t.name),
        workspace: vscode.workspace.workspaceFolders?.map(f => f.uri.fsPath),
        language:  vscode.env.language,
      };
      const channel = vscode.window.createOutputChannel('agentserver-vscode');
      channel.appendLine(JSON.stringify(info, null, 2));
      channel.show();
    }),
  );
}

export function deactivate(): void {}
```

- [ ] **Step 6: Install deps + build**

```bash
cd extensions/agentserver-vscode
npm install
npm run compile
```

Expected: `out/extension.js` exists, no TS errors.

- [ ] **Step 7: Commit**

```bash
git add extensions/agentserver-vscode/
git commit -m "feat(ext): extension entrypoint + folder picker + terminal respawn + panel guard"
```

### Task P10.3: extension tests with @vscode/test-electron

**Files:**
- Create: `extensions/agentserver-vscode/src/test/runTest.ts`
- Create: `extensions/agentserver-vscode/src/test/suite/index.ts`
- Create: `extensions/agentserver-vscode/src/test/suite/startup.test.ts`
- Create: `extensions/agentserver-vscode/src/test/suite/respawn.test.ts`

- [ ] **Step 1: Write runTest.ts**

`src/test/runTest.ts`:
```ts
import * as path from 'path';
import { runTests } from '@vscode/test-electron';

async function main() {
  try {
    const extensionDevelopmentPath = path.resolve(__dirname, '..', '..');
    const extensionTestsPath = path.resolve(__dirname, 'suite', 'index');
    await runTests({ extensionDevelopmentPath, extensionTestsPath });
  } catch {
    console.error('Failed to run tests');
    process.exit(1);
  }
}
main();
```

- [ ] **Step 2: Write suite/index.ts**

`src/test/suite/index.ts`:
```ts
import * as path from 'path';
import Mocha from 'mocha';
import { glob } from 'glob';

export function run(): Promise<void> {
  const mocha = new Mocha({ ui: 'bdd', color: true, timeout: 20_000 });
  const testsRoot = path.resolve(__dirname);
  return new Promise(async (resolve, reject) => {
    const files = await glob('**/*.test.js', { cwd: testsRoot });
    files.forEach(f => mocha.addFile(path.resolve(testsRoot, f)));
    try {
      mocha.run(failures => failures ? reject(new Error(`${failures} test(s) failed`)) : resolve());
    } catch (e) { reject(e); }
  });
}
```

- [ ] **Step 3: Write startup.test.ts**

`src/test/suite/startup.test.ts`:
```ts
import * as assert from 'assert';
import * as vscode from 'vscode';

suite('startup', () => {
  test('codex terminal exists after activation', async () => {
    await vscode.extensions.getExtension('agentserver.agentserver-vscode')?.activate();
    // Give the activate handler time to spawn the terminal.
    await new Promise(r => setTimeout(r, 1000));
    const names = vscode.window.terminals.map(t => t.name);
    assert.ok(names.includes('codex'), `expected 'codex' terminal, got ${JSON.stringify(names)}`);
  });
});
```

- [ ] **Step 4: Write respawn.test.ts**

`src/test/suite/respawn.test.ts`:
```ts
import * as assert from 'assert';
import * as vscode from 'vscode';

suite('respawn', () => {
  test('closing codex terminal respawns one', async () => {
    await vscode.extensions.getExtension('agentserver.agentserver-vscode')?.activate();
    await new Promise(r => setTimeout(r, 500));
    const t = vscode.window.terminals.find(t => t.name === 'codex');
    assert.ok(t, 'no codex terminal to close');
    t!.dispose();
    await new Promise(r => setTimeout(r, 800));
    const names = vscode.window.terminals.map(t => t.name);
    assert.ok(names.includes('codex'), `expected respawn, got ${JSON.stringify(names)}`);
  });
});
```

- [ ] **Step 5: Add glob dep + try compile**

```bash
cd extensions/agentserver-vscode
npm install --save-dev glob @types/glob
npm run compile
```

- [ ] **Step 6: Smoke-run if X is available (otherwise CI runs on windows-latest)**

```bash
# Linux dev: xvfb-run npm test  (requires xvfb)
# Windows CI:  npm test
```

Expected on Windows CI: PASS — startup + respawn.

- [ ] **Step 7: Commit**

```bash
git add extensions/agentserver-vscode/
git commit -m "test(ext): add @vscode/test-electron + startup/respawn tests"
```

### Task P10.4: vsce package + Makefile wiring

**Files:**
- Modify: `Makefile` (already has `ext-build` target)
- Verify produces `.vsix`

- [ ] **Step 1: Run packaging**

```bash
cd extensions/agentserver-vscode
npx vsce package --out agentserver-vscode-0.1.0.vsix --no-dependencies
```

Expected: `agentserver-vscode-0.1.0.vsix` produced (~10-30 KB).

- [ ] **Step 2: Commit (no source changes, just verify)**

If Makefile target needs adjustment for the actual filename, update and commit:

```bash
# nothing if already matches
```

---

## P11 Inno Setup 打包

### Task P11.1: 资源文件 + 安装脚本

**Files:**
- Create: `packaging/windows/installer.iss`
- Create: `packaging/windows/icon.ico` (placeholder; binary)
- Create: `packaging/windows/LICENSE.zh.txt`
- Create: `scripts/package-windows.sh`

- [ ] **Step 1: Add a placeholder icon.ico**

Use any 256×256 .ico. For dev use the VS Code icon as a stand-in or generate via ImageMagick:
```bash
# requires imagemagick (apt install imagemagick)
convert -size 256x256 xc:#0066cc packaging/windows/icon.ico
```
Expected: `packaging/windows/icon.ico` exists (32 KB+).

- [ ] **Step 2: Write LICENSE.zh.txt**

`packaging/windows/LICENSE.zh.txt`:
```
agentserver-vscode 安装包

本安装包会在本机安装并配置以下软件:
  - VS Code (微软, MIT License) — 若未安装
  - codex CLI (OpenAI)
  - 一个名为 agentserver-vscode 的 VS Code 扩展

并将以下数据写入用户目录:
  - %USERPROFILE%\.agentserver-vscode\  (本安装包自身的状态)
  - %USERPROFILE%\.codex\config.toml   (合并)
  - %LOCALAPPDATA%\agentserver-vscode\ (codex.exe)
  - HKCU\Software\Classes\Directory\shell\AgentserverVscode (右键菜单)
  - 桌面 agentserver-vscode.lnk

API key 由本安装包通过 OAuth 流程从 https://code.cs.ac.cn 与
https://agent.cs.ac.cn 创建,存入 Windows 凭据管理器。

继续安装即表示您同意上述行为。
```

- [ ] **Step 3: Write installer.iss**

`packaging/windows/installer.iss`:
```pascal
; agentserver-vscode v1 Inno Setup script
; Build: ISCC.exe installer.iss
;        (Linux: wine "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" installer.iss)

#define MyAppName "agentserver-vscode"
#define MyAppVersion "0.1.0"
#define MyAppPublisher "agentserver"
#define MyAppURL "https://agent.cs.ac.cn"
#define MyAppExeName "launcher.exe"

[Setup]
AppId={{A1B2C3D4-E5F6-4789-ABCD-EF0123456789}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
DefaultDirName={localappdata}\Programs\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
OutputDir=Output
OutputBaseFilename={#MyAppName}-{#MyAppVersion}-setup
SetupIconFile=icon.ico
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
LicenseFile=LICENSE.zh.txt
UninstallDisplayIcon={app}\icon.ico
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64

[Languages]
Name: "chinesesimplified"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
; Cross-built Windows binaries
Source: "..\..\dist\windows\launcher.exe";          DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\onboarding-server.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\agentctl.exe";          DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\open-folder.exe";       DestDir: "{app}"; Flags: ignoreversion
; Bundled VS Code extension
Source: "..\..\extensions\agentserver-vscode\agentserver-vscode-0.1.0.vsix"; \
    DestDir: "{app}"; DestName: "agentserver-vscode.vsix"; Flags: ignoreversion
; Icon
Source: "icon.ico"; DestDir: "{app}"; Flags: ignoreversion
; License
Source: "LICENSE.zh.txt"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{commondesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; \
      IconFilename: "{app}\icon.ico"; Tasks: desktopicon
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; \
      IconFilename: "{app}\icon.ico"
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"

[Run]
Filename: "{app}\{#MyAppExeName}"; \
    Description: "{cm:LaunchProgram,{#MyAppName}}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Best-effort cleanup; ignored if exit non-zero
Filename: "{app}\agentctl.exe"; Parameters: "uninstall --silent"; \
    Flags: runhidden; RunOnceId: "agentctl-uninstall"
```

- [ ] **Step 4: Write packaging script**

`scripts/package-windows.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Pre-flight: cross-built binaries + .vsix must exist.
for f in dist/windows/launcher.exe dist/windows/onboarding-server.exe \
         dist/windows/agentctl.exe dist/windows/open-folder.exe \
         extensions/agentserver-vscode/agentserver-vscode-0.1.0.vsix; do
  if [[ ! -e "$f" ]]; then
    echo "missing: $f"
    echo "Run 'make cross-windows ext-build' first."
    exit 1
  fi
done

# Find ISCC.exe (Inno Setup). Local install (Windows) or wine.
ISCC=""
if command -v ISCC.exe >/dev/null 2>&1; then
  ISCC="ISCC.exe"
elif command -v iscc >/dev/null 2>&1; then
  ISCC="iscc"
elif command -v wine >/dev/null 2>&1 && \
     [[ -f "$HOME/.wine/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe" ]]; then
  ISCC="wine $HOME/.wine/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe"
else
  echo "Inno Setup not found. Install on Windows, or install via Wine:"
  echo "  wine innosetup-6.x.exe /VERYSILENT"
  exit 2
fi

cd packaging/windows
mkdir -p Output
$ISCC installer.iss
ls -la Output/
```

Make executable:
```bash
chmod +x scripts/package-windows.sh
```

- [ ] **Step 5: Smoke (skipped if no Inno Setup)**

Run: `bash scripts/package-windows.sh`
Expected on a machine with Inno Setup: produces `packaging/windows/Output/agentserver-vscode-0.1.0-setup.exe`.
Expected without: exit 2 with installation hint.

- [ ] **Step 6: Commit**

```bash
git add packaging/windows/ scripts/package-windows.sh
git commit -m "feat(packaging): Inno Setup script + Windows packaging wrapper"
```

---

## P12 集成测试 (fakeserver + flow tests)

### Task P12.1: fakeserver — modelserver + agentserver in one binary

**Files:**
- Create: `test/integration/fakeserver/fakeserver.go`
- Create: `test/integration/fakeserver/fakeserver_test.go`

- [ ] **Step 1: Write the failing test (smoke contract)**

`test/integration/fakeserver/fakeserver_test.go`:
```go
//go:build integration

package fakeserver

import (
	"net/http"
	"testing"
)

func TestStart(t *testing.T) {
	srv := Start()
	defer srv.Close()

	// device auth always returns 200
	resp, err := http.Post(srv.MSURL()+"/api/oauth2/device/auth", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Implement fakeserver**

`test/integration/fakeserver/fakeserver.go`:
```go
//go:build integration

// Package fakeserver provides a single httptest.Server that emulates the
// minimal modelserver + agentserver endpoints needed for installer flows.
package fakeserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

type Server struct {
	srv *httptest.Server

	mu       sync.Mutex
	approved bool          // device-code approved?
	approveAt time.Time
	projects []map[string]string
	keys     []map[string]any
	wsList   []map[string]string
	wsKeys   []map[string]any
}

// Start spins up the fake. Caller must Close. Approval is automatic
// after 50ms by default to keep tests fast.
func Start() *Server {
	s := &Server{}
	mux := http.NewServeMux()

	// ---- modelserver routes ----
	mux.HandleFunc("/api/oauth2/device/auth", s.handleDeviceAuth)
	mux.HandleFunc("/api/oauth2/token", s.handleToken)
	mux.HandleFunc("/api/v1/projects", s.handleProjects)
	mux.HandleFunc("/api/v1/projects/", s.handleProjectsSub) // .../keys

	// ---- agentserver routes ----
	mux.HandleFunc("/api/workspaces", s.handleWorkspaces)
	mux.HandleFunc("/api/workspaces/", s.handleWorkspacesSub) // .../api-keys

	s.srv = httptest.NewServer(mux)
	return s
}

func (s *Server) Close()        { s.srv.Close() }
func (s *Server) URL() string   { return s.srv.URL }
func (s *Server) MSURL() string { return s.srv.URL }
func (s *Server) ASURL() string { return s.srv.URL }

func (s *Server) Approve() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approved = true
	s.approveAt = time.Now()
}

func (s *Server) handleDeviceAuth(w http.ResponseWriter, r *http.Request) {
	// Auto-approve after 50ms to keep tests fast.
	go func() { time.Sleep(50 * time.Millisecond); s.Approve() }()
	writeJSON(w, 200, map[string]any{
		"device_code":               "dev-fake",
		"user_code":                 "ABCD-EFGH",
		"verification_uri":          s.srv.URL + "/verify",
		"verification_uri_complete": s.srv.URL + "/verify?u=ABCD-EFGH",
		"expires_in":                30,
		"interval":                  1,
	})
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	ok := s.approved
	s.mu.Unlock()
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "authorization_pending"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"access_token": "fake-access", "token_type": "Bearer", "expires_in": 3600,
	})
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"data": s.projects})
	case http.MethodPost:
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		defer s.mu.Unlock()
		p := map[string]string{"id": fmt.Sprintf("proj-%d", len(s.projects)+1), "name": body["name"]}
		s.projects = append(s.projects, p)
		writeJSON(w, 201, map[string]any{"data": p})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleProjectsSub(w http.ResponseWriter, r *http.Request) {
	// /api/v1/projects/{id}/keys
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/")
	if len(parts) < 2 || parts[1] != "keys" {
		http.NotFound(w, r)
		return
	}
	pid := parts[0]
	if r.Method == http.MethodPost {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		defer s.mu.Unlock()
		k := map[string]any{
			"id": fmt.Sprintf("key-%d", len(s.keys)+1), "project_id": pid,
			"name": body["name"], "key_suffix": "wxyz", "status": "active",
		}
		s.keys = append(s.keys, k)
		writeJSON(w, 201, map[string]any{"data": k, "key": "ms-fakeapikey-1234"})
		return
	}
	if r.Method == http.MethodGet {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"data": s.keys})
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"data": s.wsList})
	case http.MethodPost:
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		defer s.mu.Unlock()
		ws := map[string]string{"id": fmt.Sprintf("ws-%d", len(s.wsList)+1), "name": body["name"]}
		s.wsList = append(s.wsList, ws)
		writeJSON(w, 201, map[string]any{"data": ws})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleWorkspacesSub(w http.ResponseWriter, r *http.Request) {
	// /api/workspaces/{wid}/api-keys
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/workspaces/"), "/")
	if len(parts) < 2 || parts[1] != "api-keys" {
		http.NotFound(w, r)
		return
	}
	wid := parts[0]
	if r.Method == http.MethodPost {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		defer s.mu.Unlock()
		k := map[string]any{
			"id": fmt.Sprintf("wskey-%d", len(s.wsKeys)+1), "workspace_id": wid,
			"name": body["name"], "key_suffix": "ab12",
		}
		s.wsKeys = append(s.wsKeys, k)
		writeJSON(w, 201, map[string]any{"data": k, "key": "ws-sk-fakekey-1234"})
		return
	}
	http.NotFound(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 3: Run, expect PASS**

Run: `go test -tags=integration -race ./test/integration/fakeserver/...`
Expected: PASS — TestStart.

- [ ] **Step 4: Commit**

```bash
git add test/integration/fakeserver/
git commit -m "test(integration): in-process fakeserver for modelserver+agentserver"
```

### Task P12.2: vscode_stub — fake `code` CLI that records calls

**Files:**
- Create: `test/integration/vscode_stub/main.go`
- Create: `test/integration/vscode_stub/build.sh`

- [ ] **Step 1: Write the stub**

`test/integration/vscode_stub/main.go`:
```go
// vscode_stub is a fake `code` CLI used in integration tests. It records
// every invocation (argv joined by tab) to $VSCODE_STUB_LOG, and outputs
// a fixed version when called with --version.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	args := os.Args[1:]
	if logPath := os.Getenv("VSCODE_STUB_LOG"); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintln(f, strings.Join(args, "\t"))
			f.Close()
		}
	}
	if len(args) > 0 && args[0] == "--version" {
		fmt.Println("1.96.0")
		fmt.Println("abcdef0123")
		fmt.Println("x64")
		return
	}
}
```

- [ ] **Step 2: Write build helper**

`test/integration/vscode_stub/build.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
go build -o code .
echo "built: $(pwd)/code"
```

```bash
chmod +x test/integration/vscode_stub/build.sh
```

- [ ] **Step 3: Verify**

```bash
bash test/integration/vscode_stub/build.sh
VSCODE_STUB_LOG=/tmp/calls.log test/integration/vscode_stub/code --version
cat /tmp/calls.log
```

Expected: log contains `--version`; stdout prints `1.96.0`.

- [ ] **Step 4: Commit**

```bash
git add test/integration/vscode_stub/
git commit -m "test(integration): fake `code` CLI that records invocations"
```

### Task P12.3: full-onboarding flow test

**Files:**
- Create: `test/integration/flows/full_onboarding_test.go`

- [ ] **Step 1: Write failing test**

`test/integration/flows/full_onboarding_test.go`:
```go
//go:build integration

package flows

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/ui"
	"github.com/agentserver/agentserver-pkg/test/integration/fakeserver"
)

func TestFullOnboarding_MS_AS(t *testing.T) {
	fake := fakeserver.Start()
	defer fake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	sec := secrets.New(filepath.Join(dir, "secrets.json"))

	deps := ui.Deps{
		State: store, Secrets: sec,
		MS: modelserver.New(fake.MSURL()),
		AS: agentserver.New(fake.ASURL()),
		MSOAuth: oauth.Config{Endpoint: fake.MSURL(),
			AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
			ClientID: "test", Scope: "openid"},
		ASOAuth: oauth.Config{Endpoint: fake.ASURL(),
			AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
			ClientID: "test", Scope: "openid"},
	}
	orch := ui.NewRealOrchestrator(deps)

	srv := httptest.NewServer(ui.NewServer(orch, nil))
	defer srv.Close()

	// STEP 1 MS login
	mustPost(t, srv.URL+"/api/step/modelserver_login")
	pollUntilSuccess(t, srv.URL+"/api/step/modelserver_login/status")

	// STEP 2 AS login
	mustPost(t, srv.URL+"/api/step/agentserver_login")
	pollUntilSuccess(t, srv.URL+"/api/step/agentserver_login/status")

	// Verify state
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("modelserver_login") ||
		!s.Onboarding.HasCompleted("agentserver_login") {
		t.Errorf("steps not complete: %+v", s.Onboarding.CompletedSteps)
	}
	if s.Modelserver.ProjectID == "" || s.Agentserver.WorkspaceID == "" {
		t.Errorf("missing ids: %+v", s)
	}
	// Verify secrets stored
	if v, err := sec.Get("modelserver_api_key"); err != nil || v == "" {
		t.Errorf("ms key not stored: %v", err)
	}
	if v, err := sec.Get("agentserver_ws_api_key"); err != nil || v == "" {
		t.Errorf("ws key not stored: %v", err)
	}
}

func mustPost(t *testing.T, url string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: status %d: %s", url, resp.StatusCode, body)
	}
}

func pollUntilSuccess(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		var body map[string]any
		json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if body["state"] == "success" {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("step %s did not reach success in time", url)
}

// Used to keep imports alive should helpers shrink.
var _ = bytes.NewReader
var _ = os.Open
```

- [ ] **Step 2: Run, expect PASS**

Run: `go test -tags=integration -race -count=1 ./test/integration/flows/...`
Expected: PASS — TestFullOnboarding_MS_AS.

- [ ] **Step 3: Commit**

```bash
git add test/integration/flows/
git commit -m "test(integration): full MS+AS onboarding flow"
```

### Task P12.4: resume / retry / idempotency tests

**Files:**
- Create: `test/integration/flows/resume_test.go`
- Create: `test/integration/flows/idempotent_test.go`

- [ ] **Step 1: resume_test.go**

`test/integration/flows/resume_test.go`:
```go
//go:build integration

package flows

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/ui"
	"github.com/agentserver/agentserver-pkg/test/integration/fakeserver"
)

func TestResumeAfterRestart(t *testing.T) {
	fake := fakeserver.Start()
	defer fake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	sec := secrets.New(filepath.Join(dir, "secrets.json"))

	mkServer := func() *httptest.Server {
		deps := ui.Deps{
			State: store, Secrets: sec,
			MS: modelserver.New(fake.MSURL()),
			AS: agentserver.New(fake.ASURL()),
			MSOAuth: oauth.Config{Endpoint: fake.MSURL(),
				AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
				ClientID: "test"},
			ASOAuth: oauth.Config{Endpoint: fake.ASURL(),
				AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
				ClientID: "test"},
		}
		return httptest.NewServer(ui.NewServer(ui.NewRealOrchestrator(deps), nil))
	}

	srv := mkServer()
	mustPost(t, srv.URL+"/api/step/modelserver_login")
	pollUntilSuccess(t, srv.URL+"/api/step/modelserver_login/status")
	srv.Close() // simulate kill

	srv2 := mkServer()
	defer srv2.Close()
	// state retains MS done; AS should still work
	mustPost(t, srv2.URL+"/api/step/agentserver_login")
	pollUntilSuccess(t, srv2.URL+"/api/step/agentserver_login/status")

	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("modelserver_login") ||
		!s.Onboarding.HasCompleted("agentserver_login") {
		t.Errorf("steps after resume: %+v", s.Onboarding.CompletedSteps)
	}
}
```

- [ ] **Step 2: idempotent_test.go**

`test/integration/flows/idempotent_test.go`:
```go
//go:build integration

package flows

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/ui"
	"github.com/agentserver/agentserver-pkg/test/integration/fakeserver"
)

func TestIdempotentWorkspace(t *testing.T) {
	fake := fakeserver.Start()
	defer fake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	sec := secrets.New(filepath.Join(dir, "secrets.json"))

	deps := ui.Deps{
		State: store, Secrets: sec,
		MS: modelserver.New(fake.MSURL()),
		AS: agentserver.New(fake.ASURL()),
		MSOAuth: oauth.Config{Endpoint: fake.MSURL(),
			AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
			ClientID: "test"},
		ASOAuth: oauth.Config{Endpoint: fake.ASURL(),
			AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
			ClientID: "test"},
	}
	srv := httptest.NewServer(ui.NewServer(ui.NewRealOrchestrator(deps), nil))
	defer srv.Close()

	// Run AS login twice
	mustPost(t, srv.URL+"/api/step/agentserver_login")
	pollUntilSuccess(t, srv.URL+"/api/step/agentserver_login/status")
	mustPost(t, srv.URL+"/api/step/agentserver_login")
	pollUntilSuccess(t, srv.URL+"/api/step/agentserver_login/status")

	s, _ := store.Load()
	if s.Agentserver.WorkspaceID == "" {
		t.Errorf("workspace id missing: %+v", s.Agentserver)
	}
	// fakeserver should only have ONE workspace, because GetOrCreate dedupes.
	// (This relies on fakeserver returning the same id for the same name.)
}
```

- [ ] **Step 3: Run, expect PASS**

Run: `go test -tags=integration -race -count=1 ./test/integration/flows/...`
Expected: PASS — 3 tests.

- [ ] **Step 4: Commit**

```bash
git add test/integration/flows/
git commit -m "test(integration): resume after kill + idempotent workspace"
```

---

## P13 Windows E2E (远程 SSH)

### Task P13.1: harness — ssh + pwsh wrappers

**Files:**
- Create: `test/e2e/windows/harness/ssh.go`
- Create: `test/e2e/windows/harness/pwsh.go`

- [ ] **Step 1: Write ssh.go**

`test/e2e/windows/harness/ssh.go`:
```go
//go:build e2e

// Package harness wraps SSH + PowerShell + WebDriver for the Windows E2E.
package harness

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
)

type Client struct {
	*ssh.Client
}

// Dial reads env: E2E_SSH_HOST, E2E_SSH_PORT, E2E_SSH_USER, E2E_SSH_PASSWORD.
func Dial() (*Client, error) {
	host := os.Getenv("E2E_SSH_HOST")
	port := os.Getenv("E2E_SSH_PORT")
	user := os.Getenv("E2E_SSH_USER")
	pass := os.Getenv("E2E_SSH_PASSWORD")
	if host == "" || user == "" {
		return nil, fmt.Errorf("E2E_SSH_HOST and E2E_SSH_USER required")
	}
	if port == "" {
		port = "22"
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // E2E only
		Timeout:         15 * time.Second,
	}
	c, err := ssh.Dial("tcp", host+":"+port, cfg)
	if err != nil {
		return nil, err
	}
	return &Client{c}, nil
}

// PutFile writes local path src to remote path dst via SCP-like protocol.
// Implemented as cat over an SSH session (Windows OpenSSH supports redirection).
func (c *Client) PutFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf(`powershell -NoProfile -Command "[System.IO.File]::WriteAllBytes('%s', [Convert]::FromBase64String((Get-Content -Raw -Path -)))"`,
		dst)
	if err := sess.Start(cmd); err != nil {
		return err
	}
	// stream base64 of file body to stdin
	encoded := newBase64Pipe(in)
	if _, err := io.Copy(stdin, encoded); err != nil {
		return err
	}
	stdin.Close()
	return sess.Wait()
}

func (c *Client) GetFile(remoteSrc, localDst string) error {
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	var out bytes.Buffer
	sess.Stdout = &out
	if err := sess.Run(fmt.Sprintf(`powershell -NoProfile -Command "[Convert]::ToBase64String([System.IO.File]::ReadAllBytes('%s'))"`,
		remoteSrc)); err != nil {
		return err
	}
	dec := newBase64Reader(&out)
	if err := os.MkdirAll(filepath.Dir(localDst), 0o755); err != nil {
		return err
	}
	f, err := os.Create(localDst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, dec)
	return err
}
```

`test/e2e/windows/harness/base64pipe.go`:
```go
//go:build e2e

package harness

import (
	"encoding/base64"
	"io"
)

func newBase64Pipe(r io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		enc := base64.NewEncoder(base64.StdEncoding, pw)
		_, err := io.Copy(enc, r)
		enc.Close()
		pw.CloseWithError(err)
	}()
	return pr
}

func newBase64Reader(r io.Reader) io.Reader {
	return base64.NewDecoder(base64.StdEncoding, r)
}
```

- [ ] **Step 2: Write pwsh.go**

`test/e2e/windows/harness/pwsh.go`:
```go
//go:build e2e

package harness

import (
	"bytes"
	"fmt"
	"strings"
)

// Pwsh runs `powershell -NoProfile -Command <script>` and returns stdout
// (or stderr on non-zero exit) along with the exit code.
func (c *Client) Pwsh(script string) (stdout string, exitCode int, err error) {
	sess, err := c.NewSession()
	if err != nil {
		return "", -1, err
	}
	defer sess.Close()
	var outB, errB bytes.Buffer
	sess.Stdout = &outB
	sess.Stderr = &errB
	full := fmt.Sprintf("powershell -NoProfile -Command %q", script)
	runErr := sess.Run(full)
	if runErr == nil {
		return outB.String(), 0, nil
	}
	if ee, ok := runErr.(*sshExitError); ok {
		return strings.TrimSpace(outB.String() + errB.String()), ee.ExitStatus(), nil
	}
	return strings.TrimSpace(outB.String() + errB.String()), -1, runErr
}

// sshExitError is just *ssh.ExitError aliased to avoid leaking import.
type sshExitError interface {
	ExitStatus() int
}
```

- [ ] **Step 3: Add golang.org/x/crypto + go mod tidy**

```bash
go get golang.org/x/crypto/ssh@latest
go mod tidy
```

- [ ] **Step 4: Build check (no test until E2E env wired)**

```bash
go vet -tags=e2e ./test/e2e/windows/...
```

- [ ] **Step 5: Commit**

```bash
git add test/e2e/windows/harness/ go.mod go.sum
git commit -m "test(e2e): ssh + pwsh wrappers for Windows remote driving"
```

### Task P13.2: harness — webdriver wrapper for OAuth

**Files:**
- Create: `test/e2e/windows/harness/webdriver.go`

- [ ] **Step 1: Write webdriver wrapper using chromedp-style remote**

For v1 we use WebDriver-W3C JSON wire over HTTP against a chromedriver
that the E2E setup launches on the Windows side (port 9515).

`test/e2e/windows/harness/webdriver.go`:
```go
//go:build e2e

package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type WebDriver struct {
	base    string
	session string
}

// NewWebDriver expects a chromedriver listening at base (e.g. http://127.0.0.1:9515).
// Caller is responsible for launching chromedriver on the Windows host
// (e.g. via PowerShell: Start-Process -FilePath chromedriver.exe).
func NewWebDriver(base string) (*WebDriver, error) {
	w := &WebDriver{base: base}
	resp, err := w.post("/session", map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]any{
				"browserName": "chrome",
				"goog:chromeOptions": map[string]any{
					"args": []string{"--no-sandbox", "--disable-gpu"},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Value struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, err
	}
	w.session = out.Value.SessionID
	return w, nil
}

func (w *WebDriver) Close() {
	if w.session != "" {
		_, _ = w.do("DELETE", "/session/"+w.session, nil)
	}
}

func (w *WebDriver) Go(url string) error {
	_, err := w.post("/session/"+w.session+"/url", map[string]string{"url": url})
	return err
}

func (w *WebDriver) FindAndType(cssSelector, text string) error {
	id, err := w.findElement(cssSelector)
	if err != nil {
		return err
	}
	_, err = w.post(fmt.Sprintf("/session/%s/element/%s/value", w.session, id),
		map[string]any{"text": text})
	return err
}

func (w *WebDriver) Click(cssSelector string) error {
	id, err := w.findElement(cssSelector)
	if err != nil {
		return err
	}
	_, err = w.post(fmt.Sprintf("/session/%s/element/%s/click", w.session, id), map[string]any{})
	return err
}

func (w *WebDriver) WaitForTitle(substr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		t, err := w.title()
		if err == nil && containsCI(t, substr) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("title %q not seen", substr)
}

func (w *WebDriver) title() (string, error) {
	resp, err := w.do("GET", "/session/"+w.session+"/title", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

func (w *WebDriver) findElement(selector string) (string, error) {
	resp, err := w.post("/session/"+w.session+"/element",
		map[string]string{"using": "css selector", "value": selector})
	if err != nil {
		return "", err
	}
	var out struct {
		Value map[string]string `json:"value"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return "", err
	}
	for k, v := range out.Value {
		if k != "ELEMENT" && k[0] != 'e' { // shape: {"element-6066-11e4-a52e-4f735466cecf": "..."}
			continue
		}
		return v, nil
	}
	return "", fmt.Errorf("no element id in response: %s", resp)
}

func (w *WebDriver) post(path string, body any) ([]byte, error) {
	return w.do("POST", path, body)
}

func (w *WebDriver) do(method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, w.base+path, rdr)
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return out, fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, out)
	}
	return out, nil
}

func containsCI(haystack, needle string) bool {
	// crude case-insensitive contains
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if equalFold(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if lower(a[i]) != lower(b[i]) {
			return false
		}
	}
	return true
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}
```

- [ ] **Step 2: Commit**

```bash
git add test/e2e/windows/harness/webdriver.go
git commit -m "test(e2e): minimal W3C WebDriver wrapper for browser-driven OAuth"
```

### Task P13.3: full E2E test

**Files:**
- Create: `test/e2e/windows/e2e_test.go`

- [ ] **Step 1: Write the E2E**

`test/e2e/windows/e2e_test.go`:
```go
//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/test/e2e/windows/harness"
)

// TestWindowsE2E runs the full install → onboard → verify → uninstall cycle.
// Requires env: E2E_SSH_HOST/PORT/USER/PASSWORD, TEST_MS_USER/PASS, TEST_AS_USER/PASS.
func TestWindowsE2E(t *testing.T) {
	if os.Getenv("E2E_SSH_HOST") == "" {
		t.Skip("E2E_SSH_HOST not set; skipping")
	}
	c, err := harness.Dial()
	if err != nil {
		t.Fatalf("ssh: %v", err)
	}
	defer c.Close()

	// 1. Locate locally-built setup .exe.
	setupExe := filepath.Join("..", "..", "..", "packaging", "windows", "Output",
		"agentserver-vscode-0.1.0-setup.exe")
	if _, err := os.Stat(setupExe); err != nil {
		t.Fatalf("setup exe not built: %v", err)
	}

	// 2. Push installer.
	remote := `C:\Users\61414\Downloads\agentserver-vscode-setup.exe`
	if err := c.PutFile(setupExe, remote); err != nil {
		t.Fatalf("put: %v", err)
	}

	// 3. Best-effort uninstall residual.
	out, _, _ := c.Pwsh(`& 'C:\Users\61414\AppData\Local\Programs\agentserver-vscode\agentctl.exe' uninstall --silent 2>$null; $LASTEXITCODE`)
	t.Logf("pre-uninstall: %s", out)

	// 4. Silent install.
	out, code, err := c.Pwsh(fmt.Sprintf(`Start-Process -FilePath '%s' -ArgumentList '/VERYSILENT','/SUPPRESSMSGBOXES' -Wait; $LASTEXITCODE`, remote))
	if err != nil || code != 0 {
		t.Fatalf("install: code=%d err=%v out=%s", code, err, out)
	}

	// 5. Assert: desktop .lnk exists, registry has shell hook.
	out, _, _ = c.Pwsh(`Test-Path "$env:USERPROFILE\Desktop\agentserver-vscode.lnk"`)
	if strings.TrimSpace(out) != "True" {
		t.Errorf("desktop shortcut missing: %s", out)
	}
	out, _, _ = c.Pwsh(`Test-Path 'Registry::HKEY_CURRENT_USER\Software\Classes\Directory\shell\AgentserverVscode'`)
	if strings.TrimSpace(out) != "True" {
		t.Errorf("registry key missing: %s", out)
	}

	// 6. Launch launcher (in background — it serves onboarding-server).
	c.Pwsh(`Start-Process -FilePath "$env:LOCALAPPDATA\Programs\agentserver-vscode\launcher.exe"`)

	// 7. Discover onboarding port from launcher's stdout? In v1 we hardcode
	//    a fixed port via env var. For now, look at netstat.
	port := waitForOnboardingPort(t, c)
	t.Logf("onboarding port: %s", port)

	// 8. Open chromedriver session, complete MS+AS OAuth (test accounts).
	//    Assumes chromedriver.exe is on PATH on the Windows host.
	wd, err := harness.NewWebDriver("http://127.0.0.1:9515")
	if err != nil {
		t.Fatalf("webdriver: %v", err)
	}
	defer wd.Close()
	wd.Go(fmt.Sprintf("http://127.0.0.1:%s/", port))
	wd.Click("button") // click first "开始" -> triggers MS login + opens OAuth tab
	// Switch to OAuth tab, fill credentials, submit.
	// (Wire-level chrome target switching is omitted in this excerpt;
	// see harness/webdriver.go extensions in real impl.)
	fillOAuth(t, wd, os.Getenv("TEST_MS_USER"), os.Getenv("TEST_MS_PASS"))

	// Wait for onboarding state == complete (poll up to 5 min)
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		out, _, _ := c.Pwsh(fmt.Sprintf(`(Invoke-RestMethod http://127.0.0.1:%s/api/state).onboarding_status`, port))
		if strings.TrimSpace(out) == "complete" {
			goto verify
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatal("onboarding did not complete within 5 minutes")

verify:
	// 9. Assertions
	out, _, _ = c.Pwsh(`Get-Content "$env:USERPROFILE\.codex\config.toml"`)
	if !strings.Contains(out, `model_provider = "modelserver"`) {
		t.Errorf("codex config wrong: %s", out)
	}
	out, _, _ = c.Pwsh(`& "$env:LOCALAPPDATA\Programs\Microsoft VS Code\bin\code.cmd" --version`)
	if !strings.HasPrefix(strings.TrimSpace(out), "1.") {
		t.Errorf("vs code missing: %s", out)
	}

	// 10. Right-click open simulation
	c.Pwsh(`New-Item -ItemType Directory -Force "C:\tmp\e2e-test"`)
	c.Pwsh(`& "$env:LOCALAPPDATA\Programs\agentserver-vscode\open-folder.exe" "C:\tmp\e2e-test"`)
	time.Sleep(3 * time.Second)

	// 11. Cleanup
	c.Pwsh(`& "$env:LOCALAPPDATA\Programs\agentserver-vscode\agentctl.exe" uninstall --silent`)
	c.Pwsh(`Remove-Item -Recurse -Force "C:\tmp\e2e-test"`)
}

func waitForOnboardingPort(t *testing.T, c *harness.Client) string {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, _, _ := c.Pwsh(`(Get-Process launcher -ErrorAction SilentlyContinue | Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue).LocalPort`)
		out = strings.TrimSpace(out)
		if out != "" {
			return strings.Split(out, "\n")[0]
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatal("could not find onboarding-server port")
	return ""
}

func fillOAuth(t *testing.T, wd *harness.WebDriver, user, pass string) {
	// site-specific; placeholder selectors below should be updated after
	// inspecting the real login page in dev.
	wd.WaitForTitle("登录", 30*time.Second)
	wd.FindAndType("input[name='username']", user)
	wd.FindAndType("input[name='password']", pass)
	wd.Click("button[type='submit']")
}

// Avoid "imported and not used" on json/io
var _ = json.NewDecoder
```

- [ ] **Step 2: Commit**

```bash
git add test/e2e/windows/
git commit -m "test(e2e): full Windows install→onboard→verify→uninstall flow"
```

### Task P13.4: First real-run iteration

When the first real E2E runs against `10.128.185.173`:

- [ ] **Step 1: Push initial setup + read launcher.log**

```bash
make cross-windows
make ext-build
bash scripts/package-windows.sh
E2E_SSH_HOST=10.128.185.173 E2E_SSH_PORT=2222 E2E_SSH_USER=61414 \
  E2E_SSH_PASSWORD=... \
  TEST_MS_USER=... TEST_MS_PASS=... \
  TEST_AS_USER=... TEST_AS_PASS=... \
  go test -tags=e2e -v -count=1 -timeout=30m ./test/e2e/windows/...
```

- [ ] **Step 2: Capture pain points**

Likely real-world hits and what to do:

| Issue | Fix |
|---|---|
| chromedriver not on Windows PATH | Add `scripts/setup-windows-e2e.ps1` to download chromedriver + install |
| OAuth selectors wrong (Hydra page differs from placeholder) | Inspect the real page, update `fillOAuth` selectors |
| onboarding-server port discovery via `Get-NetTCPConnection` flaky | Have launcher write port to `%USERPROFILE%\.agentserver-vscode\port.txt` |
| `setx` doesn't refresh codex's existing terminal | Document that user closes/reopens terminal once |
| VS Code SHA256 doesn't match locked value | Refresh `lockedSHA256Win64UserPlaceholder` from `https://code.visualstudio.com/sha?build=stable` |

- [ ] **Step 3: Commit any fixes**

```bash
git add -A
git commit -m "fix(e2e): adjust to real Windows host"
```

---

## Done. Total ~13 phases, ~70 tasks.

After all phases:
- `make test` green
- `make cross-windows ext-build package` produces `.exe`
- E2E run via `workflow_dispatch` or tag release

Next: subagent-driven execution or inline execution.
