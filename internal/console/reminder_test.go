package console

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestReminderThresholdsFireOncePerWindow(t *testing.T) {
	store := NewMemoryReminderStore()
	r := ReminderEngine{Store: store}
	got := r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 49, ResetsAt: "r1"}})
	if len(got) != 0 {
		t.Fatalf("49%% should not notify: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 50, ResetsAt: "r1"}})
	if len(got) != 1 || got[0].Threshold != 50 {
		t.Fatalf("50%% notification missing: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 70, ResetsAt: "r1"}})
	if len(got) != 0 {
		t.Fatalf("same threshold repeated: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 80, ResetsAt: "r1"}})
	if len(got) != 1 || got[0].Threshold != 80 {
		t.Fatalf("80%% notification missing: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 82, ResetsAt: "r1"}})
	if len(got) != 0 {
		t.Fatalf("80%% repeated: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 50, ResetsAt: "r2"}})
	if len(got) != 1 || got[0].Threshold != 50 {
		t.Fatalf("new reset window should notify again: %+v", got)
	}
}

func TestReminderTreatsUsageDropWithoutResetAsNewWindow(t *testing.T) {
	store := NewMemoryReminderStore()
	r := ReminderEngine{Store: store}
	got := r.Evaluate([]QuotaWindow{{Window: "7d", Percentage: 82}})
	if len(got) != 2 {
		t.Fatalf("initial 82%% should notify 50 and 80: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "7d", Percentage: 40}})
	if len(got) != 0 {
		t.Fatalf("drop below threshold should only reset cycle: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "7d", Percentage: 50}})
	if len(got) != 1 || got[0].Threshold != 50 {
		t.Fatalf("new local cycle should notify 50 again: %+v", got)
	}
}

func TestFileReminderStorePersistsSeenState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-notifications.json")
	store := NewFileReminderStore(path)
	store.Mark("5h", "r1", 50)
	store.SetLastPercentage("5h", 58)

	reloaded := NewFileReminderStore(path)
	if !reloaded.Seen("5h", "r1", 50) {
		t.Fatal("seen threshold was not persisted")
	}
	if got, ok := reloaded.LastPercentage("5h"); !ok || got != 58 {
		t.Fatalf("last percentage got %v ok=%v", got, ok)
	}
}

func TestFileReminderStoreReportsCorruptJSONAndClearsAfterSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-notifications.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewFileReminderStore(path)
	if store.LastError() == nil {
		t.Fatal("expected corrupt JSON diagnostic")
	}
	store.Mark("5h", "r1", 50)
	if !store.Seen("5h", "r1", 50) {
		t.Fatal("store was not usable after corrupt JSON")
	}
	if err := store.LastError(); err != nil {
		t.Fatalf("LastError after successful save=%v", err)
	}
}

func TestFileReminderStoreReportsSaveError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-notifications-dir")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	store := NewFileReminderStore(path)
	store.Mark("5h", "r1", 50)
	if store.LastError() == nil {
		t.Fatal("expected save diagnostic")
	}
	if !store.Seen("5h", "r1", 50) {
		t.Fatal("store should keep in-memory update when save fails")
	}
}

func TestReminderStoresConcurrentAccess(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		store := NewMemoryReminderStore()
		runReminderStoreConcurrently(t, store)
	})
	t.Run("file", func(t *testing.T) {
		store := NewFileReminderStore(filepath.Join(t.TempDir(), "console-notifications.json"))
		runReminderStoreConcurrently(t, store)
		if err := store.LastError(); err != nil {
			t.Fatalf("LastError=%v", err)
		}
	})
}

func runReminderStoreConcurrently(t *testing.T, store ReminderStore) {
	t.Helper()
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				window := fmt.Sprintf("w%d", (worker+i)%4)
				resetKey := fmt.Sprintf("r%d", i%3)
				threshold := 50
				if i%2 == 0 {
					threshold = 80
				}
				store.Mark(window, resetKey, threshold)
				_ = store.Seen(window, resetKey, threshold)
				store.SetLastPercentage(window, float64(worker*100+i))
				_, _ = store.LastPercentage(window)
				if i%17 == 0 {
					store.ClearWindow(window)
				}
			}
		}(worker)
	}
	wg.Wait()
}
