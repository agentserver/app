package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load reads state.json. If missing, returns a fresh State. If corrupt,
// renames the bad file to <path>.corrupt-<ts> and returns a fresh State.
func (s *Store) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (*State, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return freshState(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil || st.SchemaVersion == 0 {
		backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().Unix())
		_ = os.Rename(s.path, backup)
		return freshState(), nil
	}
	return &st, nil
}

// Save writes state.json atomically.
func (s *Store) Save(st *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(st)
}

func (s *Store) saveLocked(st *State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return writeFile(s.path, b)
}

// Update is a read-modify-write under the store mutex.
// fn must NOT call any Store method (Load/Save/Update) on the same receiver:
// the mutex is non-reentrant and the goroutine will deadlock.
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	return s.saveLocked(st)
}

func freshState() *State {
	return &State{
		SchemaVersion: CurrentSchemaVersion,
		InstallID:     uuid.NewString(),
		CreatedAt:     time.Now().UTC(),
		Onboarding:    OnboardingState{Status: StatusPending},
		Modelserver:   ModelserverState{BaseURL: "https://code.cs.ac.cn"},
		Agentserver:   AgentserverState{BaseURL: "https://agent.cs.ac.cn"},
	}
}

// writeFile atomically writes b to path via tmp + rename.
func writeFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("rename tmp to %s: %w", path, err)
	}
	return nil
}
