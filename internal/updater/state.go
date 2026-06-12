package updater

import (
	"encoding/json"
	"errors"
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
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return State{Status: StatusIdle}, nil
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.Status == "" {
		state.Status = StatusIdle
	}
	return state, nil
}

func (s *StateStore) Save(state State) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tmpPath, s.path); err != nil {
		return err
	}
	ok = true
	return nil
}
