//go:build linux

package secrets

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// keyringAvailable probes the Linux Secret Service via secret-tool.
// If secret-tool is not installed or the bus is unavailable, returns false.
func keyringAvailable() bool {
	_, err := exec.LookPath("secret-tool")
	if err != nil {
		return false
	}
	// Try a harmless lookup; if it exits without a bus error we're good.
	cmd := exec.Command("secret-tool", "lookup", "service", serviceName, "key", "__probe__")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true
	}
	// "No such secret" is a "found the service" outcome.
	if strings.Contains(string(out), "No such secret") {
		return true
	}
	return false
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

	out, err := exec.Command("secret-tool", "lookup", "service", serviceName, "key", key).CombinedOutput()
	if err != nil {
		return "", secretToolLookupError(out, err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func secretToolLookupError(out []byte, err error) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" || strings.Contains(msg, "No such secret") {
		return ErrNotFound
	}
	return fmt.Errorf("secret-tool lookup: %s: %w", msg, err)
}

func (k *keyringStore) Set(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	cmd := exec.Command("secret-tool", "store", "--label="+serviceName+":"+key,
		"service", serviceName, "key", key)
	cmd.Stdin = strings.NewReader(value)
	return cmd.Run()
}

func (k *keyringStore) Delete(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	err := exec.Command("secret-tool", "clear", "service", serviceName, "key", key).Run()
	if err != nil {
		// Treat "no such secret" as success.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
	}
	return err
}
