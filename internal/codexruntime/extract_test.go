package codexruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractRuntimeStripsVendorPrefix(t *testing.T) {
	tgz := testTarGz(t, map[string]string{
		"package/vendor/x86_64-pc-windows-msvc/bin/codex.exe":                                   "codex",
		"package/vendor/x86_64-pc-windows-msvc/codex-path/rg.exe":                               "rg",
		"package/vendor/x86_64-pc-windows-msvc/codex-resources/codex-command-runner.exe":        "runner",
		"package/vendor/x86_64-pc-windows-msvc/codex-resources/codex-windows-sandbox-setup.exe": "sandbox",
		"package/README.md": "ignored",
	})
	dir := t.TempDir()
	err := ExtractRuntime(bytes.NewReader(tgz), dir, ExtractOptions{
		StripPrefix: "vendor/x86_64-pc-windows-msvc/",
		RequiredFiles: []string{
			"bin/codex.exe",
			"codex-path/rg.exe",
			"codex-resources/codex-command-runner.exe",
			"codex-resources/codex-windows-sandbox-setup.exe",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "bin", "codex.exe")); err != nil || string(got) != "codex" {
		t.Fatalf("codex.exe=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("README should not be extracted, err=%v", err)
	}
}

func TestExtractRuntimeRejectsTraversal(t *testing.T) {
	tgz := testTarGz(t, map[string]string{
		"package/vendor/x86_64-pc-windows-msvc/../escape.exe": "escape",
	})
	err := ExtractRuntime(bytes.NewReader(tgz), t.TempDir(), ExtractOptions{
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		RequiredFiles: []string{"bin/codex.exe"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("err=%v, want unsafe path", err)
	}
}

func TestExtractRuntimeRejectsSymlink(t *testing.T) {
	buf := new(bytes.Buffer)
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "package/vendor/x86_64-pc-windows-msvc/bin/codex.exe",
		Typeflag: tar.TypeSymlink,
		Linkname: "../outside.exe",
		Mode:     0o755,
	}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	err := ExtractRuntime(bytes.NewReader(buf.Bytes()), t.TempDir(), ExtractOptions{
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		RequiredFiles: []string{"bin/codex.exe"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported tar entry") {
		t.Fatalf("err=%v, want unsupported tar entry", err)
	}
}

func testTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
