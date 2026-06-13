package slave

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWritePendingRestartsRecordsEligibleStatusesInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "pending-slave-restarts.json")
	now := time.Date(2026, 6, 12, 10, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	slaves := []Slave{
		{ID: "stopped", Status: StatusStopped},
		{ID: "running", Status: StatusRunning},
		{ID: "paused", Status: StatusPaused},
		{ID: "starting", Status: StatusStarting},
		{ID: "error", Status: StatusError},
		{ID: "auth", Status: StatusAuthRequired},
		{ID: "unknown", Status: Status("unknown")},
	}

	err := WritePendingRestarts(path, "0.1.2", slaves, func() time.Time { return now })
	if err != nil {
		t.Fatalf("WritePendingRestarts: %v", err)
	}

	got, err := ReadPendingRestarts(path)
	if err != nil {
		t.Fatalf("ReadPendingRestarts: %v", err)
	}
	if got.Reason != "app_update" {
		t.Fatalf("Reason=%q, want app_update", got.Reason)
	}
	if got.Version != "0.1.2" {
		t.Fatalf("Version=%q, want 0.1.2", got.Version)
	}
	if want := []string{"running", "starting", "auth"}; !reflect.DeepEqual(got.SlaveIDs, want) {
		t.Fatalf("SlaveIDs=%v, want %v", got.SlaveIDs, want)
	}
	if !got.CreatedAt.Equal(now.UTC()) {
		t.Fatalf("CreatedAt=%s, want %s", got.CreatedAt, now.UTC())
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt location=%v, want UTC", got.CreatedAt.Location())
	}
}

func TestWritePendingRestartsRemovesStaleFileWhenNoEligibleSlaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")
	if err := os.WriteFile(path, []byte(`{"slave_ids":["stale"]}`), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	slaves := []Slave{
		{ID: "paused", Status: StatusPaused},
		{ID: "stopped", Status: StatusStopped},
		{ID: "error", Status: StatusError},
		{ID: "unknown", Status: Status("unknown")},
	}

	if err := WritePendingRestarts(path, "0.1.2", slaves, nil); err != nil {
		t.Fatalf("WritePendingRestarts: %v", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending file exists after ineligible-only write: %v", err)
	}
}

func TestWritePendingRestartsReplacesExistingPendingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")
	if err := os.WriteFile(path, []byte(`{"reason":"stale","version":"old","created_at":"2026-01-01T00:00:00Z","slave_ids":["stale"]}`), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	err := WritePendingRestarts(path, "0.1.2", []Slave{{ID: "fresh", Status: StatusRunning}}, func() time.Time {
		return time.Unix(20, 0)
	})
	if err != nil {
		t.Fatalf("WritePendingRestarts: %v", err)
	}

	got, err := ReadPendingRestarts(path)
	if err != nil {
		t.Fatalf("ReadPendingRestarts: %v", err)
	}
	if got.Reason != "app_update" || got.Version != "0.1.2" {
		t.Fatalf("pending metadata=%+v, want app_update 0.1.2", got)
	}
	if want := []string{"fresh"}; !reflect.DeepEqual(got.SlaveIDs, want) {
		t.Fatalf("SlaveIDs=%v, want %v", got.SlaveIDs, want)
	}
}

func TestWritePendingRestartsLeavesNoFileWhenNoEligibleSlaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")

	if err := WritePendingRestarts(path, "0.1.2", nil, nil); err != nil {
		t.Fatalf("WritePendingRestarts: %v", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending file exists after empty write: %v", err)
	}
}

func TestRestorePendingRestartsRestartsEveryRecordedSlaveAndDeletesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")
	err := writePendingRestartFile(path, PendingRestarts{
		Reason:    "app_update",
		Version:   "0.1.2",
		CreatedAt: time.Unix(10, 0).UTC(),
		SlaveIDs:  []string{"a", "", "b"},
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
	if want := []string{"a", "b"}; !reflect.DeepEqual(restarted, want) {
		t.Fatalf("restarted=%v, want %v", restarted, want)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending file exists after restore: %v", err)
	}
}

func TestRestorePendingRestartsIgnoresPendingFileAlreadyRemovedAfterSuccessfulRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")
	if err := writePendingRestartFile(path, PendingRestarts{
		Reason:    "app_update",
		Version:   "0.1.2",
		CreatedAt: time.Unix(10, 0).UTC(),
		SlaveIDs:  []string{"a"},
	}); err != nil {
		t.Fatalf("write pending: %v", err)
	}

	err := RestorePendingRestarts(context.Background(), path, func(context.Context, string) error {
		return os.Remove(path)
	})

	if err != nil {
		t.Fatalf("RestorePendingRestarts: %v", err)
	}
}

func TestRestorePendingRestartsKeepsFailedIDsForRetryAndDropsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")
	createdAt := time.Unix(10, 0).UTC()
	if err := writePendingRestartFile(path, PendingRestarts{
		Reason:    "app_update",
		Version:   "0.1.2",
		CreatedAt: createdAt,
		SlaveIDs:  []string{"a", "missing", "b", "c"},
	}); err != nil {
		t.Fatalf("write pending: %v", err)
	}
	errB := errors.New("restart b failed")
	errC := errors.New("restart c failed")

	var restarted []string
	err := RestorePendingRestarts(context.Background(), path, func(_ context.Context, id string) error {
		restarted = append(restarted, id)
		switch id {
		case "missing":
			return os.ErrNotExist
		case "b":
			return errB
		case "c":
			return errC
		default:
			return nil
		}
	})

	if want := []string{"a", "missing", "b", "c"}; !reflect.DeepEqual(restarted, want) {
		t.Fatalf("restarted=%v, want %v", restarted, want)
	}
	if !errors.Is(err, errB) || !errors.Is(err, errC) {
		t.Fatalf("RestorePendingRestarts error=%v, want joined b and c errors", err)
	}
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), os.ErrNotExist.Error()) {
		t.Fatalf("RestorePendingRestarts error=%v, should not include os.ErrNotExist", err)
	}
	pending, readErr := ReadPendingRestarts(path)
	if readErr != nil {
		t.Fatalf("ReadPendingRestarts after failed restore: %v", readErr)
	}
	if pending.Reason != "app_update" || pending.Version != "0.1.2" || !pending.CreatedAt.Equal(createdAt) {
		t.Fatalf("pending metadata=%+v", pending)
	}
	if want := []string{"b", "c"}; !reflect.DeepEqual(pending.SlaveIDs, want) {
		t.Fatalf("pending slave IDs=%v, want %v", pending.SlaveIDs, want)
	}
}

func TestRestorePendingRestartsMissingFileReturnsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")

	err := RestorePendingRestarts(context.Background(), path, func(context.Context, string) error {
		t.Fatal("restart callback should not be called")
		return nil
	})

	if err != nil {
		t.Fatalf("RestorePendingRestarts: %v", err)
	}
}

func TestRestorePendingRestartsNilCallbackWithIDsReturnsErrorAndKeepsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-slave-restarts.json")
	if err := writePendingRestartFile(path, PendingRestarts{SlaveIDs: []string{"a"}}); err != nil {
		t.Fatalf("write pending: %v", err)
	}

	err := RestorePendingRestarts(context.Background(), path, nil)

	if err == nil {
		t.Fatal("RestorePendingRestarts returned nil, want error")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("pending file was not kept: %v", statErr)
	}
}

func TestWindowsReplaceFileUsesMoveFileExWithReplaceAndWriteThrough(t *testing.T) {
	text := readPackageSourceFile(t, "replace_file_windows.go")
	for _, want := range []string{
		"windows.MoveFileEx",
		"windows.MOVEFILE_REPLACE_EXISTING",
		"windows.MOVEFILE_WRITE_THROUGH",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("replace_file_windows.go does not contain %q", want)
		}
	}
}
