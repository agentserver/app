package slave

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var osMkdirAll = os.MkdirAll

type Status string

const (
	StatusStopped      Status = "stopped"
	StatusStarting     Status = "starting"
	StatusAuthRequired Status = "auth_required"
	StatusRunning      Status = "running"
	StatusPaused       Status = "paused"
	StatusError        Status = "error"
)

type Slave struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Folder      string    `json:"folder"`
	ConfigPath  string    `json:"config_path"`
	LogPath     string    `json:"log_path"`
	Status      Status    `json:"status"`
	PID         int       `json:"pid,omitempty"`
	AuthURL     string    `json:"auth_url,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateInput struct {
	Folder string
	Name   string
}

var (
	ErrInvalidCreateInput = errors.New("invalid slave create input")
	ErrSlaveConflict      = errors.New("slave conflict")
)

type Registry struct {
	mu        sync.Mutex
	path      string
	slavesDir string
}

func NewRegistry(path, slavesDir string) *Registry {
	return &Registry{path: path, slavesDir: slavesDir}
}

func (r *Registry) storageDir(id string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("slave registry required")
	}
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("slave id required")
	}
	if filepath.IsAbs(id) {
		return "", fmt.Errorf("slave id must be relative: %s", id)
	}
	root, err := filepath.Abs(r.slavesDir)
	if err != nil {
		return "", fmt.Errorf("resolve slaves dir: %w", err)
	}
	dir, err := filepath.Abs(filepath.Join(root, id))
	if err != nil {
		return "", fmt.Errorf("resolve slave storage dir: %w", err)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "", fmt.Errorf("validate slave storage dir: %w", err)
	}
	if rel == "." || rel == "" || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", fmt.Errorf("slave storage dir escapes slaves dir: %s", id)
	}
	return dir, nil
}

func (r *Registry) List() ([]Slave, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	all, err := r.loadLocked()
	if err != nil {
		return nil, err
	}
	return append([]Slave(nil), all...), nil
}

func (r *Registry) Get(id string) (Slave, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	all, err := r.loadLocked()
	if err != nil {
		return Slave{}, err
	}
	for _, sl := range all {
		if sl.ID == id {
			return sl, nil
		}
	}
	return Slave{}, os.ErrNotExist
}

func (r *Registry) Create(machine Machine, in CreateInput) (Slave, error) {
	if strings.TrimSpace(machine.MachineID) == "" || strings.TrimSpace(machine.ComputerName) == "" {
		return Slave{}, fmt.Errorf("machine identity required")
	}
	folderInput := strings.TrimSpace(in.Folder)
	if folderInput == "" {
		return Slave{}, fmt.Errorf("%w: folder required", ErrInvalidCreateInput)
	}
	folder, err := filepath.Abs(folderInput)
	if err != nil {
		return Slave{}, fmt.Errorf("%w: folder required: %v", ErrInvalidCreateInput, err)
	}
	info, err := os.Stat(folder)
	if err != nil {
		return Slave{}, fmt.Errorf("%w: folder unavailable: %w", ErrInvalidCreateInput, err)
	}
	if !info.IsDir() {
		return Slave{}, fmt.Errorf("%w: folder is not a directory: %s", ErrInvalidCreateInput, folder)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = filepath.Base(folder)
	}
	if err := validateSlaveName(name); err != nil {
		return Slave{}, fmt.Errorf("%w: %w", ErrInvalidCreateInput, err)
	}
	displayName := machine.ComputerName + "-" + name
	id, err := uuid.NewRandom()
	if err != nil {
		return Slave{}, fmt.Errorf("generate slave id: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	all, err := r.loadLocked()
	if err != nil {
		return Slave{}, err
	}
	for _, existing := range all {
		if existing.DisplayName == displayName {
			return Slave{}, fmt.Errorf("%w: slave display name already exists: %s", ErrSlaveConflict, displayName)
		}
	}
	now := time.Now().UTC()
	dir := filepath.Join(r.slavesDir, id.String())
	sl := Slave{
		ID:          id.String(),
		Name:        name,
		DisplayName: displayName,
		Folder:      folder,
		ConfigPath:  filepath.Join(dir, "config.yaml"),
		LogPath:     filepath.Join(dir, "logs", "slave.log"),
		Status:      StatusStopped,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	all = append(all, sl)
	if err := r.saveLocked(all); err != nil {
		return Slave{}, err
	}
	return sl, nil
}

func (r *Registry) Update(id string, fn func(*Slave) error) (Slave, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	all, err := r.loadLocked()
	if err != nil {
		return Slave{}, err
	}
	for i := range all {
		if all[i].ID != id {
			continue
		}
		if err := fn(&all[i]); err != nil {
			return Slave{}, err
		}
		all[i].UpdatedAt = time.Now().UTC()
		if err := r.saveLocked(all); err != nil {
			return Slave{}, err
		}
		return all[i], nil
	}
	return Slave{}, os.ErrNotExist
}

func (r *Registry) Delete(id string) (Slave, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	all, err := r.loadLocked()
	if err != nil {
		return Slave{}, err
	}
	for i, sl := range all {
		if sl.ID != id {
			continue
		}
		next := append(all[:i], all[i+1:]...)
		if err := r.saveLocked(next); err != nil {
			return Slave{}, err
		}
		return sl, nil
	}
	return Slave{}, os.ErrNotExist
}

func validateSlaveName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("slave name required")
	}
	if len([]rune(name)) > 20 {
		return fmt.Errorf("slave name must be at most 20 characters")
	}
	if strings.ContainsAny(name, `\\/:*?"<>|`) {
		return fmt.Errorf("slave name contains invalid path characters")
	}
	return nil
}

func (r *Registry) loadLocked() ([]Slave, error) {
	b, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Slave{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read slaves: %w", err)
	}
	var all []Slave
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("parse slaves: %w", err)
	}
	return all, nil
}

func (r *Registry) saveLocked(all []Slave) error {
	if err := osMkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("mkdir slave registry dir: %w", err)
	}
	if err := osMkdirAll(r.slavesDir, 0o755); err != nil {
		return fmt.Errorf("mkdir slaves dir: %w", err)
	}
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal slaves: %w", err)
	}
	dir := filepath.Dir(r.path)
	f, err := os.CreateTemp(dir, ".slaves-*.tmp")
	if err != nil {
		return fmt.Errorf("create slaves temp: %w", err)
	}
	tmpPath := f.Name()
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	closed := false
	closeTemp := func() error {
		if closed {
			return nil
		}
		closed = true
		return f.Close()
	}

	if err := f.Chmod(0o600); err != nil {
		_ = closeTemp()
		return fmt.Errorf("chmod slaves temp: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = closeTemp()
		return fmt.Errorf("write slaves temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = closeTemp()
		return fmt.Errorf("sync slaves temp: %w", err)
	}
	if err := closeTemp(); err != nil {
		return fmt.Errorf("close slaves temp: %w", err)
	}
	if err := replaceFile(tmpPath, r.path); err != nil {
		return fmt.Errorf("publish slaves: %w", err)
	}
	tmpPath = ""
	if err := os.Chmod(r.path, 0o600); err != nil {
		return fmt.Errorf("chmod slaves: %w", err)
	}
	return nil
}
