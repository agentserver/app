//go:build darwin

package secrets

import (
	"errors"
	"os/exec"
	"strings"
	"sync"
)

// keyringAvailable probes the macOS keychain via security(1).
func keyringAvailable() bool {
	_, err := exec.LookPath("security")
	return err == nil
}

type keyringStore struct {
	mu sync.Mutex
}

func newKeyringStore() *keyringStore {
	return &keyringStore{}
}

func (k *keyringStore) Get(key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	out, err := exec.Command(
		"security", "find-generic-password",
		"-s", serviceName,
		"-a", key,
		"-w",
	).Output()
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func (k *keyringStore) Set(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	// Delete first to avoid "already exists" errors.
	_ = k.deleteLocked(key)
	cmd := exec.Command(
		"security", "add-generic-password",
		"-s", serviceName,
		"-a", key,
		"-w", value,
	)
	return cmd.Run()
}

func (k *keyringStore) Delete(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.deleteLocked(key)
}

func (k *keyringStore) deleteLocked(key string) error {
	err := exec.Command(
		"security", "delete-generic-password",
		"-s", serviceName,
		"-a", key,
	).Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil // Not found is OK
		}
	}
	return err
}
