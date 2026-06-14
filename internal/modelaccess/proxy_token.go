package modelaccess

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const localProxyTokenBytes = 32

func DefaultLocalProxyTokenPath(installRoot string) string {
	return filepath.Join(installRoot, "proxy-token")
}

func EnsureLocalProxyToken(path string) (string, error) {
	if token, err := readLocalProxyToken(path); err == nil {
		return token, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	token, err := generateLocalProxyToken()
	if err != nil {
		return "", err
	}
	if err := writeNewLocalProxyToken(path, token); err != nil {
		if errors.Is(err, os.ErrExist) {
			return readLocalProxyToken(path)
		}
		return "", err
	}
	return token, nil
}

func readLocalProxyToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("modelaccess: local proxy token file is empty: %s", path)
	}
	return token, nil
}

func generateLocalProxyToken() (string, error) {
	var b [localProxyTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func writeNewLocalProxyToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
