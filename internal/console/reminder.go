package console

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Reminder struct {
	Window     string
	Threshold  int
	Percentage float64
}

type ReminderStore interface {
	Seen(window, resetKey string, threshold int) bool
	Mark(window, resetKey string, threshold int)
	LastPercentage(window string) (float64, bool)
	SetLastPercentage(window string, percentage float64)
	ClearWindow(window string)
}

type ReminderEngine struct {
	Store ReminderStore
}

func (r ReminderEngine) Evaluate(windows []QuotaWindow) []Reminder {
	store := r.Store
	if store == nil {
		store = NewMemoryReminderStore()
	}

	var out []Reminder
	for _, w := range windows {
		resetKey := w.ResetsAt
		if resetKey == "" {
			resetKey = "current"
			if last, ok := store.LastPercentage(w.Window); ok && w.Percentage < last {
				store.ClearWindow(w.Window)
			}
			store.SetLastPercentage(w.Window, w.Percentage)
		}
		for _, threshold := range []int{50, 80} {
			if w.Percentage < float64(threshold) || store.Seen(w.Window, resetKey, threshold) {
				continue
			}
			store.Mark(w.Window, resetKey, threshold)
			out = append(out, Reminder{Window: w.Window, Threshold: threshold, Percentage: w.Percentage})
		}
	}
	return out
}

type MemoryReminderStore struct {
	seen map[string]bool
	last map[string]float64
}

func NewMemoryReminderStore() *MemoryReminderStore {
	return &MemoryReminderStore{
		seen: map[string]bool{},
		last: map[string]float64{},
	}
}

func (m *MemoryReminderStore) Seen(window, resetKey string, threshold int) bool {
	return m.seen[reminderKey(window, resetKey, threshold)]
}

func (m *MemoryReminderStore) Mark(window, resetKey string, threshold int) {
	m.seen[reminderKey(window, resetKey, threshold)] = true
}

func (m *MemoryReminderStore) LastPercentage(window string) (float64, bool) {
	v, ok := m.last[window]
	return v, ok
}

func (m *MemoryReminderStore) SetLastPercentage(window string, percentage float64) {
	m.last[window] = percentage
}

func (m *MemoryReminderStore) ClearWindow(window string) {
	prefix := window + "|"
	for key := range m.seen {
		if strings.HasPrefix(key, prefix) {
			delete(m.seen, key)
		}
	}
}

type FileReminderStore struct {
	path string
	mem  *MemoryReminderStore
}

type reminderDiskState struct {
	Seen map[string]bool    `json:"seen"`
	Last map[string]float64 `json:"last"`
}

func NewFileReminderStore(path string) *FileReminderStore {
	f := &FileReminderStore{path: path, mem: NewMemoryReminderStore()}
	f.load()
	return f
}

func (f *FileReminderStore) Seen(window, resetKey string, threshold int) bool {
	return f.mem.Seen(window, resetKey, threshold)
}

func (f *FileReminderStore) Mark(window, resetKey string, threshold int) {
	f.mem.Mark(window, resetKey, threshold)
	f.save()
}

func (f *FileReminderStore) LastPercentage(window string) (float64, bool) {
	return f.mem.LastPercentage(window)
}

func (f *FileReminderStore) SetLastPercentage(window string, percentage float64) {
	f.mem.SetLastPercentage(window, percentage)
	f.save()
}

func (f *FileReminderStore) ClearWindow(window string) {
	f.mem.ClearWindow(window)
	f.save()
}

func (f *FileReminderStore) load() {
	b, err := os.ReadFile(f.path)
	if err != nil {
		return
	}
	var disk reminderDiskState
	if err := json.Unmarshal(b, &disk); err != nil {
		return
	}
	if disk.Seen != nil {
		f.mem.seen = disk.Seen
	}
	if disk.Last != nil {
		f.mem.last = disk.Last
	}
}

func (f *FileReminderStore) save() {
	if f.path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(f.path), 0o755)
	disk := reminderDiskState{Seen: f.mem.seen, Last: f.mem.last}
	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(f.path, b, 0o644)
}

func reminderKey(window, resetKey string, threshold int) string {
	return window + "|" + resetKey + "|" + strconv.Itoa(threshold)
}
