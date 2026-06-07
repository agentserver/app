package console

import (
	"path/filepath"
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
