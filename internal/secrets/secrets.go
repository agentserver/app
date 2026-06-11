// Package secrets stores sensitive values in the system keyring,
// falling back to a chmod 600 file under the user state dir.
package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const serviceName = "agentserver-app"

// ErrNotFound is returned by Get when the key does not exist.
var ErrNotFound = errors.New("secret not found")

// Store is the secrets storage interface.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// New returns a Store backed by the system keyring; if unavailable, it
// falls back to a chmod 600 JSON file at fallbackPath.
func New(fallbackPath string) Store {
	if keyringAvailable() {
		return &keyringStore{}
	}
	return newFileStore(fallbackPath)
}

// ---- File fallback ----

type fileStore struct {
	path string
	mu   sync.Mutex
}

func newFileStore(path string) *fileStore {
	return &fileStore{path: path}
}

func (f *fileStore) load() (map[string]string, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("secrets file corrupt: %w", err)
	}
	return m, nil
}

func (f *fileStore) save(m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(f.path, b, 0o600); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(f.path, 0o600)
	}
	return nil
}

func (f *fileStore) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *fileStore) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[key] = value
	return f.save(m)
}

func (f *fileStore) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	delete(m, key)
	return f.save(m)
}

func stat(p string) (os.FileInfo, error) { return os.Stat(p) }
