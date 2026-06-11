package slave

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidMachineIdentity = errors.New("invalid machine identity")

type Machine struct {
	MachineID    string `json:"machine_id"`
	ComputerName string `json:"computer_name"`
}

type MachineStore struct {
	path string
}

func NewMachineStore(path string) *MachineStore {
	return &MachineStore{path: path}
}

func (s *MachineStore) Load() (Machine, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Machine{}, os.ErrNotExist
	}
	if err != nil {
		return Machine{}, fmt.Errorf("read machine: %w", err)
	}
	var m Machine
	if err := json.Unmarshal(b, &m); err != nil {
		return Machine{}, fmt.Errorf("%w: parse machine: %v", ErrInvalidMachineIdentity, err)
	}
	if strings.TrimSpace(m.MachineID) == "" || strings.TrimSpace(m.ComputerName) == "" {
		return Machine{}, fmt.Errorf("%w: machine identity incomplete", ErrInvalidMachineIdentity)
	}
	return m, nil
}

func (s *MachineStore) Ensure(computerName string) (Machine, error) {
	if existing, err := s.Load(); err == nil {
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		if !errors.Is(err, ErrInvalidMachineIdentity) {
			return Machine{}, err
		}
		name, nameErr := normalizeComputerName(computerName)
		if nameErr != nil {
			return Machine{}, nameErr
		}
		if backupErr := s.backupInvalid(); backupErr != nil && !errors.Is(backupErr, os.ErrNotExist) {
			return Machine{}, fmt.Errorf("backup invalid machine identity: %w", backupErr)
		}
		return s.create(name)
	}
	name, err := normalizeComputerName(computerName)
	if err != nil {
		return Machine{}, err
	}
	return s.create(name)
}

func normalizeComputerName(computerName string) (string, error) {
	name := strings.TrimSpace(computerName)
	if name == "" {
		return "", fmt.Errorf("computer name required")
	}
	return name, nil
}

func (s *MachineStore) create(name string) (Machine, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return Machine{}, fmt.Errorf("generate machine id: %w", err)
	}
	m := Machine{MachineID: id.String(), ComputerName: name}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return Machine{}, fmt.Errorf("mkdir machine dir: %w", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Machine{}, fmt.Errorf("marshal machine: %w", err)
	}
	if err := publishMachineFile(s.path, append(b, '\n')); errors.Is(err, os.ErrExist) {
		return s.Load()
	} else if err != nil {
		return Machine{}, err
	}
	return m, nil
}

func (s *MachineStore) backupInvalid() error {
	dir := filepath.Dir(s.path)
	base := filepath.Base(s.path)
	backup := filepath.Join(dir, fmt.Sprintf("%s.bad-%d-%d", base, os.Getpid(), time.Now().UTC().UnixNano()))
	return os.Rename(s.path, backup)
}

func publishMachineFile(path string, b []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".machine-*.tmp")
	if err != nil {
		return fmt.Errorf("create machine temp: %w", err)
	}
	tmpPath := f.Name()
	defer func() {
		_ = os.Remove(tmpPath)
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
		return fmt.Errorf("chmod machine temp: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		_ = closeTemp()
		return fmt.Errorf("write machine temp: %w", err)
	}
	if err := closeTemp(); err != nil {
		return fmt.Errorf("close machine temp: %w", err)
	}

	// Hardlink publish gives no-overwrite semantics for a fully written same-directory temp file.
	if err := os.Link(tmpPath, path); errors.Is(err, os.ErrExist) {
		return os.ErrExist
	} else if err != nil {
		return fmt.Errorf("publish machine: %w", err)
	}
	return nil
}
