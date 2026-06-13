package codexruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
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
		VersionCommand: func(context.Context, string) (string, error) {
			return "codex-cli 0.136.0", nil
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

func TestEnsureDoesNotFetchUnpinnedMetadataWhenPinnedReturns404(t *testing.T) {
	metadataHits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/pinned.tgz", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/metadata", func(w http.ResponseWriter, r *http.Request) {
		metadataHits++
		w.Write([]byte(`{"optionalDependencies":{"@openai/codex-win32-x64":"npm:@openai/codex@0.139.0-win32-x64"}}`))
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
		Pinned:        PinnedPackage{Integrity: "sha512-missing", URLs: []string{srv.URL + "/pinned.tgz"}},
	})
	_, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     filepath.Join(dir, "root"),
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) (string, error) {
			return "codex-cli 0.136.0", nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("err=%v, want pinned download failure", err)
	}
	if metadataHits != 0 {
		t.Fatalf("unpinned metadata should not be fetched, hits=%d", metadataHits)
	}
}

func TestEnsureInstallsRepoPinnedFallbackWhenPrimaryPinnedUnavailable(t *testing.T) {
	fallbackPkg := runtimePackage(t, "fallback-codex")
	fallbackIntegrity := npmIntegrity(fallbackPkg)
	fallbackHits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/primary.tgz", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/fallback.tgz", func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.Write(fallbackPkg)
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
		Pinned:        PinnedPackage{Integrity: "sha512-missing", URLs: []string{srv.URL + "/primary.tgz"}},
		FallbackPinned: []PinnedPackage{
			{
				Version:   "0.139.0-win32-x64",
				Integrity: fallbackIntegrity,
				URLs:      []string{srv.URL + "/fallback.tgz"},
			},
		},
	})
	res, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     filepath.Join(dir, "root"),
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) (string, error) {
			return "codex-cli 0.139.0", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.139.0-win32-x64" || res.Source != "fallback-pinned" || fallbackHits != 1 {
		t.Fatalf("result=%+v fallbackHits=%d", res, fallbackHits)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "root", "bin", "codex.exe")); err != nil || string(got) != "fallback-codex" {
		t.Fatalf("codex.exe=%q err=%v", got, err)
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
		VersionCommand: func(context.Context, string) (string, error) {
			return "codex-cli 0.136.0", nil
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
		VersionCommand:      func(context.Context, string) (string, error) { return "codex-cli 0.136.0", nil },
	})
	if err == nil || !strings.Contains(err.Error(), "download idle timeout") {
		t.Fatalf("err=%v, want download idle timeout", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "cache", "codex-0.136.0-win32-x64.tgz.part")); !os.IsNotExist(statErr) {
		t.Fatalf("stale partial should be removed after timeout, stat err=%v", statErr)
	}
}

func TestEnsureAbortsHeaderStallAfterTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		Pinned:        PinnedPackage{Integrity: "sha512-unused", URLs: []string{srv.URL + "/headers-stalled.tgz"}},
	})
	_, err := Ensure(context.Background(), Options{
		ManifestPath:          manifestPath,
		DestRoot:              filepath.Join(dir, "root"),
		CacheDir:              filepath.Join(dir, "cache"),
		ResponseHeaderTimeout: 20 * time.Millisecond,
		DownloadIdleTimeout:   time.Second,
		VersionCommand:        func(context.Context, string) (string, error) { return "codex-cli 0.136.0", nil },
	})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err=%v, want response header timeout", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "cache", "codex-0.136.0-win32-x64.tgz.part")); !os.IsNotExist(statErr) {
		t.Fatalf("partial should not be created before response headers, stat err=%v", statErr)
	}
}

func TestEnsureAbortsMirrorAttemptAfterTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
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
		Pinned:        PinnedPackage{Integrity: "sha512-unused", URLs: []string{srv.URL + "/attempt-stalled.tgz"}},
	})
	_, err := Ensure(context.Background(), Options{
		ManifestPath:           manifestPath,
		DestRoot:               filepath.Join(dir, "root"),
		CacheDir:               filepath.Join(dir, "cache"),
		DownloadAttemptTimeout: 20 * time.Millisecond,
		ResponseHeaderTimeout:  time.Second,
		DownloadIdleTimeout:    time.Second,
		VersionCommand:         func(context.Context, string) (string, error) { return "codex-cli 0.136.0", nil },
	})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("err=%v, want attempt context deadline", err)
	}
}

func TestEnsureRejectsDownloadWhenContentLengthExceedsLimit(t *testing.T) {
	oldLimit := maxPackageDownloadBytes
	maxPackageDownloadBytes = 8
	t.Cleanup(func() { maxPackageDownloadBytes = oldLimit })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "9")
		w.WriteHeader(http.StatusOK)
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
		Pinned:        PinnedPackage{Integrity: "sha512-unused", URLs: []string{srv.URL + "/oversized.tgz"}},
	})
	_, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     filepath.Join(dir, "root"),
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) (string, error) {
			return "codex-cli 0.136.0", nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "download size") {
		t.Fatalf("err=%v, want download size limit error", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "cache", "codex-0.136.0-win32-x64.tgz.part")); !os.IsNotExist(statErr) {
		t.Fatalf("stale partial should be removed after oversized response, stat err=%v", statErr)
	}
}

func TestEnsureRejectsDownloadStreamThatExceedsLimit(t *testing.T) {
	oldLimit := maxPackageDownloadBytes
	maxPackageDownloadBytes = 8
	t.Cleanup(func() { maxPackageDownloadBytes = oldLimit })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("123456789"))
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
		Pinned:        PinnedPackage{Integrity: "sha512-unused", URLs: []string{srv.URL + "/oversized-stream.tgz"}},
	})
	_, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     filepath.Join(dir, "root"),
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) (string, error) {
			return "codex-cli 0.136.0", nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "download size") {
		t.Fatalf("err=%v, want download size limit error", err)
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
		VersionCommand: func(context.Context, string) (string, error) {
			calls++
			return "codex-cli 0.136.0", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped || calls != 1 {
		t.Fatalf("result=%+v calls=%d", res, calls)
	}
}

func TestEnsureReinstallsExistingRuntimeWhenVersionDoesNotMatchPinned(t *testing.T) {
	pkg := runtimePackage(t, "new-codex")
	integrity := npmIntegrity(pkg)
	downloads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads++
		w.Write(pkg)
	}))
	defer srv.Close()

	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	for _, rel := range requiredRuntimeFiles() {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
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
		Pinned:        PinnedPackage{Integrity: integrity, URLs: []string{srv.URL + "/codex.tgz"}},
	})
	calls := 0
	res, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     root,
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) (string, error) {
			calls++
			if calls == 1 {
				return "codex-cli 0.135.0", nil
			}
			return "codex-cli 0.136.0", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped || res.Source != "pinned" || downloads != 1 {
		t.Fatalf("result=%+v downloads=%d", res, downloads)
	}
	if got, err := os.ReadFile(filepath.Join(root, "bin", "codex.exe")); err != nil || string(got) != "new-codex" {
		t.Fatalf("codex.exe=%q err=%v", got, err)
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
		VersionCommand: func(context.Context, string) (string, error) {
			return "codex-cli 0.136.0", nil
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
