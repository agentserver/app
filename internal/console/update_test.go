package console

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/updater"
)

func TestControllerUpdateStateLoadsFromUpdaterStateStore(t *testing.T) {
	dir := t.TempDir()
	store := updater.NewStateStore(filepath.Join(dir, "updates.json"))
	want := updater.State{
		CurrentVersion: "1.0.0",
		Status:         updater.StatusAvailable,
		Update: &updater.AvailableUpdate{
			Version: "1.2.0",
			URL:     "https://assets.agent.cs.ac.cn/agentserver-app.exe",
			SHA256:  strings.Repeat("a", 64),
			Size:    12,
		},
	}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save update state: %v", err)
	}
	c := NewController(Deps{Updates: &updater.Service{State: store}})

	got, err := c.UpdateState(context.Background())
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state=%+v, want %+v", got, want)
	}
}

func TestControllerCheckUpdateDelegatesToUpdaterService(t *testing.T) {
	manifest := updater.Manifest{
		Version: "1.2.0",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app.exe",
		SHA256:  strings.Repeat("b", 64),
		Size:    42,
		Notes:   "release notes",
	}
	var requested bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"` + manifest.Version + `","url":"` + manifest.URL + `","sha256":"` + manifest.SHA256 + `","size":42,"notes":"release notes"}`))
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	c := NewController(Deps{
		Updates: &updater.Service{
			CurrentVersion: "1.0.0",
			ManifestURL:    server.URL,
			State:          updater.NewStateStore(filepath.Join(dir, "updates.json")),
			Client:         server.Client(),
			Now: func() time.Time {
				return time.Date(2026, 6, 12, 8, 0, 0, 0, time.UTC)
			},
		},
	})

	got, err := c.CheckUpdate(context.Background(), false)
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if !requested {
		t.Fatal("manifest server was not requested")
	}
	if got.Status != updater.StatusAvailable || got.Update == nil || got.Update.Version != manifest.Version || got.Update.Notes != manifest.Notes {
		t.Fatalf("state=%+v", got)
	}
}

func TestControllerInstallUpdateDoesNotRecordPendingRestartsBeforeValidationError(t *testing.T) {
	dir := t.TempDir()
	manager := newConsoleUpdateSlaveManager(t, dir)
	createConsoleUpdateSlave(t, manager, "running", slave.StatusRunning)
	createConsoleUpdateSlave(t, manager, "starting", slave.StatusStarting)
	createConsoleUpdateSlave(t, manager, "auth", slave.StatusAuthRequired)
	createConsoleUpdateSlave(t, manager, "paused", slave.StatusPaused)
	createConsoleUpdateSlave(t, manager, "stopped", slave.StatusStopped)
	createConsoleUpdateSlave(t, manager, "error", slave.StatusError)
	pendingPath := filepath.Join(dir, "pending-restarts.json")
	c := NewController(Deps{
		Slaves:                   manager,
		PendingSlaveRestartsPath: pendingPath,
		Now: func() time.Time {
			return time.Date(2026, 6, 12, 9, 30, 0, 0, time.UTC)
		},
		Updates: &updater.Service{
			CurrentVersion: "1.0.0",
			State:          updater.NewStateStore(filepath.Join(dir, "updates.json")),
		},
	})
	manifest := updater.Manifest{
		Version: "1.2.0",
		URL:     "http://example.test/agentserver-app.exe",
		SHA256:  "not-a-valid-sha",
		Size:    1,
	}

	got, err := c.InstallUpdate(context.Background(), manifest)
	if err == nil {
		t.Fatal("expected manifest validation error")
	}
	if got.Status != updater.StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, updater.StatusError)
	}
	if _, readErr := slave.ReadPendingRestarts(pendingPath); !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("pending restart file err=%v, want missing", readErr)
	}
}

func TestControllerInstallUpdateDoesNotRecordPendingRestartsBeforeHashError(t *testing.T) {
	dir := t.TempDir()
	manager := newConsoleUpdateSlaveManager(t, dir)
	createConsoleUpdateSlave(t, manager, "running", slave.StatusRunning)
	body := []byte("installer bytes")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	pendingPath := filepath.Join(dir, "pending-restarts.json")
	got, err := NewController(Deps{
		Slaves:                   manager,
		PendingSlaveRestartsPath: pendingPath,
		Updates: &updater.Service{
			CurrentVersion: "1.0.0",
			CacheDir:       filepath.Join(dir, "cache"),
			State:          updater.NewStateStore(filepath.Join(dir, "updates.json")),
			Client:         consoleAssetsHostClient(t, server),
		},
	}).InstallUpdate(context.Background(), updater.Manifest{
		Version: "1.2.0",
		URL:     consoleAssetsInstallerURL("/agentserver-app.exe"),
		SHA256:  strings.Repeat("d", 64),
		Size:    int64(len(body)),
	})
	if err == nil {
		t.Fatal("expected hash mismatch error")
	}
	if got.Status != updater.StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, updater.StatusError)
	}
	if _, readErr := slave.ReadPendingRestarts(pendingPath); !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("pending restart file err=%v, want missing", readErr)
	}
}

func TestControllerInstallUpdateRecordsEligibleSlavesImmediatelyBeforeInstallerStart(t *testing.T) {
	dir := t.TempDir()
	manager := newConsoleUpdateSlaveManager(t, dir)
	runningID := createConsoleUpdateSlave(t, manager, "running", slave.StatusRunning)
	startingID := createConsoleUpdateSlave(t, manager, "starting", slave.StatusStarting)
	authID := createConsoleUpdateSlave(t, manager, "auth", slave.StatusAuthRequired)
	createConsoleUpdateSlave(t, manager, "paused", slave.StatusPaused)
	body := []byte("installer bytes")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	pendingPath := filepath.Join(dir, "pending-restarts.json")
	manifest := validConsoleUpdateManifestForBody(body)
	originalBeforeCalled := false
	startCalled := false
	svc := &updater.Service{
		CurrentVersion: "1.0.0",
		CacheDir:       filepath.Join(dir, "cache"),
		State:          updater.NewStateStore(filepath.Join(dir, "updates.json")),
		Client:         consoleAssetsHostClient(t, server),
		BeforeInstallerStart: func(context.Context, updater.Manifest, string) error {
			originalBeforeCalled = true
			return nil
		},
		StartInstaller: func(ctx context.Context, path string) error {
			startCalled = true
			pending, err := slave.ReadPendingRestarts(pendingPath)
			if err != nil {
				t.Fatalf("ReadPendingRestarts before installer start: %v", err)
			}
			if pending.Version != manifest.Version {
				t.Fatalf("pending version=%q, want %q", pending.Version, manifest.Version)
			}
			if want := []string{runningID, startingID, authID}; !reflect.DeepEqual(pending.SlaveIDs, want) {
				t.Fatalf("pending SlaveIDs=%v, want %v", pending.SlaveIDs, want)
			}
			return nil
		},
	}
	originalBeforePointer := reflect.ValueOf(svc.BeforeInstallerStart).Pointer()
	originalStartPointer := reflect.ValueOf(svc.StartInstaller).Pointer()

	got, err := NewController(Deps{
		Slaves:                   manager,
		PendingSlaveRestartsPath: pendingPath,
		Now: func() time.Time {
			return time.Date(2026, 6, 12, 9, 30, 0, 0, time.UTC)
		},
		Updates: svc,
	}).InstallUpdate(context.Background(), manifest)
	if err != nil {
		t.Fatalf("InstallUpdate: %v", err)
	}
	if got.Status != updater.StatusInstallerStarted {
		t.Fatalf("Status=%q, want %q", got.Status, updater.StatusInstallerStarted)
	}
	if !originalBeforeCalled {
		t.Fatal("existing BeforeInstallerStart callback was not called")
	}
	if !startCalled {
		t.Fatal("StartInstaller was not called")
	}
	if gotPointer := reflect.ValueOf(svc.BeforeInstallerStart).Pointer(); gotPointer != originalBeforePointer {
		t.Fatal("shared updater service BeforeInstallerStart callback was mutated")
	}
	if gotPointer := reflect.ValueOf(svc.StartInstaller).Pointer(); gotPointer != originalStartPointer {
		t.Fatal("shared updater service StartInstaller callback was mutated")
	}
}

func TestControllerInstallUpdateRemovesPendingRestartsWhenInstallerStartFails(t *testing.T) {
	dir := t.TempDir()
	manager := newConsoleUpdateSlaveManager(t, dir)
	createConsoleUpdateSlave(t, manager, "running", slave.StatusRunning)
	body := []byte("installer bytes")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	pendingPath := filepath.Join(dir, "pending-restarts.json")
	startErr := errors.New("start failed")

	got, err := NewController(Deps{
		Slaves:                   manager,
		PendingSlaveRestartsPath: pendingPath,
		Now: func() time.Time {
			return time.Date(2026, 6, 12, 9, 30, 0, 0, time.UTC)
		},
		Updates: &updater.Service{
			CurrentVersion: "1.0.0",
			CacheDir:       filepath.Join(dir, "cache"),
			State:          updater.NewStateStore(filepath.Join(dir, "updates.json")),
			Client:         consoleAssetsHostClient(t, server),
			StartInstaller: func(context.Context, string) error {
				return startErr
			},
		},
	}).InstallUpdate(context.Background(), validConsoleUpdateManifestForBody(body))
	if !errors.Is(err, startErr) {
		t.Fatalf("InstallUpdate err=%v, want %v", err, startErr)
	}
	if got.Status != updater.StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, updater.StatusError)
	}
	if _, readErr := slave.ReadPendingRestarts(pendingPath); !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("pending restart file err=%v, want missing", readErr)
	}
}

func TestControllerInstallUpdateDownloadsThenStopsBeforeInstallerWhenSlaveListFails(t *testing.T) {
	dir := t.TempDir()
	body := []byte("installer bytes")
	downloaded := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloaded = true
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	startCalled := false
	c := NewController(Deps{
		Slaves: slave.NewManager(slave.ManagerDeps{
			Registry: slave.NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		}),
		PendingSlaveRestartsPath: filepath.Join(dir, "pending.json"),
		Updates: &updater.Service{
			CurrentVersion: "1.0.0",
			CacheDir:       filepath.Join(dir, "cache"),
			State:          updater.NewStateStore(filepath.Join(dir, "updates.json")),
			Client:         consoleAssetsHostClient(t, server),
			StartInstaller: func(context.Context, string) error {
				startCalled = true
				return nil
			},
		},
	})

	_, err := c.InstallUpdate(context.Background(), validConsoleUpdateManifestForBody(body))
	if err == nil {
		t.Fatal("expected slave list error")
	}
	if !strings.Contains(err.Error(), "list slaves before update") {
		t.Fatalf("error=%q, want list context", err)
	}
	if !downloaded {
		t.Fatal("installer was not downloaded before pending restart callback listed slaves")
	}
	if startCalled {
		t.Fatal("installer started after slave list failed")
	}
}

func TestControllerInstallUpdateDownloadsThenStopsBeforeInstallerWhenPendingRestartWriteFails(t *testing.T) {
	dir := t.TempDir()
	manager := newConsoleUpdateSlaveManager(t, dir)
	createConsoleUpdateSlave(t, manager, "running", slave.StatusRunning)
	body := []byte("installer bytes")
	downloaded := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloaded = true
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	blockingFile := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blockingFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	startCalled := false
	c := NewController(Deps{
		Slaves:                   manager,
		PendingSlaveRestartsPath: filepath.Join(blockingFile, "pending.json"),
		Updates: &updater.Service{
			CurrentVersion: "1.0.0",
			CacheDir:       filepath.Join(dir, "cache"),
			State:          updater.NewStateStore(filepath.Join(dir, "updates.json")),
			Client:         consoleAssetsHostClient(t, server),
			StartInstaller: func(context.Context, string) error {
				startCalled = true
				return nil
			},
		},
	})

	_, err := c.InstallUpdate(context.Background(), validConsoleUpdateManifestForBody(body))
	if err == nil {
		t.Fatal("expected pending restart write error")
	}
	if !strings.Contains(err.Error(), "record pending slave restarts") {
		t.Fatalf("error=%q, want pending restart context", err)
	}
	if !downloaded {
		t.Fatal("installer was not downloaded before pending restart write")
	}
	if startCalled {
		t.Fatal("installer started after pending restart write failed")
	}
}

func TestControllerUpdateMethodsRequireUpdater(t *testing.T) {
	c := NewController(Deps{})
	ctx := context.Background()
	if _, err := c.UpdateState(ctx); err == nil || err.Error() != "console: updater unavailable" {
		t.Fatalf("UpdateState err=%v", err)
	}
	if _, err := c.CheckUpdate(ctx, false); err == nil || err.Error() != "console: updater unavailable" {
		t.Fatalf("CheckUpdate err=%v", err)
	}
	if _, err := c.InstallUpdate(ctx, updater.Manifest{}); err == nil || err.Error() != "console: updater unavailable" {
		t.Fatalf("InstallUpdate err=%v", err)
	}
}

func TestControllerUpdateStateRequiresUpdaterStateStore(t *testing.T) {
	c := NewController(Deps{Updates: &updater.Service{}})
	_, err := c.UpdateState(context.Background())
	if err == nil || err.Error() != "console: updater unavailable" {
		t.Fatalf("UpdateState err=%v", err)
	}
}

func newConsoleUpdateSlaveManager(t *testing.T, dir string) *slave.Manager {
	t.Helper()
	manager := slave.NewManager(slave.ManagerDeps{
		Machines: slave.NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry: slave.NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
	})
	if _, err := manager.Machines.Ensure("PC"); err != nil {
		t.Fatal(err)
	}
	return manager
}

func createConsoleUpdateSlave(t *testing.T, manager *slave.Manager, name string, status slave.Status) string {
	t.Helper()
	folder := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	machine, err := manager.Machines.Load()
	if err != nil {
		t.Fatalf("Load machine: %v", err)
	}
	sl, err := manager.Registry.Create(machine, slave.CreateInput{Folder: folder, Name: name})
	if err != nil {
		t.Fatalf("Create slave %s: %v", name, err)
	}
	if _, err := manager.Registry.Update(sl.ID, func(s *slave.Slave) error {
		s.Status = status
		return nil
	}); err != nil {
		t.Fatalf("Update slave %s: %v", name, err)
	}
	return sl.ID
}

func validConsoleUpdateManifestForBody(body []byte) updater.Manifest {
	sum := sha256.Sum256(body)
	return updater.Manifest{
		Version: "1.2.0",
		URL:     consoleAssetsInstallerURL("/agentserver-app.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	}
}

func consoleAssetsInstallerURL(path string) string {
	return "https://" + updater.AssetsHost + path
}

func consoleAssetsHostClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := server.Client()
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = consoleAssetsHostRewriteTransport{base: base, target: target}
	return client
}

type consoleAssetsHostRewriteTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (t consoleAssetsHostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.EqualFold(req.URL.Hostname(), updater.AssetsHost) {
		clone := req.Clone(req.Context())
		u := *clone.URL
		u.Scheme = t.target.Scheme
		u.Host = t.target.Host
		clone.URL = &u
		clone.Host = ""
		return t.base.RoundTrip(clone)
	}
	return t.base.RoundTrip(req)
}
