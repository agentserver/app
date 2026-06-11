package codexruntime

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
)

func VerifyNPMIntegrity(path, integrity string) error {
	const prefix = "sha512-"
	if !strings.HasPrefix(integrity, prefix) {
		return fmt.Errorf("unsupported npm integrity %q", integrity)
	}
	want, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(integrity, prefix))
	if err != nil {
		return fmt.Errorf("decode npm integrity: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := h.Sum(nil); !bytes.Equal(got, want) {
		return fmt.Errorf("npm integrity mismatch for %s", path)
	}
	return nil
}
