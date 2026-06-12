package codexruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyNPMIntegrityAcceptsSHA512(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pkg.tgz")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	integrity := "sha512-m3HSJL1i83hdltRq0+o9czGb+8KJDKra4t/3JRlnPKcjI8PZm6XBHXx6zG4UuMXaDEZjR1wuXDre9G9zvN7AQw=="
	if err := VerifyNPMIntegrity(path, integrity); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyNPMIntegrityRejectsMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pkg.tgz")
	if err := os.WriteFile(path, []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := VerifyNPMIntegrity(path, "sha512-m3HSJL1i83hdltRq0+o9czGb+8KJDKra4t/3JRlnPKcjI8PZm6XBHXx6zG4UuMXaDEZjR1wuXDre9G9zvN7AQw==")
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("err=%v, want integrity mismatch", err)
	}
}
