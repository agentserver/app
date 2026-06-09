# Local Slave Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add local multi-slave management to the completed console, including immutable computer/slave naming, per-folder slave processes, per-slave reauthentication, and agentserver card source labeling.

**Architecture:** Add a focused `internal/slave` package for machine identity, slave registry, loom config generation, and process control. Wire it into the existing `internal/console.Controller` and `/api/console/*` server surface, then expose the controls in `Dashboard.vue`. Installers initialize the immutable computer name before the console creates slaves.

**Tech Stack:** Go stdlib HTTP/process/filesystem, existing `state.Store`/`secrets.Store`, YAML via `gopkg.in/yaml.v3`, Vue 3 + Element Plus + Vitest, Windows PowerShell/Inno Setup.

---

## File Structure

- Create `internal/slave/machine.go`: machine identity model, file store, immutable computer name initialization.
- Create `internal/slave/machine_test.go`: machine identity tests.
- Create `internal/slave/registry.go`: slave registry model, validation, create/list/update/delete operations.
- Create `internal/slave/registry_test.go`: registry and validation tests.
- Create `internal/slave/config.go`: loom `config.yaml` generation for Codex-backed slave agents and agentserver card fields.
- Create `internal/slave/config_test.go`: YAML content tests for `discovery.*` and `resources.tags`.
- Create `internal/slave/process.go`: process runner abstraction, start/pause/restart/delete behavior, auth URL detection.
- Create `internal/slave/process_test.go`: fake runner lifecycle tests.
- Modify `internal/paths/paths.go`: add `MachineFile`, `SlavesFile`, and `SlavesDir`.
- Modify `internal/console/state.go`: include slave manager dependency and action methods.
- Modify `internal/console/state_test.go`: controller tests for slave list/actions.
- Modify `internal/ui/console.go`: extend `ConsoleController` with slave methods.
- Modify `internal/ui/server.go`: add `/api/console/slaves` routes.
- Modify `internal/ui/server_test.go`: HTTP route and method tests.
- Modify `cmd/launcher/main.go`: construct `slave.Manager` for completed console with installed `slave-agent.exe`.
- Modify `internal/ui/web/src/api.ts`: add slave types and API calls.
- Modify `internal/ui/web/src/__tests__/api.spec.ts`: API call tests.
- Modify `internal/ui/web/src/components/Dashboard.vue`: render local-agent section and controls.
- Modify `internal/ui/web/src/__tests__/Dashboard.spec.ts`: UI behavior tests.
- Create `packaging/windows/machine.ps1`: shared machine identity initializer.
- Modify `packaging/windows/install.ps1`: call machine initializer for portable installs.
- Modify `packaging/windows/installer.iss`: add GUI computer-name page and call machine initializer.
- Modify `scripts/package-windows.sh` and `scripts/package-windows-zip.sh`: bundle `machine.ps1`.
- Modify `internal/vscode/install_test.go`: packaging tests for `machine.ps1`.

---

### Task 1: Machine Identity Store

**Files:**
- Create: `internal/slave/machine.go`
- Create: `internal/slave/machine_test.go`
- Modify: `internal/paths/paths.go`

- [ ] **Step 1: Write failing tests for immutable machine identity**

Create `internal/slave/machine_test.go`:

```go
package slave

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMachineStoreInitializesComputerNameOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.json")
	store := NewMachineStore(path)

	first, err := store.Ensure("61414-PC")
	if err != nil {
		t.Fatalf("Ensure first: %v", err)
	}
	if first.ComputerName != "61414-PC" {
		t.Fatalf("ComputerName=%q", first.ComputerName)
	}
	if first.MachineID == "" {
		t.Fatal("MachineID empty")
	}

	second, err := store.Ensure("OTHER-PC")
	if err != nil {
		t.Fatalf("Ensure second: %v", err)
	}
	if second.ComputerName != "61414-PC" {
		t.Fatalf("ComputerName changed to %q", second.ComputerName)
	}
	if second.MachineID != first.MachineID {
		t.Fatalf("MachineID changed from %q to %q", first.MachineID, second.MachineID)
	}
}

func TestMachineStoreRejectsBlankComputerName(t *testing.T) {
	store := NewMachineStore(filepath.Join(t.TempDir(), "machine.json"))
	if _, err := store.Ensure("   "); err == nil {
		t.Fatal("expected blank computer name error")
	}
}

func TestMachineStoreLoadsInstallerWrittenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.json")
	body, err := json.Marshal(Machine{
		MachineID:    "machine-123",
		ComputerName: "INSTALL-PC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := NewMachineStore(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.MachineID != "machine-123" || got.ComputerName != "INSTALL-PC" {
		t.Fatalf("machine=%+v", got)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/slave -run Machine -count=1`

Expected: FAIL because package `internal/slave` or `NewMachineStore` is undefined.

- [ ] **Step 3: Add machine paths**

Modify `internal/paths/paths.go` by adding fields to `Paths`:

```go
	MachineFile string
	SlavesFile  string
	SlavesDir   string
```

Initialize them in `Default()` after `ConsoleNotificationsFile`:

```go
		MachineFile:                      filepath.Join(root, "machine.json"),
		SlavesFile:                       filepath.Join(root, "slaves.json"),
		SlavesDir:                        filepath.Join(root, "slaves"),
```

- [ ] **Step 4: Implement machine store**

Create `internal/slave/machine.go`:

```go
package slave

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type Machine struct {
	MachineID    string `json:"machine_id"`
	ComputerName string `json:"computer_name"`
}

type MachineStore struct {
	path string
}

func NewMachineStore(path string) *MachineStore {
	return &MachineStore{path: path}
}

func (s *MachineStore) Load() (Machine, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Machine{}, os.ErrNotExist
	}
	if err != nil {
		return Machine{}, fmt.Errorf("read machine: %w", err)
	}
	var m Machine
	if err := json.Unmarshal(b, &m); err != nil {
		return Machine{}, fmt.Errorf("parse machine: %w", err)
	}
	if strings.TrimSpace(m.MachineID) == "" || strings.TrimSpace(m.ComputerName) == "" {
		return Machine{}, fmt.Errorf("machine identity incomplete")
	}
	return m, nil
}

func (s *MachineStore) Ensure(computerName string) (Machine, error) {
	if existing, err := s.Load(); err == nil {
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Machine{}, err
	}
	name := strings.TrimSpace(computerName)
	if name == "" {
		return Machine{}, fmt.Errorf("computer name required")
	}
	m := Machine{MachineID: uuid.NewString(), ComputerName: name}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return Machine{}, fmt.Errorf("mkdir machine dir: %w", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Machine{}, fmt.Errorf("marshal machine: %w", err)
	}
	if err := os.WriteFile(s.path, append(b, '\n'), 0o600); err != nil {
		return Machine{}, fmt.Errorf("write machine: %w", err)
	}
	return m, nil
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/slave -run Machine -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/paths/paths.go internal/slave/machine.go internal/slave/machine_test.go
git commit -m "feat: add local machine identity store"
```

---

### Task 2: Slave Registry and Validation

**Files:**
- Create: `internal/slave/registry.go`
- Create: `internal/slave/registry_test.go`

- [ ] **Step 1: Write failing registry tests**

Create `internal/slave/registry_test.go`:

```go
package slave

import (
	"path/filepath"
	"testing"
)

func TestRegistryCreatesSlaveWithDefaultFolderName(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "project-a")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	got, err := reg.Create(m, CreateInput{Folder: folder})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Name != "project-a" {
		t.Fatalf("Name=%q", got.Name)
	}
	if got.DisplayName != "61414-PC-project-a" {
		t.Fatalf("DisplayName=%q", got.DisplayName)
	}
	if got.Folder != folder {
		t.Fatalf("Folder=%q", got.Folder)
	}
	if got.ConfigPath == "" || filepath.Dir(got.ConfigPath) != filepath.Join(dir, "slaves", got.ID) {
		t.Fatalf("ConfigPath=%q", got.ConfigPath)
	}
}

func TestRegistryCreatesSlaveWithCustomImmutableName(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	got, err := reg.Create(m, CreateInput{Folder: folder, Name: "前端调试"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Name != "前端调试" || got.DisplayName != "61414-PC-前端调试" {
		t.Fatalf("slave=%+v", got)
	}

	loaded, err := reg.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 1 || loaded[0].DisplayName != got.DisplayName {
		t.Fatalf("loaded=%+v", loaded)
	}
}

func TestRegistryRejectsInvalidCreateInput(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	if _, err := reg.Create(m, CreateInput{Folder: filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("expected missing folder error")
	}

	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Create(m, CreateInput{Folder: folder, Name: "123456789012345678901"}); err == nil {
		t.Fatal("expected long name error")
	}
	if _, err := reg.Create(Machine{}, CreateInput{Folder: folder}); err == nil {
		t.Fatal("expected missing machine error")
	}
}

func TestRegistryRejectsDuplicateDisplayName(t *testing.T) {
	dir := t.TempDir()
	folderA := filepath.Join(dir, "a")
	folderB := filepath.Join(dir, "b")
	_ = mkdir(folderA)
	_ = mkdir(folderB)
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	if _, err := reg.Create(m, CreateInput{Folder: folderA, Name: "worker"}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if _, err := reg.Create(m, CreateInput{Folder: folderB, Name: "worker"}); err == nil {
		t.Fatal("expected duplicate display name error")
	}
}

func mkdir(path string) error {
	return osMkdirAll(path)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/slave -run Registry -count=1`

Expected: FAIL because `NewRegistry`, `CreateInput`, and `osMkdirAll` are undefined.

- [ ] **Step 3: Implement registry types and create/list**

Create `internal/slave/registry.go`:

```go
package slave

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

var osMkdirAll = os.MkdirAll

type Status string

const (
	StatusStopped      Status = "stopped"
	StatusStarting     Status = "starting"
	StatusAuthRequired Status = "auth_required"
	StatusRunning      Status = "running"
	StatusPaused       Status = "paused"
	StatusError        Status = "error"
)

type Slave struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Folder      string    `json:"folder"`
	ConfigPath  string    `json:"config_path"`
	LogPath     string    `json:"log_path"`
	Status      Status    `json:"status"`
	PID         int       `json:"pid,omitempty"`
	AuthURL     string    `json:"auth_url,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateInput struct {
	Folder string
	Name   string
}

type Registry struct {
	path      string
	slavesDir string
}

func NewRegistry(path, slavesDir string) *Registry {
	return &Registry{path: path, slavesDir: slavesDir}
}

func (r *Registry) List() ([]Slave, error) {
	all, err := r.load()
	if err != nil {
		return nil, err
	}
	return append([]Slave(nil), all...), nil
}

func (r *Registry) Create(machine Machine, in CreateInput) (Slave, error) {
	if strings.TrimSpace(machine.MachineID) == "" || strings.TrimSpace(machine.ComputerName) == "" {
		return Slave{}, fmt.Errorf("machine identity required")
	}
	folder, err := filepath.Abs(strings.TrimSpace(in.Folder))
	if err != nil || folder == "" {
		return Slave{}, fmt.Errorf("folder required")
	}
	info, err := os.Stat(folder)
	if err != nil {
		return Slave{}, fmt.Errorf("folder unavailable: %w", err)
	}
	if !info.IsDir() {
		return Slave{}, fmt.Errorf("folder is not a directory: %s", folder)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = filepath.Base(folder)
	}
	if err := validateSlaveName(name); err != nil {
		return Slave{}, err
	}
	displayName := machine.ComputerName + "-" + name
	all, err := r.load()
	if err != nil {
		return Slave{}, err
	}
	for _, existing := range all {
		if existing.DisplayName == displayName {
			return Slave{}, fmt.Errorf("slave display name already exists: %s", displayName)
		}
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	dir := filepath.Join(r.slavesDir, id)
	sl := Slave{
		ID:          id,
		Name:        name,
		DisplayName: displayName,
		Folder:      folder,
		ConfigPath:  filepath.Join(dir, "config.yaml"),
		LogPath:     filepath.Join(dir, "logs", "slave.log"),
		Status:      StatusStopped,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	all = append(all, sl)
	if err := r.save(all); err != nil {
		return Slave{}, err
	}
	return sl, nil
}

func validateSlaveName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("slave name required")
	}
	if len([]rune(name)) > 20 {
		return fmt.Errorf("slave name must be at most 20 characters")
	}
	if strings.ContainsAny(name, `\/:*?"<>|`) {
		return fmt.Errorf("slave name contains invalid path characters")
	}
	return nil
}

func (r *Registry) load() ([]Slave, error) {
	b, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Slave{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read slaves: %w", err)
	}
	var all []Slave
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("parse slaves: %w", err)
	}
	return all, nil
}

func (r *Registry) save(all []Slave) error {
	if err := osMkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("mkdir slave registry dir: %w", err)
	}
	if err := osMkdirAll(r.slavesDir, 0o755); err != nil {
		return fmt.Errorf("mkdir slaves dir: %w", err)
	}
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal slaves: %w", err)
	}
	return os.WriteFile(r.path, append(b, '\n'), 0o600)
}
```

- [ ] **Step 4: Fix test imports**

Add imports to `internal/slave/registry_test.go`:

```go
import (
	"os"
	"path/filepath"
	"testing"
)
```

The helper remains:

```go
func mkdir(path string) error {
	return os.MkdirAll(path, 0o755)
}
```

Remove use of `osMkdirAll` from the test helper.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/slave -run Registry -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/slave/registry.go internal/slave/registry_test.go
git commit -m "feat: add local slave registry"
```

---

### Task 3: Loom Slave Config Generation

**Files:**
- Create: `internal/slave/config.go`
- Create: `internal/slave/config_test.go`

- [ ] **Step 1: Write failing config tests**

Create `internal/slave/config_test.go`:

```go
package slave

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteConfigPublishesMachineSourceInAgentserverCardFields(t *testing.T) {
	dir := t.TempDir()
	sl := Slave{
		DisplayName: "61414-PC-前端调试",
		Folder:      `C:\Users\61414\project-a`,
		ConfigPath:  filepath.Join(dir, "config.yaml"),
	}
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	if err := WriteConfig(sl, m, ConfigInput{
		ServerURL:   "https://agent.cs.ac.cn",
		ObserverURL: "https://loom.nj.cs.ac.cn:10062/",
		CodexBin:    `C:\Users\61414\AppData\Local\agentserver-vscode\bin\codex.exe`,
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	b, err := os.ReadFile(sl.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{
		`name: 61414-PC-前端调试`,
		`kind: codex`,
		`display_name: 61414-PC-前端调试`,
		`description: 来自同一台电脑：61414-PC；工作目录：C:\Users\61414\project-a`,
		`- agentserver-vscode-slave`,
		`- local-machine:machine-1`,
		`- host:61414-PC`,
		`url: https://loom.nj.cs.ac.cn:10062/`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
}

func TestWriteConfigStartsWithoutCredentialsForReauth(t *testing.T) {
	dir := t.TempDir()
	sl := Slave{DisplayName: "PC-worker", Folder: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
	m := Machine{MachineID: "machine-1", ComputerName: "PC"}

	if err := WriteConfig(sl, m, ConfigInput{ServerURL: "https://agent.cs.ac.cn"}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	b, _ := os.ReadFile(sl.ConfigPath)
	text := string(b)
	for _, want := range []string{
		`sandbox_id: ""`,
		`tunnel_token: ""`,
		`proxy_token: ""`,
		`workspace_id: ""`,
		`short_id: ""`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing empty credential %q:\n%s", want, text)
		}
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/slave -run WriteConfig -count=1`

Expected: FAIL because `WriteConfig` and `ConfigInput` are undefined.

- [ ] **Step 3: Implement config writer**

Create `internal/slave/config.go`:

```go
package slave

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const DefaultObserverURL = "https://loom.nj.cs.ac.cn:10062/"

type ConfigInput struct {
	ServerURL   string
	ObserverURL string
	CodexBin    string
}

type loomSlaveConfig struct {
	Server      loomServer      `yaml:"server"`
	Credentials loomCredentials `yaml:"credentials"`
	Agent       loomAgent       `yaml:"agent"`
	Codex       loomCodex       `yaml:"codex"`
	Discovery   loomDiscovery   `yaml:"discovery"`
	Resources   loomResources   `yaml:"resources"`
	Observer    loomObserver    `yaml:"observer"`
}

type loomServer struct {
	URL  string `yaml:"url"`
	Name string `yaml:"name"`
}

type loomCredentials struct {
	SandboxID   string `yaml:"sandbox_id"`
	TunnelToken string `yaml:"tunnel_token"`
	ProxyToken  string `yaml:"proxy_token"`
	WorkspaceID string `yaml:"workspace_id"`
	ShortID     string `yaml:"short_id"`
}

type loomAgent struct {
	Kind string `yaml:"kind"`
}

type loomCodex struct {
	Bin       string   `yaml:"bin"`
	WorkDir   string   `yaml:"workdir"`
	ExtraArgs []string `yaml:"extra_args"`
}

type loomDiscovery struct {
	DisplayName string   `yaml:"display_name"`
	Description string   `yaml:"description"`
	Skills      []string `yaml:"skills"`
}

type loomResources struct {
	Tags []string `yaml:"tags"`
}

type loomObserver struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
}

func WriteConfig(sl Slave, m Machine, in ConfigInput) error {
	if sl.DisplayName == "" || sl.Folder == "" || sl.ConfigPath == "" {
		return fmt.Errorf("slave display name, folder, and config path required")
	}
	if m.MachineID == "" || m.ComputerName == "" {
		return fmt.Errorf("machine identity required")
	}
	serverURL := in.ServerURL
	if serverURL == "" {
		serverURL = "https://agent.cs.ac.cn"
	}
	observerURL := in.ObserverURL
	if observerURL == "" {
		observerURL = DefaultObserverURL
	}
	codexBin := in.CodexBin
	if codexBin == "" {
		codexBin = "codex"
	}
	cfg := loomSlaveConfig{
		Server: loomServer{URL: serverURL, Name: sl.DisplayName},
		Credentials: loomCredentials{
			SandboxID: "", TunnelToken: "", ProxyToken: "", WorkspaceID: "", ShortID: "",
		},
		Agent: loomAgent{Kind: "codex"},
		Codex: loomCodex{Bin: codexBin, WorkDir: sl.Folder, ExtraArgs: []string{}},
		Discovery: loomDiscovery{
			DisplayName: sl.DisplayName,
			Description: fmt.Sprintf("来自同一台电脑：%s；工作目录：%s", m.ComputerName, sl.Folder),
			Skills:      []string{"chat", "file", "permissions", "register_mcp", "unregister_mcp"},
		},
		Resources: loomResources{Tags: []string{
			"agentserver-vscode-slave",
			"local-machine:" + m.MachineID,
			"host:" + m.ComputerName,
		}},
		Observer: loomObserver{Enabled: true, URL: observerURL},
	}
	if err := os.MkdirAll(filepath.Dir(sl.ConfigPath), 0o755); err != nil {
		return fmt.Errorf("mkdir slave config dir: %w", err)
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal slave config: %w", err)
	}
	return os.WriteFile(sl.ConfigPath, b, 0o600)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/slave -run WriteConfig -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/slave/config.go internal/slave/config_test.go
git commit -m "feat: write loom slave configs"
```

---

### Task 4: Slave Process Lifecycle

**Files:**
- Create: `internal/slave/process.go`
- Create: `internal/slave/process_test.go`
- Modify: `internal/slave/registry.go`

- [ ] **Step 1: Write failing process tests**

Create `internal/slave/process_test.go`:

```go
package slave

import (
	"context"
	"path/filepath"
	"testing"
)

func TestManagerCreateWritesConfigAndStartsProcess(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	runner := &fakeRunner{pid: 4321, authURL: "https://agent.cs.ac.cn/device?user_code=ABCD"}
	manager := NewManager(ManagerDeps{
		Machines:  NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry:  NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:    runner,
		SlaveExe:  filepath.Join(dir, "slave-agent.exe"),
		ServerURL: "https://agent.cs.ac.cn",
		CodexBin:  "codex",
	})
	if _, err := manager.Machines.Ensure("61414-PC"); err != nil {
		t.Fatal(err)
	}

	got, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder, Name: "worker"})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	if got.Status != StatusAuthRequired || got.PID != 4321 || got.AuthURL != runner.authURL {
		t.Fatalf("slave=%+v", got)
	}
	if runner.startedConfig != got.ConfigPath {
		t.Fatalf("startedConfig=%q want %q", runner.startedConfig, got.ConfigPath)
	}
}

func TestManagerPauseRestartAndDelete(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	_ = mkdir(folder)
	runner := &fakeRunner{pid: 1111}
	manager := NewManager(ManagerDeps{
		Machines: NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   runner,
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
	})
	_, _ = manager.Machines.Ensure("PC")
	sl, err := manager.CreateAndStart(context.Background(), CreateInput{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}

	paused, err := manager.Pause(context.Background(), sl.ID)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if paused.Status != StatusPaused || !runner.stopped[1111] {
		t.Fatalf("paused=%+v stopped=%+v", paused, runner.stopped)
	}

	runner.pid = 2222
	restarted, err := manager.Restart(context.Background(), sl.ID)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if restarted.Status != StatusStarting || restarted.PID != 2222 {
		t.Fatalf("restarted=%+v", restarted)
	}

	if err := manager.Delete(context.Background(), sl.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	all, err := manager.Registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("slaves after delete=%+v", all)
	}
}

type fakeRunner struct {
	pid           int
	authURL       string
	startedConfig string
	stopped       map[int]bool
}

func (f *fakeRunner) Start(_ context.Context, req StartRequest) (StartResult, error) {
	if f.stopped == nil {
		f.stopped = map[int]bool{}
	}
	f.startedConfig = req.ConfigPath
	return StartResult{PID: f.pid, AuthURL: f.authURL}, nil
}

func (f *fakeRunner) Stop(_ context.Context, pid int) error {
	if f.stopped == nil {
		f.stopped = map[int]bool{}
	}
	f.stopped[pid] = true
	return nil
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./internal/slave -run Manager -count=1`

Expected: FAIL because `NewManager`, `ManagerDeps`, `StartRequest`, and `StartResult` are undefined.

- [ ] **Step 3: Add registry update/delete helpers**

Append to `internal/slave/registry.go`:

```go
func (r *Registry) Get(id string) (Slave, error) {
	all, err := r.load()
	if err != nil {
		return Slave{}, err
	}
	for _, sl := range all {
		if sl.ID == id {
			return sl, nil
		}
	}
	return Slave{}, os.ErrNotExist
}

func (r *Registry) Update(id string, fn func(*Slave) error) (Slave, error) {
	all, err := r.load()
	if err != nil {
		return Slave{}, err
	}
	for i := range all {
		if all[i].ID != id {
			continue
		}
		if err := fn(&all[i]); err != nil {
			return Slave{}, err
		}
		all[i].UpdatedAt = time.Now().UTC()
		if err := r.save(all); err != nil {
			return Slave{}, err
		}
		return all[i], nil
	}
	return Slave{}, os.ErrNotExist
}

func (r *Registry) Delete(id string) (Slave, error) {
	all, err := r.load()
	if err != nil {
		return Slave{}, err
	}
	for i, sl := range all {
		if sl.ID != id {
			continue
		}
		next := append(all[:i], all[i+1:]...)
		if err := r.save(next); err != nil {
			return Slave{}, err
		}
		return sl, nil
	}
	return Slave{}, os.ErrNotExist
}
```

- [ ] **Step 4: Implement manager and runner abstraction**

Create `internal/slave/process.go`:

```go
package slave

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

type ManagerDeps struct {
	Machines  *MachineStore
	Registry  *Registry
	Runner    Runner
	SlaveExe  string
	ServerURL string
	CodexBin  string
}

type Manager struct {
	Machines *MachineStore
	Registry *Registry
	d        ManagerDeps
}

type Runner interface {
	Start(context.Context, StartRequest) (StartResult, error)
	Stop(context.Context, int) error
}

type StartRequest struct {
	Exe        string
	ConfigPath string
	LogPath    string
	WorkDir    string
}

type StartResult struct {
	PID     int
	AuthURL string
}

func NewManager(d ManagerDeps) *Manager {
	runner := d.Runner
	if runner == nil {
		runner = execRunner{}
	}
	d.Runner = runner
	return &Manager{Machines: d.Machines, Registry: d.Registry, d: d}
}

func (m *Manager) List(context.Context) (Machine, []Slave, error) {
	machine, err := m.d.Machines.Load()
	if err != nil {
		return Machine{}, nil, err
	}
	slaves, err := m.d.Registry.List()
	if err != nil {
		return Machine{}, nil, err
	}
	return machine, slaves, nil
}

func (m *Manager) CreateAndStart(ctx context.Context, in CreateInput) (Slave, error) {
	machine, err := m.d.Machines.Load()
	if err != nil {
		return Slave{}, err
	}
	sl, err := m.d.Registry.Create(machine, in)
	if err != nil {
		return Slave{}, err
	}
	if err := WriteConfig(sl, machine, ConfigInput{
		ServerURL: m.d.ServerURL,
		CodexBin:  m.d.CodexBin,
	}); err != nil {
		return Slave{}, err
	}
	return m.start(ctx, sl)
}

func (m *Manager) Restart(ctx context.Context, id string) (Slave, error) {
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return Slave{}, err
	}
	if sl.PID != 0 {
		_ = m.d.Runner.Stop(ctx, sl.PID)
	}
	return m.start(ctx, sl)
}

func (m *Manager) Pause(ctx context.Context, id string) (Slave, error) {
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return Slave{}, err
	}
	if sl.PID != 0 {
		if err := m.d.Runner.Stop(ctx, sl.PID); err != nil {
			return Slave{}, err
		}
	}
	return m.d.Registry.Update(id, func(s *Slave) error {
		s.Status = StatusPaused
		s.PID = 0
		s.LastError = ""
		return nil
	})
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return err
	}
	if sl.PID != 0 {
		if err := m.d.Runner.Stop(ctx, sl.PID); err != nil {
			return err
		}
	}
	removed, err := m.d.Registry.Delete(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(removed.ConfigPath))
}

func (m *Manager) start(ctx context.Context, sl Slave) (Slave, error) {
	if m.d.SlaveExe == "" {
		return Slave{}, fmt.Errorf("slave-agent.exe path required")
	}
	if _, err := os.Stat(m.d.SlaveExe); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Slave{}, err
	}
	res, err := m.d.Runner.Start(ctx, StartRequest{
		Exe:        m.d.SlaveExe,
		ConfigPath: sl.ConfigPath,
		LogPath:    sl.LogPath,
		WorkDir:    filepath.Dir(sl.ConfigPath),
	})
	if err != nil {
		_, _ = m.d.Registry.Update(sl.ID, func(s *Slave) error {
			s.Status = StatusError
			s.LastError = err.Error()
			return nil
		})
		return Slave{}, err
	}
	status := StatusStarting
	if res.AuthURL != "" {
		status = StatusAuthRequired
	}
	return m.d.Registry.Update(sl.ID, func(s *Slave) error {
		s.Status = status
		s.PID = res.PID
		s.AuthURL = res.AuthURL
		s.LastError = ""
		return nil
	})
}

type execRunner struct{}

func (execRunner) Start(ctx context.Context, req StartRequest) (StartResult, error) {
	if err := os.MkdirAll(filepath.Dir(req.LogPath), 0o755); err != nil {
		return StartResult{}, err
	}
	logFile, err := os.OpenFile(req.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return StartResult{}, err
	}
	cmd := exec.CommandContext(ctx, req.Exe, req.ConfigPath)
	cmd.Dir = req.WorkDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logFile.Close()
		return StartResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		logFile.Close()
		return StartResult{}, err
	}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return StartResult{}, err
	}
	authCh := make(chan string, 1)
	go copyAndDetectURL(io.MultiReader(stdout, stderr), logFile, authCh)
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case url := <-authCh:
		return StartResult{PID: cmd.Process.Pid, AuthURL: url}, nil
	case <-timer.C:
		return StartResult{PID: cmd.Process.Pid}, nil
	}
}

func (execRunner) Stop(_ context.Context, pid int) error {
	if pid == 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

var authURLPattern = regexp.MustCompile(`https?://\S+`)

func copyAndDetectURL(r io.Reader, w io.Writer, authCh chan<- string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(w, line)
		if authURLPattern.MatchString(line) {
			select {
			case authCh <- authURLPattern.FindString(line):
			default:
			}
		}
	}
}
```

- [ ] **Step 5: Run process tests**

Run: `go test ./internal/slave -run 'Manager|Registry|WriteConfig|Machine' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/slave/process.go internal/slave/process_test.go internal/slave/registry.go
git commit -m "feat: manage local slave processes"
```

---

### Task 5: Console Controller and HTTP API

**Files:**
- Modify: `internal/console/state.go`
- Modify: `internal/console/state_test.go`
- Modify: `internal/ui/console.go`
- Modify: `internal/ui/server.go`
- Modify: `internal/ui/server_test.go`
- Modify: `cmd/launcher/main.go`

- [ ] **Step 1: Write failing console controller tests**

Append to `internal/console/state_test.go`:

```go
func TestControllerListsAndControlsSlaves(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	manager := newTestSlaveManager(t, dir, "61414-PC")
	c := NewController(Deps{State: state.NewStore(filepath.Join(dir, "state.json")), Secrets: newTestSecrets(), Slaves: manager})

	created, err := c.CreateSlave(context.Background(), consoleCreateSlaveInput(folder, "worker"))
	if err != nil {
		t.Fatalf("CreateSlave: %v", err)
	}
	if created.DisplayName != "61414-PC-worker" {
		t.Fatalf("created=%+v", created)
	}

	machine, slaves, err := c.Slaves(context.Background())
	if err != nil {
		t.Fatalf("Slaves: %v", err)
	}
	if machine.ComputerName != "61414-PC" || len(slaves) != 1 {
		t.Fatalf("machine=%+v slaves=%+v", machine, slaves)
	}

	if _, err := c.PauseSlave(context.Background(), created.ID); err != nil {
		t.Fatalf("PauseSlave: %v", err)
	}
	if _, err := c.RestartSlave(context.Background(), created.ID); err != nil {
		t.Fatalf("RestartSlave: %v", err)
	}
	if err := c.DeleteSlave(context.Background(), created.ID); err != nil {
		t.Fatalf("DeleteSlave: %v", err)
	}
}

func newTestSlaveManager(t *testing.T, dir, computerName string) *slave.Manager {
	t.Helper()
	ms := slave.NewMachineStore(filepath.Join(dir, "machine.json"))
	if _, err := ms.Ensure(computerName); err != nil {
		t.Fatal(err)
	}
	return slave.NewManager(slave.ManagerDeps{
		Machines: ms,
		Registry: slave.NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:   &consoleFakeSlaveRunner{pid: 1234},
		SlaveExe: filepath.Join(dir, "slave-agent.exe"),
		CodexBin: "codex",
	})
}

func consoleCreateSlaveInput(folder, name string) slave.CreateInput {
	return slave.CreateInput{Folder: folder, Name: name}
}

type consoleFakeSlaveRunner struct{ pid int }

func (r *consoleFakeSlaveRunner) Start(context.Context, slave.StartRequest) (slave.StartResult, error) {
	return slave.StartResult{PID: r.pid}, nil
}

func (r *consoleFakeSlaveRunner) Stop(context.Context, int) error { return nil }
```

Add imports to `internal/console/state_test.go`:

```go
	"os"
	"github.com/agentserver/agentserver-pkg/internal/slave"
```

- [ ] **Step 2: Run controller test and verify it fails**

Run: `go test ./internal/console -run TestControllerListsAndControlsSlaves -count=1`

Expected: FAIL because `Deps.Slaves`, `CreateSlave`, `PauseSlave`, `RestartSlave`, `DeleteSlave`, and `Slaves` are undefined.

- [ ] **Step 3: Extend console controller**

Modify `internal/console/state.go`:

Add import:

```go
	"github.com/agentserver/agentserver-pkg/internal/slave"
```

Add to `Deps`:

```go
	Slaves                *slave.Manager
```

Add methods near existing action methods:

```go
func (c *Controller) Slaves(ctx context.Context) (slave.Machine, []slave.Slave, error) {
	if c.d.Slaves == nil {
		return slave.Machine{}, nil, errors.New("console: slave manager unavailable")
	}
	return c.d.Slaves.List(ctx)
}

func (c *Controller) CreateSlave(ctx context.Context, in slave.CreateInput) (slave.Slave, error) {
	if c.d.Slaves == nil {
		return slave.Slave{}, errors.New("console: slave manager unavailable")
	}
	return c.d.Slaves.CreateAndStart(ctx, in)
}

func (c *Controller) RestartSlave(ctx context.Context, id string) (slave.Slave, error) {
	if c.d.Slaves == nil {
		return slave.Slave{}, errors.New("console: slave manager unavailable")
	}
	return c.d.Slaves.Restart(ctx, id)
}

func (c *Controller) PauseSlave(ctx context.Context, id string) (slave.Slave, error) {
	if c.d.Slaves == nil {
		return slave.Slave{}, errors.New("console: slave manager unavailable")
	}
	return c.d.Slaves.Pause(ctx, id)
}

func (c *Controller) DeleteSlave(ctx context.Context, id string) error {
	if c.d.Slaves == nil {
		return errors.New("console: slave manager unavailable")
	}
	return c.d.Slaves.Delete(ctx, id)
}
```

- [ ] **Step 4: Run controller tests**

Run: `go test ./internal/console -run 'TestControllerListsAndControlsSlaves|TestControllerActionsInvokeCallbacks' -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing HTTP API tests**

Append to `internal/ui/server_test.go`:

```go
func TestServerConsoleSlaveEndpoints(t *testing.T) {
	cc := &fakeConsoleController{
		machine: consoleSlaveMachine("61414-PC"),
		slaves: []slave.Slave{{ID: "sl-1", DisplayName: "61414-PC-worker", Status: slave.StatusRunning}},
	}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/console/slaves")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if list["machine"] == nil || list["slaves"] == nil {
		t.Fatalf("list body=%+v", list)
	}

	resp, err = http.Post(srv.URL+"/api/console/slaves", "application/json", strings.NewReader(`{"folder":"C:\\repo","name":"worker"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !cc.createdSlave {
		t.Fatalf("create status=%d created=%v", resp.StatusCode, cc.createdSlave)
	}

	for _, path := range []string{
		"/api/console/slaves/sl-1/restart",
		"/api/console/slaves/sl-1/pause",
	} {
		resp, err := http.Post(srv.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s status=%d", path, resp.StatusCode)
		}
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/console/slaves/sl-1", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !cc.deletedSlave {
		t.Fatalf("delete status=%d deleted=%v", resp.StatusCode, cc.deletedSlave)
	}
}

func consoleSlaveMachine(name string) slave.Machine {
	return slave.Machine{MachineID: "machine-1", ComputerName: name}
}
```

Add import to `internal/ui/server_test.go`:

```go
	"github.com/agentserver/agentserver-pkg/internal/slave"
```

- [ ] **Step 6: Extend UI console interface and fake**

Modify `internal/ui/console.go`:

Add import:

```go
	"github.com/agentserver/agentserver-pkg/internal/slave"
```

Extend `ConsoleController`:

```go
	Slaves(context.Context) (slave.Machine, []slave.Slave, error)
	CreateSlave(context.Context, slave.CreateInput) (slave.Slave, error)
	RestartSlave(context.Context, string) (slave.Slave, error)
	PauseSlave(context.Context, string) (slave.Slave, error)
	DeleteSlave(context.Context, string) error
```

Add noop methods:

```go
func (noopConsoleController) Slaves(context.Context) (slave.Machine, []slave.Slave, error) {
	return slave.Machine{}, nil, nil
}
func (noopConsoleController) CreateSlave(context.Context, slave.CreateInput) (slave.Slave, error) {
	return slave.Slave{}, nil
}
func (noopConsoleController) RestartSlave(context.Context, string) (slave.Slave, error) {
	return slave.Slave{}, nil
}
func (noopConsoleController) PauseSlave(context.Context, string) (slave.Slave, error) {
	return slave.Slave{}, nil
}
func (noopConsoleController) DeleteSlave(context.Context, string) error { return nil }
```

Modify `fakeConsoleController` in `internal/ui/server_test.go` by adding fields:

```go
	machine      slave.Machine
	slaves       []slave.Slave
	createdSlave bool
	deletedSlave bool
```

Add methods:

```go
func (f *fakeConsoleController) Slaves(context.Context) (slave.Machine, []slave.Slave, error) {
	return f.machine, f.slaves, nil
}
func (f *fakeConsoleController) CreateSlave(_ context.Context, in slave.CreateInput) (slave.Slave, error) {
	f.createdSlave = true
	return slave.Slave{ID: "created", Name: in.Name, Folder: in.Folder, DisplayName: "61414-PC-" + in.Name}, nil
}
func (f *fakeConsoleController) RestartSlave(context.Context, string) (slave.Slave, error) {
	return slave.Slave{ID: "sl-1", Status: slave.StatusStarting}, nil
}
func (f *fakeConsoleController) PauseSlave(context.Context, string) (slave.Slave, error) {
	return slave.Slave{ID: "sl-1", Status: slave.StatusPaused}, nil
}
func (f *fakeConsoleController) DeleteSlave(context.Context, string) error {
	f.deletedSlave = true
	return nil
}
```

- [ ] **Step 7: Add HTTP handlers**

Modify `internal/ui/server.go`:

Add imports:

```go
	"strings"
	"github.com/agentserver/agentserver-pkg/internal/slave"
```

Register routes in `NewServerWithConsole`:

```go
	mux.HandleFunc("/api/console/slaves", s.handleConsoleSlaves)
	mux.HandleFunc("/api/console/slaves/", s.handleConsoleSlaveAction)
```

Add handlers:

```go
func (s *server) handleConsoleSlaves(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		machine, slaves, err := s.c.Slaves(r.Context())
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"machine": machine, "slaves": slaves})
	case http.MethodPost:
		var in slave.CreateInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		sl, err := s.c.CreateSlave(r.Context(), in)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, sl)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *server) handleConsoleSlaveAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/console/slaves/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, fmt.Errorf("slave id required"))
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodDelete {
		if err := s.c.DeleteSlave(r.Context(), id); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]string{"state": "deleted"})
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	switch parts[1] {
	case "restart":
		sl, err := s.c.RestartSlave(r.Context(), id)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, sl)
	case "pause":
		sl, err := s.c.PauseSlave(r.Context(), id)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, sl)
	default:
		writeErr(w, http.StatusNotFound, fmt.Errorf("unknown slave action"))
	}
}
```

Also add `fmt` to server imports because the handler uses `fmt.Errorf`.

- [ ] **Step 8: Wire manager in launcher**

Modify `cmd/launcher/main.go` imports:

```go
	"github.com/agentserver/agentserver-pkg/internal/slave"
```

In `serveCompletedConsole`, before `ctrl := console.NewController(...)`, add:

```go
	machineStore := slave.NewMachineStore(in.Paths.MachineFile)
	_, _ = machineStore.Ensure(os.Getenv("COMPUTERNAME"))
	slaveManager := slave.NewManager(slave.ManagerDeps{
		Machines:  machineStore,
		Registry:  slave.NewRegistry(in.Paths.SlavesFile, in.Paths.SlavesDir),
		SlaveExe:  joinExe(in.InstallDir, "slave-agent.exe"),
		ServerURL: "https://agent.cs.ac.cn",
		CodexBin:  in.Paths.CodexExePath,
	})
```

Add to `console.Deps`:

```go
		Slaves:                slaveManager,
```

- [ ] **Step 9: Run server/controller tests**

Run: `go test ./internal/console ./internal/ui ./cmd/launcher -run 'Slave|ConsoleSlave|Completed|Controller' -count=1`

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/console/state.go internal/console/state_test.go internal/ui/console.go internal/ui/server.go internal/ui/server_test.go cmd/launcher/main.go
git commit -m "feat: expose local slave console api"
```

---

### Task 6: Frontend API and Dashboard UI

**Files:**
- Modify: `internal/ui/web/src/api.ts`
- Modify: `internal/ui/web/src/__tests__/api.spec.ts`
- Modify: `internal/ui/web/src/components/Dashboard.vue`
- Modify: `internal/ui/web/src/__tests__/Dashboard.spec.ts`

- [ ] **Step 1: Write failing API tests**

Append to `internal/ui/web/src/__tests__/api.spec.ts`:

```ts
  it('getConsoleSlaves returns machine and slaves', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        machine: { machine_id: 'machine-1', computer_name: '61414-PC' },
        slaves: [{ id: 'sl-1', display_name: '61414-PC-worker', status: 'running' }],
      }),
    } as Response);
    const s = await api.getConsoleSlaves();
    expect(s.machine.computer_name).toBe('61414-PC');
    expect(s.slaves[0].display_name).toBe('61414-PC-worker');
  });

  it('createConsoleSlave POSTs folder and name', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ id: 'sl-1', status: 'auth_required' }),
    } as Response);
    await api.createConsoleSlave({ folder: 'C:\\repo', name: 'worker' });
    expect(fetchSpy).toHaveBeenCalledWith('/api/console/slaves', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ folder: 'C:\\repo', name: 'worker' }),
    }));
  });

  it('controls a console slave', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ id: 'sl-1', status: 'paused' }),
    } as Response);
    await api.restartConsoleSlave('sl-1');
    await api.pauseConsoleSlave('sl-1');
    await api.deleteConsoleSlave('sl-1');
    expect(fetchSpy).toHaveBeenCalledWith('/api/console/slaves/sl-1/restart', expect.objectContaining({ method: 'POST' }));
    expect(fetchSpy).toHaveBeenCalledWith('/api/console/slaves/sl-1/pause', expect.objectContaining({ method: 'POST' }));
    expect(fetchSpy).toHaveBeenCalledWith('/api/console/slaves/sl-1', expect.objectContaining({ method: 'DELETE' }));
  });
```

- [ ] **Step 2: Run API tests and verify failure**

Run: `cd internal/ui/web && npm test -- api.spec.ts`

Expected: FAIL because slave API exports are undefined.

- [ ] **Step 3: Implement frontend API types and calls**

Modify `internal/ui/web/src/api.ts`:

```ts
export interface ConsoleMachine {
  machine_id: string;
  computer_name: string;
}

export type ConsoleSlaveStatus = 'stopped' | 'starting' | 'auth_required' | 'running' | 'paused' | 'error';

export interface ConsoleSlave {
  id: string;
  name: string;
  display_name: string;
  folder: string;
  status: ConsoleSlaveStatus;
  pid?: number;
  auth_url?: string;
  last_error?: string;
  created_at?: string;
  updated_at?: string;
}

export interface ConsoleSlavesResponse {
  machine: ConsoleMachine;
  slaves: ConsoleSlave[];
}

export interface CreateConsoleSlaveInput {
  folder: string;
  name?: string;
}
```

Add functions near other console functions:

```ts
export const getConsoleSlaves = () =>
  request<ConsoleSlavesResponse>('/api/console/slaves');

export const createConsoleSlave = (input: CreateConsoleSlaveInput) =>
  request<ConsoleSlave>('/api/console/slaves', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });

export const restartConsoleSlave = (id: string) =>
  request<ConsoleSlave>(`/api/console/slaves/${encodeURIComponent(id)}/restart`, { method: 'POST' });

export const pauseConsoleSlave = (id: string) =>
  request<ConsoleSlave>(`/api/console/slaves/${encodeURIComponent(id)}/pause`, { method: 'POST' });

export const deleteConsoleSlave = (id: string) =>
  request<{ state: 'deleted' }>(`/api/console/slaves/${encodeURIComponent(id)}`, { method: 'DELETE' });
```

- [ ] **Step 4: Run API tests**

Run: `cd internal/ui/web && npm test -- api.spec.ts`

Expected: PASS.

- [ ] **Step 5: Write failing Dashboard tests**

Append to `internal/ui/web/src/__tests__/Dashboard.spec.ts`:

```ts
  it('renders local machine slave group and auth link', async () => {
    mockConsoleState();
    vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue({
      machine: { machine_id: 'machine-1', computer_name: '61414-PC' },
      slaves: [{
        id: 'sl-1',
        name: 'worker',
        display_name: '61414-PC-worker',
        folder: 'C:\\repo',
        status: 'auth_required',
        auth_url: 'https://agent.cs.ac.cn/device?user_code=ABCD',
      }],
    });
    const w = mount(Dashboard);
    await flushPromises();
    expect(w.text()).toContain('这台电脑上的 agent');
    expect(w.text()).toContain('本机：61414-PC');
    expect(w.text()).toContain('61414-PC-worker');
    expect(w.text()).toContain('C:\\repo');
    expect(w.find('a[href="https://agent.cs.ac.cn/device?user_code=ABCD"]').exists()).toBe(true);
  });

  it('creates a slave with selected folder and custom name', async () => {
    mockConsoleState();
    vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue({
      machine: { machine_id: 'machine-1', computer_name: '61414-PC' },
      slaves: [],
    });
    const createSpy = vi.spyOn(api, 'createConsoleSlave').mockResolvedValue({
      id: 'sl-1',
      name: '前端调试',
      display_name: '61414-PC-前端调试',
      folder: 'C:\\repo',
      status: 'starting',
    });
    const refreshSpy = vi.spyOn(api, 'getConsoleSlaves').mockResolvedValueOnce({
      machine: { machine_id: 'machine-1', computer_name: '61414-PC' },
      slaves: [],
    }).mockResolvedValueOnce({
      machine: { machine_id: 'machine-1', computer_name: '61414-PC' },
      slaves: [{ id: 'sl-1', name: '前端调试', display_name: '61414-PC-前端调试', folder: 'C:\\repo', status: 'starting' }],
    });

    const w = mount(Dashboard);
    await flushPromises();
    await w.find('[data-test="slave-folder-input"] input').setValue('C:\\repo');
    await w.find('[data-test="slave-name-input"] input').setValue('前端调试');
    await w.find('[data-test="create-slave"]').trigger('click');
    await flushPromises();

    expect(createSpy).toHaveBeenCalledWith({ folder: 'C:\\repo', name: '前端调试' });
    expect(refreshSpy).toHaveBeenCalledTimes(2);
  });

  it('blocks slave names longer than twenty characters', async () => {
    mockConsoleState();
    vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue({
      machine: { machine_id: 'machine-1', computer_name: '61414-PC' },
      slaves: [],
    });
    const createSpy = vi.spyOn(api, 'createConsoleSlave').mockResolvedValue({} as api.ConsoleSlave);
    const w = mount(Dashboard);
    await flushPromises();
    await w.find('[data-test="slave-folder-input"] input').setValue('C:\\repo');
    await w.find('[data-test="slave-name-input"] input').setValue('123456789012345678901');
    await w.find('[data-test="create-slave"]').trigger('click');
    await flushPromises();
    expect(createSpy).not.toHaveBeenCalled();
    expect(w.text()).toContain('slave 名最多 20 个字符');
  });
```

- [ ] **Step 6: Run Dashboard tests and verify failure**

Run: `cd internal/ui/web && npm test -- Dashboard.spec.ts`

Expected: FAIL because Dashboard does not load or render slaves.

- [ ] **Step 7: Implement Dashboard slave state and actions**

Modify `Dashboard.vue` script section. Add refs:

```ts
const slaveMachine = ref<api.ConsoleMachine | null>(null);
const slaves = ref<api.ConsoleSlave[]>([]);
const slaveFolder = ref('');
const slaveName = ref('');
const slaveError = ref('');
const creatingSlave = ref(false);
const slaveBusy = ref<Record<string, boolean>>({});
```

Add `slaveError` to `visibleErrors`:

```ts
  { key: 'slave', message: slaveError.value },
```

Add computed:

```ts
const slaveDisplayPreview = computed(() => {
  const machine = slaveMachine.value?.computer_name || '本机';
  const name = normalizedSlaveName();
  return `${machine}-${name || '文件夹名'}`;
});
```

Add functions:

```ts
function normalizedSlaveName() {
  const explicit = slaveName.value.trim();
  if (explicit) return explicit;
  const normalized = slaveFolder.value.replace(/\\/g, '/').replace(/\/+$/, '');
  return normalized.split('/').pop() || '';
}

async function loadSlaves() {
  try {
    const res = await api.getConsoleSlaves();
    slaveMachine.value = res.machine;
    slaves.value = res.slaves;
    slaveError.value = '';
  } catch (e) {
    slaveError.value = errorMessage(e);
  }
}

async function createSlave() {
  if (creatingSlave.value) return;
  const folder = slaveFolder.value.trim();
  const name = normalizedSlaveName();
  if (!folder) {
    slaveError.value = '请选择 slave 文件夹';
    return;
  }
  if ([...name].length > 20) {
    slaveError.value = 'slave 名最多 20 个字符';
    return;
  }
  creatingSlave.value = true;
  try {
    await api.createConsoleSlave({ folder, name });
    slaveFolder.value = '';
    slaveName.value = '';
    await loadSlaves();
  } catch (e) {
    slaveError.value = errorMessage(e);
  } finally {
    creatingSlave.value = false;
  }
}

async function restartSlave(id: string) {
  await runSlaveAction(id, () => api.restartConsoleSlave(id));
}

async function pauseSlave(id: string) {
  await runSlaveAction(id, () => api.pauseConsoleSlave(id));
}

async function deleteSlave(id: string) {
  if (!window.confirm('删除后会停止该 slave 并删除本地配置。确定删除吗？')) return;
  await runSlaveAction(id, async () => {
    await api.deleteConsoleSlave(id);
    return undefined;
  });
}

async function runSlaveAction(id: string, action: () => Promise<unknown>) {
  if (slaveBusy.value[id]) return;
  slaveBusy.value = { ...slaveBusy.value, [id]: true };
  try {
    await action();
    await loadSlaves();
  } catch (e) {
    slaveError.value = errorMessage(e);
  } finally {
    slaveBusy.value = { ...slaveBusy.value, [id]: false };
  }
}

function slaveStatusLabel(status: api.ConsoleSlaveStatus) {
  const labels: Record<api.ConsoleSlaveStatus, string> = {
    stopped: '已停止',
    starting: '启动中',
    auth_required: '待认证',
    running: '运行中',
    paused: '已暂停',
    error: '出错',
  };
  return labels[status] || status;
}
```

Change `onMounted(load);` to:

```ts
onMounted(async () => {
  await load();
  await loadSlaves();
});
```

- [ ] **Step 8: Add Dashboard markup**

Insert before subscription actions in `Dashboard.vue` template:

```vue
    <section class="slave-panel">
      <div class="section-head">
        <div>
          <h2>这台电脑上的 agent</h2>
          <p>本机：{{ slaveMachine?.computer_name || '未初始化' }}</p>
        </div>
      </div>

      <div class="slave-create">
        <el-input
          v-model="slaveFolder"
          data-test="slave-folder-input"
          placeholder="选择或粘贴文件夹路径"
        />
        <el-input
          v-model="slaveName"
          data-test="slave-name-input"
          maxlength="20"
          show-word-limit
          placeholder="slave 名，默认使用文件夹名"
        />
        <span class="slave-preview">{{ slaveDisplayPreview }}</span>
        <el-button
          data-test="create-slave"
          type="primary"
          :loading="creatingSlave"
          :disabled="creatingSlave"
          @click="createSlave"
        >
          创建并启动
        </el-button>
      </div>

      <div class="slave-list">
        <div v-for="sl in slaves" :key="sl.id" class="slave-row">
          <div class="slave-main">
            <strong>{{ sl.display_name }}</strong>
            <span>{{ sl.folder }}</span>
            <small>{{ slaveStatusLabel(sl.status) }}</small>
            <a v-if="sl.auth_url" :href="sl.auth_url" target="_blank" rel="noopener noreferrer">完成认证</a>
            <em v-if="sl.last_error">{{ sl.last_error }}</em>
          </div>
          <div class="slave-actions">
            <el-button :loading="slaveBusy[sl.id]" @click="restartSlave(sl.id)">启动/重启</el-button>
            <el-button :loading="slaveBusy[sl.id]" @click="pauseSlave(sl.id)">暂停</el-button>
            <el-button type="danger" plain :loading="slaveBusy[sl.id]" @click="deleteSlave(sl.id)">删除</el-button>
          </div>
        </div>
      </div>
    </section>
```

- [ ] **Step 9: Add Dashboard CSS**

Append inside `<style scoped>`:

```css
.slave-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.section-head h2 {
  margin: 0 0 4px;
  font-size: 16px;
}

.section-head p {
  margin: 0;
  color: #606266;
  font-size: 13px;
}

.slave-create {
  display: grid;
  grid-template-columns: minmax(180px, 1.4fr) minmax(160px, 1fr) minmax(140px, auto) auto;
  gap: 8px;
  align-items: center;
}

.slave-preview {
  min-width: 0;
  overflow-wrap: anywhere;
  color: #606266;
  font-size: 13px;
}

.slave-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.slave-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 12px;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  background: #fff;
}

.slave-main {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.slave-main span,
.slave-main small,
.slave-main em,
.slave-main a {
  overflow-wrap: anywhere;
  font-size: 13px;
}

.slave-main span,
.slave-main small {
  color: #606266;
}

.slave-main em {
  color: #c45656;
  font-style: normal;
}

.slave-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.slave-actions :deep(.el-button) {
  margin-left: 0;
}

@media (max-width: 760px) {
  .slave-create {
    grid-template-columns: 1fr;
  }

  .slave-row {
    flex-direction: column;
  }

  .slave-actions {
    justify-content: flex-start;
  }
}
```

- [ ] **Step 10: Run frontend tests**

Run: `cd internal/ui/web && npm test -- api.spec.ts Dashboard.spec.ts`

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/ui/web/src/api.ts internal/ui/web/src/__tests__/api.spec.ts internal/ui/web/src/components/Dashboard.vue internal/ui/web/src/__tests__/Dashboard.spec.ts
git commit -m "feat: add slave controls to dashboard"
```

---

### Task 7: Installer Machine Initialization and Packaging

**Files:**
- Create: `packaging/windows/machine.ps1`
- Modify: `packaging/windows/install.ps1`
- Modify: `packaging/windows/installer.iss`
- Modify: `scripts/package-windows.sh`
- Modify: `scripts/package-windows-zip.sh`
- Modify: `internal/vscode/install_test.go`

- [ ] **Step 1: Write failing packaging tests**

Modify `internal/vscode/install_test.go` in `TestWindowsInstallScriptsIncludeVSCodeInstaller`:

Add `"machine.ps1"` and `"machine.json"` to the `install.ps1` expected strings.

Add `"packaging/windows/machine.ps1"` and `"machine.ps1"` to both `package-windows-zip.sh` and `package-windows.sh` expected strings.

Add `"machine.ps1"`, `"ComputerNamePage"`, and `"COMPUTERNAME"` to `installer.iss` expected strings.

Add `../../packaging/windows/machine.ps1` to `TestWindowsPowerShellScriptsUseUTF8BOM`.

- [ ] **Step 2: Run packaging tests and verify failure**

Run: `go test ./internal/vscode -run 'TestWindowsInstallScriptsIncludeVSCodeInstaller|TestWindowsPowerShellScriptsUseUTF8BOM' -count=1`

Expected: FAIL because `machine.ps1` is missing from scripts and BOM test.

- [ ] **Step 3: Create machine initializer PowerShell**

Create `packaging/windows/machine.ps1` with UTF-8 BOM:

```powershell
param(
    [string]$MachinePath = (Join-Path $env:USERPROFILE '.agentserver-vscode\machine.json'),
    [string]$ComputerName = $env:COMPUTERNAME
)

$ErrorActionPreference = 'Stop'

function New-MachineId {
    return [guid]::NewGuid().ToString()
}

$name = ($ComputerName | ForEach-Object { "$_".Trim() })
if ([string]::IsNullOrWhiteSpace($name)) {
    throw 'ComputerName is required'
}

if (Test-Path $MachinePath) {
    Write-Host "machine.json already exists; keeping existing computer name."
    exit 0
}

$dir = Split-Path -Parent $MachinePath
if (-not (Test-Path $dir)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}

$payload = [ordered]@{
    machine_id = New-MachineId
    computer_name = $name
}
$json = $payload | ConvertTo-Json
$utf8 = New-Object System.Text.UTF8Encoding $false
[System.IO.File]::WriteAllText($MachinePath, $json + [Environment]::NewLine, $utf8)
Write-Host "Initialized machine identity: $name"
```

Use an editor or script that preserves BOM. If using `apply_patch`, run this after creation:

```bash
python3 - <<'PY'
from pathlib import Path
p = Path('packaging/windows/machine.ps1')
b = p.read_bytes()
if not b.startswith(b'\xef\xbb\xbf'):
    p.write_bytes(b'\xef\xbb\xbf' + b)
PY
```

- [ ] **Step 4: Wire portable installer**

Modify `packaging/windows/install.ps1`:

Add `'machine.ps1'` to `$required`.

After `$srcDir` and required-copy setup, before frontend install, add:

```powershell
$MachinePath = Join-Path $env:USERPROFILE '.agentserver-vscode\machine.json'
$InitialComputerName = $env:COMPUTERNAME
if (-not $Silent -and -not (Test-Path $MachinePath)) {
    $entered = Read-Host "请输入这台电脑在 agent 列表中的名称（默认：$InitialComputerName）"
    if (-not [string]::IsNullOrWhiteSpace($entered)) {
        $InitialComputerName = $entered.Trim()
    }
}
Write-Step "Initializing local computer name..."
& (Join-Path $InstallDir 'machine.ps1') -MachinePath $MachinePath -ComputerName $InitialComputerName
```

- [ ] **Step 5: Wire Inno installer**

Modify `packaging/windows/installer.iss`:

Add file:

```iss
Source: "machine.ps1"; DestDir: "{app}"; Flags: ignoreversion
```

Add code variables near `[Code]` top:

```pascal
var
  ComputerNamePage: TInputQueryWizardPage;
```

Add `InitializeWizard`:

```pascal
procedure InitializeWizard;
begin
  ComputerNamePage := CreateInputQueryPage(wpSelectTasks,
    '确认本机名称',
    '这个名称会用于这台电脑上的 agent，创建后不可修改。',
    '请确认本机名称。');
  ComputerNamePage.Add('本机名称:', False);
  ComputerNamePage.Values[0] := GetEnv('COMPUTERNAME');
end;
```

In `CurStepChanged`, before mode setup, add:

```pascal
  RunEstimatedPowerShellStep('machine-init', '正在初始化本机名称...', 'machine.ps1',
    '-MachinePath ' + PowerShellQuote(ExpandConstant('{userprofile}\.agentserver-vscode\machine.json')) +
    ' -ComputerName ' + PowerShellQuote(ComputerNamePage.Values[0]), 5);
```

If silent install skips page value initialization, `ComputerNamePage.Values[0]` still holds `GetEnv('COMPUTERNAME')`.

- [ ] **Step 6: Bundle machine.ps1 in scripts**

Modify `scripts/package-windows.sh` preflight list:

```bash
         packaging/windows/machine.ps1 \
```

Modify `scripts/package-windows-zip.sh` preflight list and copy section:

```bash
         packaging/windows/machine.ps1 \
```

```bash
cp packaging/windows/machine.ps1 "$STAGE/"
```

- [ ] **Step 7: Run packaging tests**

Run: `go test ./internal/vscode -run 'TestWindowsInstallScriptsIncludeVSCodeInstaller|TestWindowsPowerShellScriptsUseUTF8BOM' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add packaging/windows/machine.ps1 packaging/windows/install.ps1 packaging/windows/installer.iss scripts/package-windows.sh scripts/package-windows-zip.sh internal/vscode/install_test.go
git commit -m "feat: initialize local computer name during install"
```

---

### Task 8: Full Verification

**Files:**
- No source edits expected.

- [ ] **Step 1: Run Go tests**

Run: `go test ./... -count=1`

Expected: PASS for all Go packages.

- [ ] **Step 2: Run frontend tests**

Run: `cd internal/ui/web && npm test`

Expected: PASS for all Vitest suites.

- [ ] **Step 3: Build frontend assets**

Run: `make ui-build`

Expected: command exits 0 and updates `internal/ui/assets/dist`.

- [ ] **Step 4: Run shell syntax checks**

Run:

```bash
bash -n scripts/package-windows.sh
bash -n scripts/package-windows-zip.sh
```

Expected: both commands exit 0.

- [ ] **Step 5: Build portable package**

Run: `bash scripts/package-windows-zip.sh`

Expected: `dist/agentserver-vscode-0.1.0-portable.zip` is created and contains:

```text
agentserver-vscode-0.1.0-portable/slave-agent.exe
agentserver-vscode-0.1.0-portable/machine.ps1
```

Verify with:

```bash
unzip -l dist/agentserver-vscode-0.1.0-portable.zip | rg 'slave-agent\.exe|machine\.ps1'
```

- [ ] **Step 6: Build Inno package when Inno is available**

Run: `bash scripts/package-windows.sh`

Expected if Inno/Wine is present: `packaging/windows/Output/agentserver-vscode-0.1.0-setup.exe` is created.

Expected if Inno/Wine is absent: script exits with its documented "Inno Setup not found" message after all preflight payload checks.

- [ ] **Step 7: Review diff**

Run:

```bash
git status --short
git diff --stat
```

Expected: only intentional source and generated asset changes remain.

- [ ] **Step 8: Final commit for generated UI assets**

Run `git status --short internal/ui/assets/dist`.

Expected when generated assets changed: output lists paths under `internal/ui/assets/dist`; commit them with:

```bash
git add internal/ui/assets/dist
git commit -m "build: refresh embedded console assets"
```

Expected when generated assets did not change: no output; do not create this commit.

---

## Self-Review

Spec coverage:

- Multiple local slaves: Tasks 2, 4, 5, and 6.
- Start/restart, pause, delete: Tasks 4, 5, and 6.
- Folder-selected slave creation: Tasks 2, 5, and 6.
- Per-slave reauthentication: Tasks 3 and 4 keep credentials empty and capture auth URL.
- Immutable install-time computer name: Tasks 1 and 7.
- Immutable create-time slave name, default folder name, 20-character limit: Task 2 and Task 6.
- Agentserver card source marking via real loom fields: Task 3 writes `discovery.display_name`, `discovery.description`, and `resources.tags`.
- Control console display of `本机：<电脑名>` and `这台电脑上的 agent`: Task 6.
- Packaging `slave-agent.exe`: already present from the prior package update; Task 8 verifies it, and Task 7 adds `machine.ps1`.

Placeholder scan:

- No red-flag placeholder terms or unnamed validation steps are present.
- Each code-changing task includes concrete files, code snippets, commands, and expected outcomes.

Type consistency:

- Backend types use `slave.Machine`, `slave.Slave`, `slave.CreateInput`.
- Frontend JSON uses snake_case fields matching Go struct tags.
- HTTP routes are consistent across server, API client, and tests.
