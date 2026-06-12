# Auto Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add user-confirmed manual and automatic application updates from `assets.agent.cs.ac.cn`, preserving login/state data and restarting eligible local slaves after upgrade.

**Architecture:** Add a focused `internal/updater` package for manifest validation, state, download, hash verification, and installer launch. Wire it through completed-console controller APIs, Dashboard controls, and launcher startup hooks for automatic checks and pending slave restarts.

**Tech Stack:** Go standard library HTTP/filesystem/exec, existing `internal/download`, existing `internal/slave`, existing `internal/ui` HTTP server, Vue 3 + Element Plus frontend tests.

---

## File Structure

- Create `internal/appversion/version.go`: central Go runtime current app version.
- Create `internal/appversion/version_test.go`: verifies version is non-empty and SemVer-like.
- Modify `internal/paths/paths.go`: add update cache/state paths and pending slave restart path.
- Modify `internal/paths/paths_test.go`: verify new paths live under `~/.agentserver-app`.
- Create `internal/updater/manifest.go`: manifest type, validation, URL allowlist.
- Create `internal/updater/version.go`: small SemVer comparator used by update checks.
- Create `internal/updater/state.go`: persisted update state store.
- Create `internal/updater/service.go`: check/download/install orchestration.
- Create `internal/updater/installer_windows.go`: Windows installer process start.
- Create `internal/updater/installer_other.go`: non-Windows unsupported error.
- Create `internal/updater/*_test.go`: unit tests for manifest, version, state, download, installer start injection.
- Create `internal/console/update.go`: console-level update methods and pending slave restart recording.
- Create `internal/console/update_test.go`: verifies update checks and slave restart selection.
- Modify `internal/console/state.go`: extend `Deps` with updater dependency and expose controller methods if not kept fully in `update.go`.
- Modify `internal/ui/console.go`: add update methods to `ConsoleController` and noop implementation.
- Modify `internal/ui/server.go`: add `/api/console/update` endpoints.
- Modify `internal/ui/server_test.go`: endpoint tests and trusted mutation coverage.
- Modify `cmd/launcher/main.go`: construct updater service, run delayed auto-check, restore pending slaves on startup.
- Modify `cmd/launcher/main_test.go`: wiring tests for updater configuration and pending restore behavior.
- Modify `internal/ui/web/src/api.ts`: update state types and functions.
- Modify `internal/ui/web/src/components/Dashboard.vue`: render update status and install confirmation.
- Modify `internal/ui/web/src/__tests__/Dashboard.spec.ts`: frontend behavior tests.

---

### Task 1: Central App Version and Update Paths

**Files:**
- Create: `internal/appversion/version.go`
- Create: `internal/appversion/version_test.go`
- Modify: `internal/paths/paths.go`
- Modify: `internal/paths/paths_test.go`

- [ ] **Step 1: Write failing app version tests**

Create `internal/appversion/version_test.go`:

```go
package appversion

import (
	"regexp"
	"testing"
)

func TestVersionIsSemverLike(t *testing.T) {
	if Version == "" {
		t.Fatal("Version is empty")
	}
	if !regexp.MustCompile(`^v?\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("Version=%q, want v?MAJOR.MINOR.PATCH", Version)
	}
}
```

- [ ] **Step 2: Run app version test to verify it fails**

Run:

```bash
go test ./internal/appversion
```

Expected: FAIL because package `internal/appversion` or `Version` does not exist.

- [ ] **Step 3: Add app version package**

Create `internal/appversion/version.go`:

```go
package appversion

const Version = "0.1.1"
```

- [ ] **Step 4: Run app version test to verify it passes**

Run:

```bash
go test ./internal/appversion
```

Expected: PASS.

- [ ] **Step 5: Write failing paths test**

Add to `internal/paths/paths_test.go`:

```go
func TestDefaultIncludesUpdatePaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.UpdateStateFile != filepath.Join(p.InstallRoot, "update-state.json") {
		t.Fatalf("UpdateStateFile=%q", p.UpdateStateFile)
	}
	if p.UpdatesCacheDir != filepath.Join(p.CacheDir, "updates") {
		t.Fatalf("UpdatesCacheDir=%q", p.UpdatesCacheDir)
	}
	if p.PendingSlaveRestartsFile != filepath.Join(p.InstallRoot, "pending-slave-restarts.json") {
		t.Fatalf("PendingSlaveRestartsFile=%q", p.PendingSlaveRestartsFile)
	}
}
```

- [ ] **Step 6: Run paths test to verify it fails**

Run:

```bash
go test ./internal/paths -run TestDefaultIncludesUpdatePaths
```

Expected: FAIL because `Paths` has no update path fields.

- [ ] **Step 7: Add update path fields**

Modify `internal/paths/paths.go`:

```go
type Paths struct {
	UserHome string

	InstallRoot              string
	StateFile                string
	SecretsFile              string
	CacheDir                 string
	ConsolePortFile          string
	ConsoleNotificationsFile string
	MachineFile              string
	SlavesFile               string
	SlavesDir                string
	UpdateStateFile          string
	UpdatesCacheDir          string
	PendingSlaveRestartsFile string
	VSCodeUserDataDir        string
	VSCodeExtDir             string

	CodexDir                          string
	CodexConfigFile                   string
	CodexDesktopGlobalStateFile       string
	CodexDesktopComputerUseConfigFile string

	LocalAppDataRoot string
	CodexExePath     string
}
```

Set fields in `Default()`:

```go
UpdateStateFile:          filepath.Join(root, "update-state.json"),
UpdatesCacheDir:          filepath.Join(root, "cache", "updates"),
PendingSlaveRestartsFile: filepath.Join(root, "pending-slave-restarts.json"),
```

- [ ] **Step 8: Run paths tests**

Run:

```bash
go test ./internal/paths
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/appversion internal/paths/paths.go internal/paths/paths_test.go
git commit -m "feat: add app version and update paths"
```

---

### Task 2: Updater Manifest and Version Comparison

**Files:**
- Create: `internal/updater/manifest.go`
- Create: `internal/updater/version.go`
- Create: `internal/updater/manifest_test.go`
- Create: `internal/updater/version_test.go`

- [ ] **Step 1: Write failing manifest tests**

Create `internal/updater/manifest_test.go`:

```go
package updater

import "testing"

func TestManifestValidateAcceptsAssetsHTTPSInstaller(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:    123,
		Notes:   "release notes",
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestManifestValidateRejectsMissingSHA256(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected missing sha256 error")
	}
}

func TestManifestValidateRejectsNonHTTPSURL(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "http://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected non-HTTPS URL error")
	}
}

func TestManifestValidateRejectsURLOutsideAssetsHost(t *testing.T) {
	m := Manifest{
		Version: "0.1.2",
		URL:     "https://example.com/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected host allowlist error")
	}
}
```

- [ ] **Step 2: Write failing version tests**

Create `internal/updater/version_test.go`:

```go
package updater

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.1.2", "0.1.1", 1},
		{"0.1.10", "0.1.2", 1},
		{"v0.2.0", "0.1.9", 1},
		{"0.1.1", "0.1.1", 0},
		{"0.1.1", "0.1.2", -1},
	}
	for _, tt := range tests {
		got, err := CompareVersions(tt.a, tt.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q,%q): %v", tt.a, tt.b, err)
		}
		if got != tt.want {
			t.Fatalf("CompareVersions(%q,%q)=%d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCompareVersionsRejectsInvalidVersion(t *testing.T) {
	if _, err := CompareVersions("latest", "0.1.1"); err == nil {
		t.Fatal("expected invalid version error")
	}
}
```

- [ ] **Step 3: Run updater tests to verify they fail**

Run:

```bash
go test ./internal/updater
```

Expected: FAIL because `Manifest` and `CompareVersions` do not exist.

- [ ] **Step 4: Implement manifest validation**

Create `internal/updater/manifest.go`:

```go
package updater

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

const AssetsHost = "assets.agent.cs.ac.cn"

type Manifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

func (m Manifest) Validate() error {
	if _, err := parseVersion(m.Version); err != nil {
		return fmt.Errorf("invalid version: %w", err)
	}
	u, err := url.Parse(strings.TrimSpace(m.URL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid installer url")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("installer url must use https")
	}
	if !strings.EqualFold(u.Hostname(), AssetsHost) {
		return fmt.Errorf("installer url host %q is not allowed", u.Hostname())
	}
	if err := validateSHA256(m.SHA256); err != nil {
		return err
	}
	if m.Size < 0 {
		return fmt.Errorf("size must not be negative")
	}
	return nil
}

func validateSHA256(s string) error {
	s = strings.TrimSpace(s)
	if len(s) != 64 {
		return fmt.Errorf("sha256 must be 64 hex characters")
	}
	if _, err := hex.DecodeString(s); err != nil {
		return fmt.Errorf("sha256 must be hex: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Implement version comparison**

Create `internal/updater/version.go`:

```go
package updater

import (
	"fmt"
	"strconv"
	"strings"
)

type parsedVersion struct {
	major int
	minor int
	patch int
}

func CompareVersions(a, b string) (int, error) {
	av, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	bv, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for _, pair := range [][2]int{{av.major, bv.major}, {av.minor, bv.minor}, {av.patch, bv.patch}} {
		if pair[0] > pair[1] {
			return 1, nil
		}
		if pair[0] < pair[1] {
			return -1, nil
		}
	}
	return 0, nil
}

func parseVersion(v string) (parsedVersion, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return parsedVersion{}, fmt.Errorf("version %q must be MAJOR.MINOR.PATCH", v)
	}
	nums := [3]int{}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return parsedVersion{}, fmt.Errorf("version %q contains invalid component %q", v, part)
		}
		nums[i] = n
	}
	return parsedVersion{major: nums[0], minor: nums[1], patch: nums[2]}, nil
}
```

- [ ] **Step 6: Run updater manifest/version tests**

Run:

```bash
go test ./internal/updater -run 'TestManifest|TestCompare'
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/updater
git commit -m "feat: validate update manifests"
```

---

### Task 3: Updater State, Check, Download, and Installer Start

**Files:**
- Create: `internal/updater/state.go`
- Create: `internal/updater/service.go`
- Create: `internal/updater/installer_windows.go`
- Create: `internal/updater/installer_other.go`
- Create: `internal/updater/state_test.go`
- Create: `internal/updater/service_test.go`

- [ ] **Step 1: Write failing service tests**

Create `internal/updater/service_test.go`:

```go
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceCheckReportsAvailableUpdate(t *testing.T) {
	manifest := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	srv := manifestServer(t, manifest)
	service := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    srv.URL + "/latest.json",
		State:          NewStateStore(filepath.Join(t.TempDir(), "update-state.json")),
		Client:         srv.Client(),
		Now:            func() time.Time { return time.Unix(10, 0).UTC() },
	}
	st, err := service.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if st.Status != StatusAvailable || st.Update == nil || st.Update.Version != "0.1.2" {
		t.Fatalf("state=%+v", st)
	}
}

func TestServiceCheckReportsLatestForEqualVersion(t *testing.T) {
	manifest := Manifest{
		Version: "0.1.1",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.1-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	srv := manifestServer(t, manifest)
	service := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    srv.URL + "/latest.json",
		State:          NewStateStore(filepath.Join(t.TempDir(), "update-state.json")),
		Client:         srv.Client(),
		Now:            func() time.Time { return time.Unix(10, 0).UTC() },
	}
	st, err := service.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if st.Status != StatusLatest || st.Update != nil {
		t.Fatalf("state=%+v", st)
	}
}

func TestServiceAutomaticCheckSkipsWhenRecentlyChecked(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()
	store := NewStateStore(filepath.Join(t.TempDir(), "update-state.json"))
	now := time.Unix(10_000, 0).UTC()
	err := store.Save(State{
		CurrentVersion: "0.1.1",
		LastCheckedAt:  now.Add(-time.Hour),
		Status:         StatusLatest,
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	service := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    srv.URL + "/latest.json",
		State:          store,
		Client:         srv.Client(),
		Now:            func() time.Time { return now },
		AutoCheckEvery: 24 * time.Hour,
	}
	st, err := service.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if hits != 0 {
		t.Fatalf("manifest requested %d times", hits)
	}
	if st.Status != StatusLatest {
		t.Fatalf("state=%+v", st)
	}
}

func TestServiceDownloadVerifiesSHA256AndStartsInstaller(t *testing.T) {
	payload := []byte("setup")
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])
	var installerStarted string
	cacheDir := t.TempDir()
	manifest := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
		SHA256:  sha,
		Size:    int64(len(payload)),
	}
	fileSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer fileSrv.Close()
	manifest.URL = fileSrv.URL + "/agentserver-app-0.1.2-setup.exe"
	allowTestInstallerHost(t, "127.0.0.1")
	service := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       cacheDir,
		State:          NewStateStore(filepath.Join(t.TempDir(), "update-state.json")),
		Client:         fileSrv.Client(),
		StartInstaller: func(_ context.Context, path string) error {
			installerStarted = path
			return nil
		},
		Now: func() time.Time { return time.Unix(10, 0).UTC() },
	}
	st, err := service.DownloadAndStart(context.Background(), manifest)
	if err != nil {
		t.Fatalf("DownloadAndStart: %v", err)
	}
	if st.Status != StatusInstallerStarted {
		t.Fatalf("status=%q", st.Status)
	}
	if installerStarted == "" {
		t.Fatal("installer was not started")
	}
	if _, err := os.Stat(installerStarted); err != nil {
		t.Fatalf("installer file missing: %v", err)
	}
	if filepath.Dir(installerStarted) != cacheDir {
		t.Fatalf("installer path=%q, want cache dir %q", installerStarted, cacheDir)
	}
}

func TestServiceDownloadDeletesPartOnHashMismatch(t *testing.T) {
	payload := []byte("setup")
	fileSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer fileSrv.Close()
	cacheDir := t.TempDir()
	manifest := Manifest{
		Version: "0.1.2",
		URL:     fileSrv.URL + "/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:    int64(len(payload)),
	}
	allowTestInstallerHost(t, "127.0.0.1")
	service := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       cacheDir,
		State:          NewStateStore(filepath.Join(t.TempDir(), "update-state.json")),
		Client:         fileSrv.Client(),
		StartInstaller: func(context.Context, string) error { t.Fatal("installer should not start"); return nil },
	}
	_, err := service.DownloadAndStart(context.Background(), manifest)
	if err == nil {
		t.Fatal("expected hash mismatch")
	}
	matches, _ := filepath.Glob(filepath.Join(cacheDir, "*.part"))
	if len(matches) != 0 {
		t.Fatalf("part files left behind: %v", matches)
	}
}

func manifestServer(t *testing.T, m Manifest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
}

func allowTestInstallerHost(t *testing.T, host string) {
	t.Helper()
	extraAllowedInstallerHosts[host] = true
	t.Cleanup(func() { delete(extraAllowedInstallerHosts, host) })
}
```

- [ ] **Step 2: Write failing state store test**

Create `internal/updater/state_test.go`:

```go
package updater

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStateStoreRoundTripsState(t *testing.T) {
	store := NewStateStore(filepath.Join(t.TempDir(), "update-state.json"))
	in := State{
		CurrentVersion: "0.1.1",
		Status:         StatusAvailable,
		LastCheckedAt:  time.Unix(10, 0).UTC(),
		Update:         &AvailableUpdate{Version: "0.1.2", URL: "https://assets.agent.cs.ac.cn/app.exe", SHA256: "abc", Notes: "notes"},
	}
	if err := store.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Status != in.Status || got.Update == nil || got.Update.Version != "0.1.2" {
		t.Fatalf("got=%+v", got)
	}
}
```

- [ ] **Step 3: Run updater service tests to verify they fail**

Run:

```bash
go test ./internal/updater
```

Expected: FAIL because `Service`, state types, and installer helpers do not exist.

- [ ] **Step 4: Implement state store**

Create `internal/updater/state.go`:

```go
package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Status string

const (
	StatusIdle             Status = "idle"
	StatusChecking         Status = "checking"
	StatusLatest           Status = "latest"
	StatusAvailable        Status = "available"
	StatusDownloading      Status = "downloading"
	StatusReady            Status = "ready"
	StatusInstallerStarted Status = "installer_started"
	StatusError            Status = "error"
)

type AvailableUpdate struct {
	Version string `json:"version"`
	URL     string `json:"url,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

type State struct {
	CurrentVersion string           `json:"current_version"`
	LastCheckedAt  time.Time        `json:"last_checked_at,omitempty"`
	Status         Status           `json:"status"`
	Update         *AvailableUpdate `json:"update,omitempty"`
	LastError      string           `json:"last_error,omitempty"`
}

type StateStore struct {
	path string
}

func NewStateStore(path string) *StateStore {
	return &StateStore{path: path}
}

func (s *StateStore) Load() (State, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return State{Status: StatusIdle}, nil
	}
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, err
	}
	if st.Status == "" {
		st.Status = StatusIdle
	}
	return st, nil
}

func (s *StateStore) Save(st State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "."+filepath.Base(s.path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename update state: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Implement service**

Create `internal/updater/service.go` with check, download, and installer orchestration. Use this exact public shape so later tasks can wire it:

```go
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultManifestURL = "https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json"

type Service struct {
	CurrentVersion string
	ManifestURL    string
	CacheDir       string
	State          *StateStore
	Client         *http.Client
	StartInstaller func(context.Context, string) error
	Now            func() time.Time
	AutoCheckEvery time.Duration
}

func (s Service) Check(ctx context.Context, automatic bool) (State, error) {
	now := s.now()
	if automatic && s.State != nil {
		prior, err := s.State.Load()
		if err == nil && !prior.LastCheckedAt.IsZero() && now.Sub(prior.LastCheckedAt) < s.autoCheckEvery() {
			prior.CurrentVersion = s.CurrentVersion
			return prior, nil
		}
	}
	st := State{CurrentVersion: s.CurrentVersion, Status: StatusChecking}
	_ = s.save(st)
	m, err := s.fetchManifest(ctx)
	if err != nil {
		return s.errorState(now, err)
	}
	cmp, err := CompareVersions(m.Version, s.CurrentVersion)
	if err != nil {
		return s.errorState(now, err)
	}
	st.LastCheckedAt = now
	st.CurrentVersion = s.CurrentVersion
	st.LastError = ""
	if cmp <= 0 {
		st.Status = StatusLatest
		st.Update = nil
		return st, s.save(st)
	}
	st.Status = StatusAvailable
	st.Update = updateFromManifest(m)
	return st, s.save(st)
}

func (s Service) DownloadAndStart(ctx context.Context, m Manifest) (State, error) {
	if err := m.Validate(); err != nil {
		return s.errorState(s.now(), err)
	}
	if err := os.MkdirAll(s.CacheDir, 0o755); err != nil {
		return s.errorState(s.now(), err)
	}
	st := State{CurrentVersion: s.CurrentVersion, Status: StatusDownloading, Update: updateFromManifest(m)}
	_ = s.save(st)
	dst := filepath.Join(s.CacheDir, fmt.Sprintf("agentserver-app-%s-setup.exe", strings.TrimPrefix(m.Version, "v")))
	if err := s.download(ctx, m, dst); err != nil {
		_ = os.Remove(dst + ".part")
		return s.errorState(s.now(), err)
	}
	start := s.StartInstaller
	if start == nil {
		start = StartInstaller
	}
	if err := start(ctx, dst); err != nil {
		return s.errorState(s.now(), err)
	}
	st.Status = StatusInstallerStarted
	st.LastError = ""
	return st, s.save(st)
}

func (s Service) fetchManifest(ctx context.Context) (Manifest, error) {
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	url := s.ManifestURL
	if url == "" {
		url = DefaultManifestURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Manifest{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Manifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Manifest{}, fmt.Errorf("manifest status %d", resp.StatusCode)
	}
	var m Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return Manifest{}, err
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (s Service) download(ctx context.Context, m Manifest, dst string) error {
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("installer status %d", resp.StatusCode)
	}
	part := dst + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if m.Size > 0 && n != m.Size {
		_ = os.Remove(part)
		return fmt.Errorf("installer size mismatch: got %d want %d", n, m.Size)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, m.SHA256) {
		_ = os.Remove(part)
		return fmt.Errorf("installer sha256 mismatch: got %s want %s", got, m.SHA256)
	}
	return os.Rename(part, dst)
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s Service) autoCheckEvery() time.Duration {
	if s.AutoCheckEvery > 0 {
		return s.AutoCheckEvery
	}
	return 24 * time.Hour
}

func (s Service) save(st State) error {
	if s.State == nil {
		return nil
	}
	return s.State.Save(st)
}

func (s Service) errorState(now time.Time, cause error) (State, error) {
	st := State{CurrentVersion: s.CurrentVersion, LastCheckedAt: now, Status: StatusError, LastError: cause.Error()}
	_ = s.save(st)
	return st, cause
}

func updateFromManifest(m Manifest) *AvailableUpdate {
	return &AvailableUpdate{Version: m.Version, URL: m.URL, SHA256: m.SHA256, Size: m.Size, Notes: m.Notes}
}
```

- [ ] **Step 6: Implement installer platform files**

Create `internal/updater/installer_windows.go`:

```go
//go:build windows

package updater

import (
	"context"
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

func StartInstaller(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, path)
	process.HideWindow(cmd)
	return cmd.Start()
}
```

Create `internal/updater/installer_other.go`:

```go
//go:build !windows

package updater

import (
	"context"
	"fmt"
)

func StartInstaller(context.Context, string) error {
	return fmt.Errorf("starting Windows installer is only supported on Windows")
}
```

- [ ] **Step 7: Add test host override**

The manifest allowlist blocks `httptest` download URLs. Add this package-level map to `internal/updater/manifest.go`:

```go
var extraAllowedInstallerHosts = map[string]bool{}
```

Modify `Validate()` host check:

```go
host := strings.ToLower(u.Hostname())
if !strings.EqualFold(host, AssetsHost) && !extraAllowedInstallerHosts[host] {
	return fmt.Errorf("installer url host %q is not allowed", u.Hostname())
}
```

- [ ] **Step 8: Run updater tests**

Run:

```bash
go test ./internal/updater
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/updater
git commit -m "feat: add updater service"
```

---

### Task 4: Pending Slave Restart Recording and Restore

**Files:**
- Create: `internal/slave/restart_pending.go`
- Create: `internal/slave/restart_pending_test.go`

- [ ] **Step 1: Write failing pending restart tests**

Create `internal/slave/restart_pending_test.go`:

```go
package slave

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWritePendingRestartsRecordsEligibleStatuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")
	slaves := []Slave{
		{ID: "running", Status: StatusRunning},
		{ID: "starting", Status: StatusStarting},
		{ID: "auth", Status: StatusAuthRequired},
		{ID: "paused", Status: StatusPaused},
		{ID: "stopped", Status: StatusStopped},
		{ID: "error", Status: StatusError},
	}
	err := WritePendingRestarts(path, "0.1.2", slaves, func() time.Time { return time.Unix(10, 0).UTC() })
	if err != nil {
		t.Fatalf("WritePendingRestarts: %v", err)
	}
	got, err := ReadPendingRestarts(path)
	if err != nil {
		t.Fatalf("ReadPendingRestarts: %v", err)
	}
	want := []string{"running", "starting", "auth"}
	if len(got.SlaveIDs) != len(want) {
		t.Fatalf("SlaveIDs=%v", got.SlaveIDs)
	}
	for i := range want {
		if got.SlaveIDs[i] != want[i] {
			t.Fatalf("SlaveIDs=%v, want %v", got.SlaveIDs, want)
		}
	}
}

func TestRestorePendingRestartsRestartsEveryRecordedSlaveAndDeletesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")
	err := writePendingRestartFile(path, PendingRestarts{
		Reason:    "app_update",
		Version:   "0.1.2",
		CreatedAt: time.Unix(10, 0).UTC(),
		SlaveIDs:  []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("write pending: %v", err)
	}
	var restarted []string
	err = RestorePendingRestarts(context.Background(), path, func(_ context.Context, id string) error {
		restarted = append(restarted, id)
		return nil
	})
	if err != nil {
		t.Fatalf("RestorePendingRestarts: %v", err)
	}
	if len(restarted) != 2 || restarted[0] != "a" || restarted[1] != "b" {
		t.Fatalf("restarted=%v", restarted)
	}
	if _, err := ReadPendingRestarts(path); err == nil {
		t.Fatal("pending file still exists")
	}
}
```

- [ ] **Step 2: Run pending tests to verify they fail**

Run:

```bash
go test ./internal/slave -run 'PendingRestarts|RestorePending'
```

Expected: FAIL because pending restart functions do not exist.

- [ ] **Step 3: Implement pending restart helper**

Create `internal/slave/restart_pending.go`:

```go
package slave

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type PendingRestarts struct {
	Reason    string    `json:"reason"`
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	SlaveIDs  []string  `json:"slave_ids"`
}

func WritePendingRestarts(path, version string, slaves []Slave, now func() time.Time) error {
	ids := make([]string, 0, len(slaves))
	for _, sl := range slaves {
		switch sl.Status {
		case StatusRunning, StatusStarting, StatusAuthRequired:
			ids = append(ids, sl.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return writePendingRestartFile(path, PendingRestarts{
		Reason:    "app_update",
		Version:   version,
		CreatedAt: now().UTC(),
		SlaveIDs:  ids,
	})
}

func ReadPendingRestarts(path string) (PendingRestarts, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return PendingRestarts{}, err
	}
	var p PendingRestarts
	if err := json.Unmarshal(b, &p); err != nil {
		return PendingRestarts{}, err
	}
	return p, nil
}

func RestorePendingRestarts(ctx context.Context, path string, restart func(context.Context, string) error) error {
	p, err := ReadPendingRestarts(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range p.SlaveIDs {
		if id == "" {
			continue
		}
		if err := restart(ctx, id); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func writePendingRestartFile(path string, p PendingRestarts) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run slave tests**

Run:

```bash
go test ./internal/slave
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/slave/restart_pending.go internal/slave/restart_pending_test.go
git commit -m "feat: persist slave restarts for updates"
```

---

### Task 5: Console Update Controller Methods

**Files:**
- Create: `internal/console/update.go`
- Create: `internal/console/update_test.go`
- Modify: `internal/console/state.go`

- [ ] **Step 1: Write failing console update tests**

Create `internal/console/update_test.go`:

```go
package console

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/updater"
)

func TestControllerCheckUpdateDelegatesToUpdater(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "update-state.json")
	svc := &updater.Service{
		CurrentVersion: "0.1.1",
		State:          updater.NewStateStore(statePath),
		Now:            func() time.Time { return time.Unix(10, 0).UTC() },
	}
	ctrl := NewController(Deps{Updates: svc})
	_, err := ctrl.UpdateState(context.Background())
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
}

func TestControllerInstallUpdateRecordsEligibleSlaves(t *testing.T) {
	dir := t.TempDir()
	reg := slave.NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	machineStore := slave.NewMachineStore(filepath.Join(dir, "machine.json"))
	machine, err := machineStore.Ensure("PC")
	if err != nil {
		t.Fatal(err)
	}
	runningFolder := filepath.Join(dir, "running")
	pausedFolder := filepath.Join(dir, "paused")
	if err := os.MkdirAll(runningFolder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pausedFolder, 0o755); err != nil {
		t.Fatal(err)
	}
	running, err := reg.Create(machine, slave.CreateInput{Folder: runningFolder, Name: "running"})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := reg.Create(machine, slave.CreateInput{Folder: pausedFolder, Name: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Update(running.ID, func(s *slave.Slave) error {
		s.Status = slave.StatusRunning
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Update(paused.ID, func(s *slave.Slave) error {
		s.Status = slave.StatusPaused
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var started bool
	ctrl := NewController(Deps{
		Slaves: slave.NewManager(slave.ManagerDeps{Machines: machineStore, Registry: reg}),
		Updates: &updater.Service{
			CurrentVersion: "0.1.1",
			CacheDir:       dir,
			State:          updater.NewStateStore(filepath.Join(dir, "update-state.json")),
			StartInstaller: func(context.Context, string) error { started = true; return nil },
		},
		PendingSlaveRestartsPath: filepath.Join(dir, "pending-slave-restarts.json"),
		Now: func() time.Time { return time.Unix(10, 0).UTC() },
	})
	_, err = ctrl.InstallUpdate(context.Background(), updater.Manifest{
		Version: "0.1.2",
		URL:     "https://example.com/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err == nil || started {
		t.Fatalf("expected validation failure before installer start, err=%v started=%v", err, started)
	}
	pending, err := slave.ReadPendingRestarts(filepath.Join(dir, "pending-slave-restarts.json"))
	if err != nil {
		t.Fatalf("ReadPendingRestarts: %v", err)
	}
	if len(pending.SlaveIDs) != 1 || pending.SlaveIDs[0] != running.ID {
		t.Fatalf("pending=%+v", pending)
	}
}
```

- [ ] **Step 2: Run console update tests to verify they fail**

Run:

```bash
go test ./internal/console -run 'Update|InstallUpdate'
```

Expected: FAIL because `Deps.Updates`, `UpdateState`, `InstallUpdate`, and `PendingSlaveRestartsPath` do not exist.

- [ ] **Step 3: Extend console deps**

Modify `internal/console/state.go` `Deps`:

```go
Updates                  *updater.Service
PendingSlaveRestartsPath string
```

Add import:

```go
"github.com/agentserver/agentserver-pkg/internal/updater"
```

- [ ] **Step 4: Implement console update methods**

Create `internal/console/update.go`:

```go
package console

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/updater"
)

func (c *Controller) UpdateState(context.Context) (updater.State, error) {
	if c.d.Updates == nil || c.d.Updates.State == nil {
		return updater.State{}, errors.New("console: updater unavailable")
	}
	return c.d.Updates.State.Load()
}

func (c *Controller) CheckUpdate(ctx context.Context, automatic bool) (updater.State, error) {
	if c.d.Updates == nil {
		return updater.State{}, errors.New("console: updater unavailable")
	}
	return c.d.Updates.Check(ctx, automatic)
}

func (c *Controller) InstallUpdate(ctx context.Context, m updater.Manifest) (updater.State, error) {
	if c.d.Updates == nil {
		return updater.State{}, errors.New("console: updater unavailable")
	}
	if c.d.Slaves != nil && c.d.PendingSlaveRestartsPath != "" {
		_, slaves, err := c.d.Slaves.List(ctx)
		if err != nil {
			return updater.State{}, fmt.Errorf("list slaves before update: %w", err)
		}
		if err := slave.WritePendingRestarts(c.d.PendingSlaveRestartsPath, m.Version, slaves, c.d.Now); err != nil {
			return updater.State{}, fmt.Errorf("record pending slave restarts: %w", err)
		}
	}
	return c.d.Updates.DownloadAndStart(ctx, m)
}
```

- [ ] **Step 5: Run console tests**

Run:

```bash
go test ./internal/console
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/console/state.go internal/console/update.go internal/console/update_test.go
git commit -m "feat: add console update actions"
```

---

### Task 6: HTTP Console Update API

**Files:**
- Modify: `internal/ui/console.go`
- Modify: `internal/ui/server.go`
- Modify: `internal/ui/server_test.go`

- [ ] **Step 1: Write failing server endpoint tests**

Add to `internal/ui/server_test.go`:

```go
func TestServerConsoleUpdateStateEndpoint(t *testing.T) {
	cc := &fakeConsoleController{updateState: updater.State{CurrentVersion: "0.1.1", Status: updater.StatusLatest}}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/console/update")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body updater.State
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.CurrentVersion != "0.1.1" || body.Status != updater.StatusLatest {
		t.Fatalf("body=%+v", body)
	}
}

func TestServerConsoleUpdateCheckEndpoint(t *testing.T) {
	cc := &fakeConsoleController{updateState: updater.State{CurrentVersion: "0.1.1", Status: updater.StatusAvailable}}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/console/update/check", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !cc.checkedUpdate {
		t.Fatal("CheckUpdate was not called")
	}
}

func TestServerConsoleUpdateInstallEndpoint(t *testing.T) {
	cc := &fakeConsoleController{updateState: updater.State{CurrentVersion: "0.1.1", Status: updater.StatusInstallerStarted}}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	body := bytes.NewBufferString(`{"version":"0.1.2","url":"https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`)
	resp, err := http.Post(srv.URL+"/api/console/update/install", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !cc.installedUpdate || cc.installManifest.Version != "0.1.2" {
		t.Fatalf("install called=%v manifest=%+v", cc.installedUpdate, cc.installManifest)
	}
}
```

Update imports in `internal/ui/server_test.go`:

```go
"github.com/agentserver/agentserver-pkg/internal/updater"
```

Extend `fakeConsoleController`:

```go
updateState     updater.State
checkedUpdate   bool
installedUpdate bool
installManifest updater.Manifest
```

Add methods:

```go
func (f *fakeConsoleController) UpdateState(context.Context) (updater.State, error) {
	return f.updateState, nil
}
func (f *fakeConsoleController) CheckUpdate(context.Context, bool) (updater.State, error) {
	f.checkedUpdate = true
	return f.updateState, nil
}
func (f *fakeConsoleController) InstallUpdate(_ context.Context, m updater.Manifest) (updater.State, error) {
	f.installedUpdate = true
	f.installManifest = m
	return f.updateState, nil
}
```

- [ ] **Step 2: Run UI server tests to verify they fail**

Run:

```bash
go test ./internal/ui -run 'ConsoleUpdate'
```

Expected: FAIL because update methods and routes do not exist.

- [ ] **Step 3: Extend ConsoleController interface and noop**

Modify `internal/ui/console.go`:

```go
UpdateState(context.Context) (updater.State, error)
CheckUpdate(context.Context, bool) (updater.State, error)
InstallUpdate(context.Context, updater.Manifest) (updater.State, error)
```

Add import:

```go
"github.com/agentserver/agentserver-pkg/internal/updater"
```

Add noop methods returning unavailable errors:

```go
func (noopConsoleController) UpdateState(context.Context) (updater.State, error) {
	return updater.State{}, errors.New("console: updater unavailable")
}
func (noopConsoleController) CheckUpdate(context.Context, bool) (updater.State, error) {
	return updater.State{}, errors.New("console: updater unavailable")
}
func (noopConsoleController) InstallUpdate(context.Context, updater.Manifest) (updater.State, error) {
	return updater.State{}, errors.New("console: updater unavailable")
}
```

- [ ] **Step 4: Add server routes and handlers**

Modify route setup in `internal/ui/server.go`:

```go
mux.HandleFunc("/api/console/update", s.handleConsoleUpdate)
mux.HandleFunc("/api/console/update/check", s.handleConsoleUpdateCheck)
mux.HandleFunc("/api/console/update/install", s.handleConsoleUpdateInstall)
```

Add handlers:

```go
func (s *server) handleConsoleUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	st, err := s.c.UpdateState(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *server) handleConsoleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !requirePostTrustedMutation(w, r) {
		return
	}
	st, err := s.c.CheckUpdate(r.Context(), false)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *server) handleConsoleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if !requirePostTrustedMutation(w, r) {
		return
	}
	var m updater.Manifest
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	st, err := s.c.InstallUpdate(r.Context(), m)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}
```

Add import:

```go
"github.com/agentserver/agentserver-pkg/internal/updater"
```

- [ ] **Step 5: Run UI tests**

Run:

```bash
go test ./internal/ui
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/console.go internal/ui/server.go internal/ui/server_test.go
git commit -m "feat: expose update console api"
```

---

### Task 7: Launcher Wiring, Automatic Checks, and Slave Restore

**Files:**
- Modify: `cmd/launcher/main.go`
- Modify: `cmd/launcher/main_test.go`

- [ ] **Step 1: Write failing launcher tests**

Add to `cmd/launcher/main_test.go`:

```go
func TestNewCompletedUpdaterUsesDefaultManifestAndPaths(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		UpdateStateFile:          filepath.Join(dir, "update-state.json"),
		UpdatesCacheDir:          filepath.Join(dir, "cache", "updates"),
		PendingSlaveRestartsFile: filepath.Join(dir, "pending-slave-restarts.json"),
	}
	svc := newCompletedUpdater(p)
	if svc.ManifestURL != updater.DefaultManifestURL {
		t.Fatalf("ManifestURL=%q", svc.ManifestURL)
	}
	if svc.CacheDir != p.UpdatesCacheDir {
		t.Fatalf("CacheDir=%q", svc.CacheDir)
	}
	if svc.State == nil {
		t.Fatal("State store missing")
	}
}

func TestRestorePendingSlaveRestartsCallsManager(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")
	err := os.WriteFile(path, []byte(`{"reason":"app_update","version":"0.1.2","created_at":"2026-06-12T00:00:00Z","slave_ids":["a","b"]}`), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	err = restorePendingSlaveRestarts(context.Background(), path, func(_ context.Context, id string) error {
		ids = append(ids, id)
		return nil
	})
	if err != nil {
		t.Fatalf("restorePendingSlaveRestarts: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("ids=%v", ids)
	}
}
```

Add imports:

```go
"github.com/agentserver/agentserver-pkg/internal/updater"
```

- [ ] **Step 2: Run launcher tests to verify they fail**

Run:

```bash
go test ./cmd/launcher -run 'CompletedUpdater|RestorePendingSlaveRestarts'
```

Expected: FAIL because helper functions do not exist.

- [ ] **Step 3: Implement updater construction and restore helper**

Modify `cmd/launcher/main.go` imports:

```go
"github.com/agentserver/agentserver-pkg/internal/appversion"
"github.com/agentserver/agentserver-pkg/internal/updater"
```

Add helpers:

```go
func newCompletedUpdater(p paths.Paths) *updater.Service {
	return &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    updater.DefaultManifestURL,
		CacheDir:       p.UpdatesCacheDir,
		State:          updater.NewStateStore(p.UpdateStateFile),
	}
}

func restorePendingSlaveRestarts(ctx context.Context, path string, restart func(context.Context, string) error) error {
	return slave.RestorePendingRestarts(ctx, path, restart)
}

func scheduleAutomaticUpdateCheck(ctx context.Context, svc *updater.Service, delay time.Duration) {
	if svc == nil {
		return
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		run := func() {
			if _, err := svc.Check(ctx, true); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("launcher: automatic update check: %v", err)
			}
		}
		run()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
```

- [ ] **Step 4: Wire updater and restore in `serveCompletedConsole`**

In `serveCompletedConsole`, after `slaveManager` is created:

```go
updates := newCompletedUpdater(in.Paths)
if err := restorePendingSlaveRestarts(ctx, in.Paths.PendingSlaveRestartsFile, func(ctx context.Context, id string) error {
	_, err := slaveManager.Restart(ctx, id)
	return err
}); err != nil {
	log.Printf("launcher: restore pending slave restarts: %v", err)
}
scheduleAutomaticUpdateCheck(ctx, updates, 30*time.Second)
```

Pass update deps to `console.NewController`:

```go
Updates:                  updates,
PendingSlaveRestartsPath: in.Paths.PendingSlaveRestartsFile,
```

- [ ] **Step 5: Run launcher tests**

Run:

```bash
go test ./cmd/launcher
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/launcher/main.go cmd/launcher/main_test.go
git commit -m "feat: wire automatic update checks"
```

---

### Task 8: Frontend API and Dashboard Update Controls

**Files:**
- Modify: `internal/ui/web/src/api.ts`
- Modify: `internal/ui/web/src/components/Dashboard.vue`
- Modify: `internal/ui/web/src/__tests__/Dashboard.spec.ts`

- [ ] **Step 1: Write failing frontend API/dashboard tests**

Add to `internal/ui/web/src/__tests__/Dashboard.spec.ts`:

```ts
it('renders update state and checks updates manually', async () => {
  vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
  vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves());
  vi.spyOn(api, 'getConsoleUpdate').mockResolvedValue({
    current_version: '0.1.1',
    status: 'latest',
  });
  const checkSpy = vi.spyOn(api, 'checkConsoleUpdate').mockResolvedValue({
    current_version: '0.1.1',
    status: 'available',
    update: { version: '0.1.2', notes: '修复问题' },
  });

  const w = mount(Dashboard);
  await flushPromises();

  expect(w.text()).toContain('当前版本 0.1.1');
  await w.find('[data-test="check-update"]').trigger('click');
  await flushPromises();

  expect(checkSpy).toHaveBeenCalled();
  expect(w.text()).toContain('发现新版本 0.1.2');
});

it('asks for confirmation before installing an update', async () => {
  vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
  vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves());
  vi.spyOn(api, 'getConsoleUpdate').mockResolvedValue({
    current_version: '0.1.1',
    status: 'available',
    update: {
      version: '0.1.2',
      url: 'https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe',
      sha256: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
    },
  });
  const installSpy = vi.spyOn(api, 'installConsoleUpdate').mockResolvedValue({
    current_version: '0.1.1',
    status: 'installer_started',
  });
  vi.spyOn(window, 'confirm').mockReturnValue(true);

  const w = mount(Dashboard);
  await flushPromises();
  await w.find('[data-test="install-update"]').trigger('click');
  await flushPromises();

  expect(window.confirm).toHaveBeenCalled();
  expect(installSpy).toHaveBeenCalledWith({
    version: '0.1.2',
    url: 'https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe',
    sha256: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
  });
});

it('does not install an update when confirmation is cancelled', async () => {
  vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
  vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves());
  vi.spyOn(api, 'getConsoleUpdate').mockResolvedValue({
    current_version: '0.1.1',
    status: 'available',
    update: { version: '0.1.2' },
  });
  const installSpy = vi.spyOn(api, 'installConsoleUpdate').mockResolvedValue({
    current_version: '0.1.1',
    status: 'installer_started',
  });
  vi.spyOn(window, 'confirm').mockReturnValue(false);

  const w = mount(Dashboard);
  await flushPromises();
  await w.find('[data-test="install-update"]').trigger('click');
  await flushPromises();

  expect(installSpy).not.toHaveBeenCalled();
});
```

Also update the existing Dashboard test helper so existing tests do not make real update API calls:

```ts
function consoleUpdate(overrides?: Partial<api.ConsoleUpdateState>): api.ConsoleUpdateState {
  return {
    current_version: '0.1.1',
    status: 'latest',
    ...overrides,
  };
}

function mockConsoleState() {
  vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
  vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves());
  vi.spyOn(api, 'getConsoleUpdate').mockResolvedValue(consoleUpdate());
}
```

- [ ] **Step 2: Run Dashboard tests to verify they fail**

Run:

```bash
cd internal/ui/web && npm test -- Dashboard.spec.ts
```

Expected: FAIL because update API functions and Dashboard controls do not exist.

- [ ] **Step 3: Add API types and functions**

Modify `internal/ui/web/src/api.ts`:

```ts
export type ConsoleUpdateStatus =
  'idle' | 'checking' | 'latest' | 'available' | 'downloading' | 'ready' | 'installer_started' | 'error';

export interface ConsoleAvailableUpdate {
  version: string;
  url?: string;
  sha256?: string;
  size?: number;
  notes?: string;
}

export interface ConsoleUpdateState {
  current_version: string;
  last_checked_at?: string;
  status: ConsoleUpdateStatus;
  update?: ConsoleAvailableUpdate;
  last_error?: string;
}

export const getConsoleUpdate = () =>
  request<ConsoleUpdateState>('/api/console/update');

export const checkConsoleUpdate = () =>
  request<ConsoleUpdateState>('/api/console/update/check', { method: 'POST' });

export const installConsoleUpdate = (input: ConsoleAvailableUpdate) =>
  request<ConsoleUpdateState>('/api/console/update/install', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
```

- [ ] **Step 4: Add Dashboard state and methods**

In `Dashboard.vue` script, add refs:

```ts
const updateState = ref<api.ConsoleUpdateState | null>(null);
const updateError = ref('');
const checkingUpdate = ref(false);
const installingUpdate = ref(false);
```

Include update error in `visibleErrors`:

```ts
{ key: 'update', message: updateError.value },
```

Add load/check/install methods:

```ts
async function loadUpdate() {
  try {
    updateState.value = await api.getConsoleUpdate();
    updateError.value = '';
  } catch (e) {
    updateError.value = errorMessage(e);
  }
}

async function checkUpdate() {
  if (checkingUpdate.value) return;
  checkingUpdate.value = true;
  try {
    updateState.value = await api.checkConsoleUpdate();
    updateError.value = '';
  } catch (e) {
    updateError.value = errorMessage(e);
  } finally {
    checkingUpdate.value = false;
  }
}

async function installUpdate() {
  if (installingUpdate.value || !updateState.value?.update) return;
  const update = updateState.value.update;
  const confirmed = window.confirm(`将下载并启动星池指挥官 ${update.version} 安装器。安装过程中星池指挥官会短暂关闭，登录信息和已创建的本地智能体会保留。是否继续？`);
  if (!confirmed) return;
  installingUpdate.value = true;
  try {
    updateState.value = await api.installConsoleUpdate(update);
    updateError.value = '';
  } catch (e) {
    updateError.value = errorMessage(e);
  } finally {
    installingUpdate.value = false;
  }
}
```

Call on mount:

```ts
void loadUpdate();
```

- [ ] **Step 5: Render update controls**

Add template block near the top after alerts:

```vue
<section class="update-panel">
  <div class="section-head">
    <h2>应用更新</h2>
    <p>当前版本 {{ updateState?.current_version || '读取中' }}</p>
  </div>
  <div class="update-body">
    <span v-if="updateState?.status === 'latest'">已是最新版本</span>
    <span v-else-if="updateState?.status === 'available' && updateState.update">
      发现新版本 {{ updateState.update.version }}
    </span>
    <span v-else-if="updateState?.status === 'installer_started'">安装器已启动</span>
    <span v-else-if="updateState?.last_error">{{ updateState.last_error }}</span>
    <span v-else>可手动检查更新</span>
    <p v-if="updateState?.update?.notes">{{ updateState.update.notes }}</p>
  </div>
  <div class="update-actions">
    <el-button
      data-test="check-update"
      :loading="checkingUpdate"
      :disabled="checkingUpdate || installingUpdate"
      @click="checkUpdate"
    >
      检查更新
    </el-button>
    <el-button
      v-if="updateState?.status === 'available' && updateState.update"
      data-test="install-update"
      type="primary"
      :loading="installingUpdate"
      :disabled="checkingUpdate || installingUpdate"
      @click="installUpdate"
    >
      下载并安装
    </el-button>
  </div>
</section>
```

Add CSS:

```css
.update-panel {
  padding: 14px 16px;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  background: #fff;
}

.update-body {
  display: flex;
  flex-direction: column;
  gap: 6px;
  color: #303133;
}

.update-body p {
  margin: 0;
  color: #606266;
}

.update-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
}

.update-actions :deep(.el-button) {
  margin-left: 0;
}
```

- [ ] **Step 6: Run frontend tests**

Run:

```bash
cd internal/ui/web && npm test -- Dashboard.spec.ts
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/web/src/api.ts internal/ui/web/src/components/Dashboard.vue internal/ui/web/src/__tests__/Dashboard.spec.ts
git commit -m "feat: add dashboard update controls"
```

---

### Task 9: Full Verification

**Files:**
- No code files.

- [ ] **Step 1: Run Go test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run frontend test suite**

Run:

```bash
cd internal/ui/web && npm test
```

Expected: PASS.

- [ ] **Step 3: Build frontend assets**

Run:

```bash
cd internal/ui/web && npm run build
```

Expected: PASS and `internal/ui/assets/dist/index.html` exists.

- [ ] **Step 4: Run cross-package compile check**

Run:

```bash
go test ./cmd/launcher ./internal/updater ./internal/console ./internal/ui ./internal/slave
```

Expected: PASS.

- [ ] **Step 5: Check worktree status**

Run:

```bash
git status --short
```

Expected: only intended source/test/doc files changed, no build artifacts that should be excluded.

- [ ] **Step 6: Commit verification adjustments if needed**

If verification required small fixes, commit them:

```bash
git add <fixed-files>
git commit -m "fix: stabilize update verification"
```

Expected: commit succeeds or no fixes were needed.

---

## Self-Review Notes

- Spec coverage: Tasks cover manifest, manual check, automatic check, user confirmation, installer download and launch, state preservation by avoiding uninstall, pending slave restarts, API, UI, and verification.
- Scope: One feature with four connected surfaces; implementation remains one plan because each task produces testable increments.
- Type consistency: Public update types are `updater.Manifest`, `updater.State`, `updater.AvailableUpdate`, and frontend `ConsoleUpdateState`.
