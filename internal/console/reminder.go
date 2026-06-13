package console

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	mu   sync.RWMutex
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.seen[reminderKey(window, resetKey, threshold)]
}

func (m *MemoryReminderStore) Mark(window, resetKey string, threshold int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen[reminderKey(window, resetKey, threshold)] = true
}

func (m *MemoryReminderStore) LastPercentage(window string) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.last[window]
	return v, ok
}

func (m *MemoryReminderStore) SetLastPercentage(window string, percentage float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last[window] = percentage
}

func (m *MemoryReminderStore) ClearWindow(window string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := window + "|"
	for key := range m.seen {
		if strings.HasPrefix(key, prefix) {
			delete(m.seen, key)
		}
	}
}

type FileReminderStore struct {
	mu      sync.Mutex
	path    string
	mem     *MemoryReminderStore
	lastErr error
}

type reminderDiskState struct {
	Seen map[string]bool    `json:"seen"`
	Last map[string]float64 `json:"last"`
}

func NewFileReminderStore(path string) *FileReminderStore {
	f := &FileReminderStore{path: path, mem: NewMemoryReminderStore()}
	f.loadLocked()
	return f
}

func (f *FileReminderStore) LastError() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastErr
}

func (f *FileReminderStore) Seen(window, resetKey string, threshold int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mem.seen[reminderKey(window, resetKey, threshold)]
}

func (f *FileReminderStore) Mark(window, resetKey string, threshold int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mem.seen[reminderKey(window, resetKey, threshold)] = true
	f.saveLocked()
}

func (f *FileReminderStore) LastPercentage(window string) (float64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.mem.last[window]
	return v, ok
}

func (f *FileReminderStore) SetLastPercentage(window string, percentage float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mem.last[window] = percentage
	f.saveLocked()
}

func (f *FileReminderStore) ClearWindow(window string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := window + "|"
	for key := range f.mem.seen {
		if strings.HasPrefix(key, prefix) {
			delete(f.mem.seen, key)
		}
	}
	f.saveLocked()
}

func (f *FileReminderStore) loadLocked() {
	if f.path == "" {
		return
	}
	b, err := os.ReadFile(f.path)
	if err != nil {
		if !os.IsNotExist(err) {
			f.lastErr = err
		}
		return
	}
	var disk reminderDiskState
	if err := json.Unmarshal(b, &disk); err != nil {
		f.lastErr = err
		return
	}
	if disk.Seen != nil {
		f.mem.seen = disk.Seen
	}
	if disk.Last != nil {
		f.mem.last = disk.Last
	}
	f.lastErr = nil
}

func (f *FileReminderStore) saveLocked() {
	if f.path == "" {
		f.lastErr = nil
		return
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		f.lastErr = err
		return
	}
	disk := reminderDiskState{Seen: f.mem.seen, Last: f.mem.last}
	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		f.lastErr = err
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.path), filepath.Base(f.path)+".*.tmp")
	if err != nil {
		f.lastErr = err
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		f.lastErr = err
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		f.lastErr = err
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		f.lastErr = err
		return
	}
	if err := os.Rename(tmpPath, f.path); err != nil {
		_ = os.Remove(tmpPath)
		f.lastErr = err
		return
	}
	f.lastErr = nil
}

func reminderKey(window, resetKey string, threshold int) string {
	return window + "|" + resetKey + "|" + strconv.Itoa(threshold)
}
