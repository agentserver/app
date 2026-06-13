package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Store struct {
	path    string
	mu      sync.Mutex
	ownerMu sync.Mutex
	owner   uint64
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

var ErrReentrantStoreAccess = errors.New("state store method called reentrantly")

// Load reads state.json. If missing, returns a fresh State. If corrupt,
// renames the bad file to <path>.corrupt-<ts> and returns a fresh State.
func (s *Store) Load() (*State, error) {
	lock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer s.unlock(lock)
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
	st.FrontendMode = NormalizeFrontendMode(st.FrontendMode)
	return &st, nil
}

// Save writes state.json atomically.
func (s *Store) Save(st *State) error {
	lock, err := s.lock()
	if err != nil {
		return err
	}
	defer s.unlock(lock)
	return s.saveLocked(st)
}

func (s *Store) saveLocked(st *State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	st.FrontendMode = NormalizeFrontendMode(st.FrontendMode)
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return writeFile(s.path, b)
}

// Update is a read-modify-write under the store mutex.
// fn must NOT call any Store method (Load/Save/Update) on the same receiver:
// reentrant access returns ErrReentrantStoreAccess.
func (s *Store) Update(fn func(*State) error) error {
	lock, err := s.lock()
	if err != nil {
		return err
	}
	defer s.unlock(lock)
	st, err := s.loadLocked()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	return s.saveLocked(st)
}

func (s *Store) lock() (*stateFileLock, error) {
	gid := currentGoroutineID()
	if gid != 0 {
		s.ownerMu.Lock()
		if s.owner == gid {
			s.ownerMu.Unlock()
			return nil, ErrReentrantStoreAccess
		}
		s.ownerMu.Unlock()
	}
	s.mu.Lock()
	if gid != 0 {
		s.ownerMu.Lock()
		s.owner = gid
		s.ownerMu.Unlock()
	}
	lock, err := acquireStateFileLock(s.path + ".lock")
	if err != nil {
		s.ownerMu.Lock()
		s.owner = 0
		s.ownerMu.Unlock()
		s.mu.Unlock()
		return nil, err
	}
	return lock, nil
}

func (s *Store) unlock(lock *stateFileLock) {
	_ = lock.close()
	s.ownerMu.Lock()
	s.owner = 0
	s.ownerMu.Unlock()
	s.mu.Unlock()
}

func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := strings.Fields(string(buf[:n]))
	if len(fields) < 2 || fields[0] != "goroutine" {
		return 0
	}
	id, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func freshState() *State {
	return &State{
		SchemaVersion: CurrentSchemaVersion,
		InstallID:     uuid.NewString(),
		CreatedAt:     time.Now().UTC(),
		FrontendMode:  NormalizeFrontendMode(FrontendModeCodexDesktop),
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
