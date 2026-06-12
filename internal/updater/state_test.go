package updater

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStateStoreRoundTripsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "update-state.json")
	store := NewStateStore(path)

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load missing state: %v", err)
	}
	if got.Status != StatusIdle {
		t.Fatalf("missing state status=%q, want %q", got.Status, StatusIdle)
	}

	checkedAt := time.Date(2026, 6, 12, 10, 11, 12, 0, time.UTC)
	want := State{
		CurrentVersion: "0.1.1",
		LastCheckedAt:  checkedAt,
		Status:         StatusAvailable,
		Update: &AvailableUpdate{
			Version: "0.1.2",
			URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
			SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Size:    123,
			Notes:   "release notes",
		},
		LastError: "previous error",
	}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err = store.Load()
	if err != nil {
		t.Fatalf("Load saved state: %v", err)
	}
	if got.CurrentVersion != want.CurrentVersion {
		t.Fatalf("CurrentVersion=%q, want %q", got.CurrentVersion, want.CurrentVersion)
	}
	if !got.LastCheckedAt.Equal(want.LastCheckedAt) {
		t.Fatalf("LastCheckedAt=%s, want %s", got.LastCheckedAt, want.LastCheckedAt)
	}
	if got.Status != want.Status {
		t.Fatalf("Status=%q, want %q", got.Status, want.Status)
	}
	if got.Update == nil || *got.Update != *want.Update {
		t.Fatalf("Update=%+v, want %+v", got.Update, want.Update)
	}
	if got.LastError != want.LastError {
		t.Fatalf("LastError=%q, want %q", got.LastError, want.LastError)
	}
}

func TestStateStoreSaveOverwritesExistingState(t *testing.T) {
	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	if err := store.Save(State{
		CurrentVersion: "0.1.1",
		Status:         StatusLatest,
	}); err != nil {
		t.Fatalf("Save initial state: %v", err)
	}
	if err := store.Save(State{
		CurrentVersion: "0.1.2",
		Status:         StatusAvailable,
		Update: &AvailableUpdate{
			Version: "0.1.3",
		},
	}); err != nil {
		t.Fatalf("Save replacement state: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load replacement state: %v", err)
	}
	if got.CurrentVersion != "0.1.2" {
		t.Fatalf("CurrentVersion=%q, want replacement version", got.CurrentVersion)
	}
	if got.Status != StatusAvailable {
		t.Fatalf("Status=%q, want %q", got.Status, StatusAvailable)
	}
	if got.Update == nil || got.Update.Version != "0.1.3" {
		t.Fatalf("Update=%+v, want replacement update", got.Update)
	}
}

func TestStateJSONOmitsZeroLastCheckedAt(t *testing.T) {
	data, err := json.Marshal(State{Status: StatusIdle})
	if err != nil {
		t.Fatalf("Marshal state: %v", err)
	}
	if strings.Contains(string(data), "last_checked_at") {
		t.Fatalf("state JSON=%s, want no last_checked_at for zero time", data)
	}
}
