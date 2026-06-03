//go:build darwin

package secrets

import (
	"fmt"
	"os/exec"
	"strings"
)

// keyringAvailable probes the macOS keychain via security(1).
func keyringAvailable() bool {
	_, err := exec.LookPath("security")
	return err == nil
}

type keyringStore struct{}

func (k *keyringStore) Get(key string) (string, error) {
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
	// Delete first to avoid "already exists" errors.
	_ = k.Delete(key)
	return exec.Command(
		"security", "add-generic-password",
		"-s", serviceName,
		"-a", key,
		"-w", value,
	).Run()
}

func (k *keyringStore) Delete(key string) error {
	err := exec.Command(
		"security", "delete-generic-password",
		"-s", serviceName,
		"-a", key,
	).Run()
	if err != nil {
		var exitErr *exec.ExitError
		if fmt.Sprintf("%T", err) == "*exec.ExitError" {
			_ = exitErr
			return nil // Not found is OK
		}
	}
	return err
}
