//go:build !linux && !windows && !darwin

package secrets

// On unsupported platforms the keyring is never available;
// New() will always fall back to the file store.
func keyringAvailable() bool { return false }

type keyringStore struct{}

func (k *keyringStore) Get(_ string) (string, error)      { return "", ErrNotFound }
func (k *keyringStore) Set(_, _ string) error             { return ErrNotFound }
func (k *keyringStore) Delete(_ string) error             { return nil }
