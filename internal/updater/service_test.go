package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceCheckReportsAvailableUpdate(t *testing.T) {
	now := time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC)
	manifest := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:    123,
		Notes:   "release notes",
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(manifest); err != nil {
			t.Fatalf("Encode manifest: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	svc := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    server.URL,
		State:          store,
		Client:         server.Client(),
		Now:            func() time.Time { return now },
	}
	got, err := svc.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Status != StatusAvailable {
		t.Fatalf("Status=%q, want %q", got.Status, StatusAvailable)
	}
	if got.CurrentVersion != "0.1.1" {
		t.Fatalf("CurrentVersion=%q, want 0.1.1", got.CurrentVersion)
	}
	if !got.LastCheckedAt.Equal(now) {
		t.Fatalf("LastCheckedAt=%s, want %s", got.LastCheckedAt, now)
	}
	if got.Update == nil {
		t.Fatal("Update is nil")
	}
	if got.Update.Version != manifest.Version || got.Update.URL != manifest.URL || got.Update.SHA256 != manifest.SHA256 || got.Update.Size != manifest.Size || got.Update.Notes != manifest.Notes {
		t.Fatalf("Update=%+v, want manifest fields", got.Update)
	}

	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load saved state: %v", err)
	}
	if saved.Status != StatusAvailable {
		t.Fatalf("saved Status=%q, want %q", saved.Status, StatusAvailable)
	}
}

func TestServiceCheckReportsLatestForEqualVersion(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	manifest := Manifest{
		Version: "0.1.1",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.1-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(manifest); err != nil {
			t.Fatalf("Encode manifest: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	got, err := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    server.URL,
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         server.Client(),
		Now:            func() time.Time { return now },
	}.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Status != StatusLatest {
		t.Fatalf("Status=%q, want %q", got.Status, StatusLatest)
	}
	if got.Update != nil {
		t.Fatalf("Update=%+v, want nil", got.Update)
	}
	if got.LastError != "" {
		t.Fatalf("LastError=%q, want empty", got.LastError)
	}
}

func TestServiceAutomaticCheckSkipsWhenRecentlyChecked(t *testing.T) {
	now := time.Date(2026, 6, 12, 13, 0, 0, 0, time.UTC)
	called := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	prior := State{
		CurrentVersion: "0.1.0",
		LastCheckedAt:  now.Add(-10 * time.Minute),
		Status:         StatusLatest,
	}
	if err := store.Save(prior); err != nil {
		t.Fatalf("Save prior state: %v", err)
	}

	got, err := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    server.URL,
		State:          store,
		Client:         server.Client(),
		Now:            func() time.Time { return now },
		AutoCheckEvery: time.Hour,
	}.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check automatic: %v", err)
	}
	if called {
		t.Fatal("automatic check made network request despite recent LastCheckedAt")
	}
	if got.CurrentVersion != "0.1.1" {
		t.Fatalf("CurrentVersion=%q, want refreshed current version", got.CurrentVersion)
	}
	if got.Status != prior.Status {
		t.Fatalf("Status=%q, want prior %q", got.Status, prior.Status)
	}
}

func TestServiceDownloadVerifiesSHA256AndStartsInstaller(t *testing.T) {
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)
	var installerPath string
	var startCalled bool

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	allowTestInstallerHost(t, server.URL)

	manifest := Manifest{
		Version: "0.1.2",
		URL:     server.URL + "/agentserver-app-0.1.2-setup.exe",
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	now := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       cacheDir,
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         server.Client(),
		Now:            func() time.Time { return now },
		StartInstaller: func(ctx context.Context, path string) error {
			startCalled = true
			installerPath = path
			return nil
		},
	}.DownloadAndStart(context.Background(), manifest)
	if err != nil {
		t.Fatalf("DownloadAndStart: %v", err)
	}
	if got.Status != StatusInstallerStarted {
		t.Fatalf("Status=%q, want %q", got.Status, StatusInstallerStarted)
	}
	if !startCalled {
		t.Fatal("StartInstaller was not called")
	}
	if installerPath == "" {
		t.Fatal("installer path is empty")
	}
	if !strings.HasPrefix(installerPath, cacheDir) {
		t.Fatalf("installerPath=%q, want inside %q", installerPath, cacheDir)
	}
	if filepath.Ext(installerPath) != ".exe" {
		t.Fatalf("installerPath=%q, want .exe extension", installerPath)
	}
	gotBody, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatalf("Read installer: %v", err)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("installer body=%q, want %q", gotBody, body)
	}
}

func TestServiceDownloadDeletesPartOnHashMismatch(t *testing.T) {
	body := []byte("installer bytes")
	wrong := sha256.Sum256([]byte("different bytes"))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	allowTestInstallerHost(t, server.URL)

	cacheDir := filepath.Join(t.TempDir(), "cache")
	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       cacheDir,
		State:          store,
		Client:         server.Client(),
		StartInstaller: func(ctx context.Context, path string) error {
			t.Fatal("StartInstaller should not be called on hash mismatch")
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     server.URL + "/agentserver-app-0.1.2-setup.exe",
		SHA256:  hex.EncodeToString(wrong[:]),
		Size:    int64(len(body)),
	})
	if err == nil {
		t.Fatal("expected hash mismatch error")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if got.LastError == "" {
		t.Fatal("LastError is empty")
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, "*.part"))
	if err != nil {
		t.Fatalf("Glob part files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("part files remain: %v", matches)
	}
}

func allowTestInstallerHost(t *testing.T, rawURL string) {
	t.Helper()
	u, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	host, _, err := net.SplitHostPort(u.URL.Host)
	if err != nil {
		host = u.URL.Hostname()
	}
	host = strings.ToLower(host)
	extraAllowedInstallerHosts[host] = true
	t.Cleanup(func() {
		delete(extraAllowedInstallerHosts, host)
	})
}
