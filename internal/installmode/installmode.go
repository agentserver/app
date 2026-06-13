package installmode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/state"
)

type fileShape struct {
	FrontendMode state.FrontendMode `json:"frontend_mode"`
}

func Read(path string) (state.FrontendMode, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state.FrontendModeCodexDesktop, nil
	}
	if err != nil {
		return state.FrontendModeCodexDesktop, fmt.Errorf("read install mode: %w", err)
	}
	b = bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
	var f fileShape
	if err := json.Unmarshal(b, &f); err != nil {
		return state.FrontendModeCodexDesktop, fmt.Errorf("parse install mode: %w", err)
	}
	return state.NormalizeFrontendMode(f.FrontendMode), nil
}

func Write(path string, mode state.FrontendMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir install mode dir: %w", err)
	}
	b, err := json.MarshalIndent(fileShape{FrontendMode: state.NormalizeFrontendMode(mode)}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal install mode: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write install mode: %w", err)
	}
	return nil
}

func SyncStore(store *state.Store, path string) error {
	mode, err := Read(path)
	if err != nil {
		return err
	}
	return store.Update(func(s *state.State) error {
		s.FrontendMode = state.NormalizeFrontendMode(mode)
		return nil
	})
}

func SyncStoreIfPresent(store *state.Store, path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat install mode: %w", err)
	}
	return SyncStore(store, path)
}

func PathForExecutable(exe string) string {
	return filepath.Join(filepath.Dir(exe), "install-mode.json")
}

func PathFromExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return PathForExecutable(exe), nil
}
