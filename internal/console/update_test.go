package console

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestControllerInstallUpdateRecordsEligibleSlavesBeforeDownloadError(t *testing.T) {
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
	pending, readErr := slave.ReadPendingRestarts(pendingPath)
	if readErr != nil {
		t.Fatalf("ReadPendingRestarts: %v", readErr)
	}
	if pending.Version != manifest.Version {
		t.Fatalf("pending version=%q, want %q", pending.Version, manifest.Version)
	}
	if want := []string{"running", "starting", "auth"}; !reflect.DeepEqual(pending.SlaveIDs, want) {
		t.Fatalf("pending SlaveIDs=%v, want %v", pending.SlaveIDs, want)
	}
}

func TestControllerInstallUpdateDoesNotStartDownloadWhenPendingRestartWriteFails(t *testing.T) {
	dir := t.TempDir()
	manager := newConsoleUpdateSlaveManager(t, dir)
	createConsoleUpdateSlave(t, manager, "running", slave.StatusRunning)
	blockingFile := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blockingFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	transport := &recordingRoundTripper{}
	c := NewController(Deps{
		Slaves:                   manager,
		PendingSlaveRestartsPath: filepath.Join(blockingFile, "pending.json"),
		Updates: &updater.Service{
			CurrentVersion: "1.0.0",
			CacheDir:       filepath.Join(dir, "cache"),
			State:          updater.NewStateStore(filepath.Join(dir, "updates.json")),
			Client:         &http.Client{Transport: transport},
		},
	})

	_, err := c.InstallUpdate(context.Background(), updater.Manifest{
		Version: "1.2.0",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app.exe",
		SHA256:  strings.Repeat("c", 64),
		Size:    1,
	})
	if err == nil {
		t.Fatal("expected pending restart write error")
	}
	if transport.called {
		t.Fatal("updater download started after pending restart write failed")
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

func createConsoleUpdateSlave(t *testing.T, manager *slave.Manager, id string, status slave.Status) {
	t.Helper()
	folder := filepath.Join(t.TempDir(), id)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	machine, err := manager.Machines.Load()
	if err != nil {
		t.Fatalf("Load machine: %v", err)
	}
	sl, err := manager.Registry.Create(machine, slave.CreateInput{Folder: folder, Name: id})
	if err != nil {
		t.Fatalf("Create slave %s: %v", id, err)
	}
	if _, err := manager.Registry.Update(sl.ID, func(s *slave.Slave) error {
		s.ID = id
		s.Status = status
		return nil
	}); err != nil {
		t.Fatalf("Update slave %s: %v", id, err)
	}
}

type recordingRoundTripper struct {
	called bool
}

func (r *recordingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	r.called = true
	return nil, errors.New("unexpected download")
}
