package codexruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureInstallsPinnedRuntimeFromFirstGoodMirror(t *testing.T) {
	pkg := runtimePackage(t, "codex")
	integrity := npmIntegrity(pkg)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pkg)
	}))
	defer srv.Close()

	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, Manifest{
		Package:       "@openai/codex",
		Platform:      "win32-x64",
		PinnedVersion: "0.136.0-win32-x64",
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		CodexExe:      "bin/codex.exe",
		RequiredFiles: requiredRuntimeFiles(),
		Pinned:        PinnedPackage{Integrity: integrity, URLs: []string{srv.URL + "/codex.tgz"}},
	})
	res, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     filepath.Join(dir, "root"),
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.136.0-win32-x64" || res.Source != "pinned" {
		t.Fatalf("result=%+v", res)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "root", "bin", "codex.exe")); err != nil || string(got) != "codex" {
		t.Fatalf("codex.exe=%q err=%v", got, err)
	}
}

func TestEnsureFallsBackToLatestWhenPinnedReturns404(t *testing.T) {
	pkg := runtimePackage(t, "latest-codex")
	integrity := npmIntegrity(pkg)
	mux := http.NewServeMux()
	mux.HandleFunc("/pinned.tgz", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"optionalDependencies":{"@openai/codex-win32-x64":"npm:@openai/codex@0.139.0-win32-x64"}}`))
	})
	mux.HandleFunc("/pkg/0.139.0-win32-x64", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"dist":{"tarball":"%s/latest.tgz","integrity":"%s"}}`, "http://"+r.Host, integrity)
	})
	mux.HandleFunc("/latest.tgz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(pkg)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, Manifest{
		Package:                     "@openai/codex",
		Platform:                    "win32-x64",
		PinnedVersion:               "0.136.0-win32-x64",
		StripPrefix:                 "vendor/x86_64-pc-windows-msvc/",
		CodexExe:                    "bin/codex.exe",
		RequiredFiles:               requiredRuntimeFiles(),
		Pinned:                      PinnedPackage{Integrity: "sha512-missing", URLs: []string{srv.URL + "/pinned.tgz"}},
		LatestMetadataURLs:          []string{srv.URL + "/latest"},
		PackageMetadataURLTemplates: []string{srv.URL + "/pkg/{version}"},
	})
	res, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     filepath.Join(dir, "root"),
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.139.0-win32-x64" || res.Source != "latest" {
		t.Fatalf("result=%+v", res)
	}
}

func TestEnsureTriesSecondPinnedMirrorAfterFirstHTTPError(t *testing.T) {
	pkg := runtimePackage(t, "codex-from-second")
	integrity := npmIntegrity(pkg)
	firstHits := 0
	secondHits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/first.tgz", func(w http.ResponseWriter, r *http.Request) {
		firstHits++
		http.Error(w, "mirror error", http.StatusInternalServerError)
	})
	mux.HandleFunc("/second.tgz", func(w http.ResponseWriter, r *http.Request) {
		secondHits++
		w.Write(pkg)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, Manifest{
		Package:       "@openai/codex",
		Platform:      "win32-x64",
		PinnedVersion: "0.136.0-win32-x64",
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		CodexExe:      "bin/codex.exe",
		RequiredFiles: requiredRuntimeFiles(),
		Pinned: PinnedPackage{
			Integrity: integrity,
			URLs:      []string{srv.URL + "/first.tgz", srv.URL + "/second.tgz"},
		},
	})
	res, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     filepath.Join(dir, "root"),
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "pinned" || firstHits != 1 || secondHits != 1 {
		t.Fatalf("result=%+v firstHits=%d secondHits=%d", res, firstHits, secondHits)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "root", "bin", "codex.exe")); err != nil || string(got) != "codex-from-second" {
		t.Fatalf("codex.exe=%q err=%v", got, err)
	}
}

func TestEnsureAbortsStalledDownloadAfterIdleTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-release
	}))
	defer srv.Close()
	defer close(release)

	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, Manifest{
		Package:       "@openai/codex",
		Platform:      "win32-x64",
		PinnedVersion: "0.136.0-win32-x64",
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		CodexExe:      "bin/codex.exe",
		RequiredFiles: requiredRuntimeFiles(),
		Pinned:        PinnedPackage{Integrity: "sha512-unused", URLs: []string{srv.URL + "/stalled.tgz"}},
	})
	_, err := Ensure(context.Background(), Options{
		ManifestPath:        manifestPath,
		DestRoot:            filepath.Join(dir, "root"),
		CacheDir:            filepath.Join(dir, "cache"),
		DownloadIdleTimeout: 20 * time.Millisecond,
		VersionCommand:      func(context.Context, string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "download idle timeout") {
		t.Fatalf("err=%v, want download idle timeout", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "cache", "codex-0.136.0-win32-x64.tgz.part")); !os.IsNotExist(statErr) {
		t.Fatalf("stale partial should be removed after timeout, stat err=%v", statErr)
	}
}

func TestEnsureSkipsWhenRuntimeAlreadyWorks(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	for _, rel := range requiredRuntimeFiles() {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath := writeManifest(t, dir, Manifest{
		Package:       "@openai/codex",
		Platform:      "win32-x64",
		PinnedVersion: "0.136.0-win32-x64",
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		CodexExe:      "bin/codex.exe",
		RequiredFiles: requiredRuntimeFiles(),
		Pinned:        PinnedPackage{Integrity: "sha512-unused", URLs: []string{"https://unused.invalid/codex.tgz"}},
	})
	calls := 0
	res, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     root,
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) error {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped || calls != 1 {
		t.Fatalf("result=%+v calls=%d", res, calls)
	}
}

func TestEnsureRejectsIntegrityMismatch(t *testing.T) {
	pkg := runtimePackage(t, "codex")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pkg)
	}))
	defer srv.Close()

	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, Manifest{
		Package:       "@openai/codex",
		Platform:      "win32-x64",
		PinnedVersion: "0.136.0-win32-x64",
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		CodexExe:      "bin/codex.exe",
		RequiredFiles: requiredRuntimeFiles(),
		Pinned:        PinnedPackage{Integrity: npmIntegrity([]byte("different")), URLs: []string{srv.URL + "/codex.tgz"}},
	})
	_, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     filepath.Join(dir, "root"),
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) error {
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("err=%v, want integrity mismatch", err)
	}
}

func writeManifest(t *testing.T, dir string, m Manifest) string {
	t.Helper()
	path := filepath.Join(dir, "codex-manifest.json")
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func requiredRuntimeFiles() []string {
	return []string{
		"bin/codex.exe",
		"codex-path/rg.exe",
		"codex-resources/codex-command-runner.exe",
		"codex-resources/codex-windows-sandbox-setup.exe",
	}
}

func runtimePackage(t *testing.T, codexBody string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		"package/vendor/x86_64-pc-windows-msvc/bin/codex.exe":                                   "codex",
		"package/vendor/x86_64-pc-windows-msvc/codex-path/rg.exe":                               "rg",
		"package/vendor/x86_64-pc-windows-msvc/codex-resources/codex-command-runner.exe":        "runner",
		"package/vendor/x86_64-pc-windows-msvc/codex-resources/codex-windows-sandbox-setup.exe": "sandbox",
	}
	files["package/vendor/x86_64-pc-windows-msvc/bin/codex.exe"] = codexBody
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func npmIntegrity(body []byte) string {
	sum := sha512.Sum512(body)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}
