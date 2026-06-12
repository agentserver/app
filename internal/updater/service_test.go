package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		Client:         assetsHostClient(t, server),
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

func TestServiceCheckReturnsErrorStateOnCorruptPersistedState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(statePath, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("Write corrupt state: %v", err)
	}
	called := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	got, err := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    server.URL,
		State:          NewStateStore(statePath),
		Client:         assetsHostClient(t, server),
	}.Check(context.Background(), false)
	if err == nil {
		t.Fatal("expected corrupt state load error")
	}
	if called {
		t.Fatal("Check fetched manifest despite corrupt persisted state")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if !strings.Contains(got.LastError, "invalid character") {
		t.Fatalf("LastError=%q, want corrupt JSON error", got.LastError)
	}
}

func TestServiceCheckReportsLatestForEqualVersion(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	manifest := Manifest{
		Version: "0.1.1",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.1-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:    123,
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
		Client:         assetsHostClient(t, server),
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

func TestServiceCheckReturnsErrorStateWhenFinalSaveFails(t *testing.T) {
	manifest := Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
		SHA256:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Size:    123,
	}
	stateRoot := filepath.Join(t.TempDir(), "state-root")
	statePath := filepath.Join(stateRoot, "state.json")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := os.Remove(statePath); err != nil {
			t.Fatalf("Remove state file: %v", err)
		}
		if err := os.Remove(stateRoot); err != nil {
			t.Fatalf("Remove state dir: %v", err)
		}
		if err := os.WriteFile(stateRoot, []byte("not a directory"), 0o644); err != nil {
			t.Fatalf("Write state root conflict: %v", err)
		}
		if err := json.NewEncoder(w).Encode(manifest); err != nil {
			t.Fatalf("Encode manifest: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	got, err := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    server.URL,
		State:          NewStateStore(statePath),
		Client:         assetsHostClient(t, server),
	}.Check(context.Background(), false)
	if err == nil {
		t.Fatal("expected final save error")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if !strings.Contains(got.LastError, "state-root") {
		t.Fatalf("LastError=%q, want final save error", got.LastError)
	}
	if !strings.Contains(err.Error(), "state-root") {
		t.Fatalf("error=%q, want final save error", err)
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
		Client:         assetsHostClient(t, server),
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

func TestServiceAutomaticCheckReturnsErrorStateWhenRefreshedStateSaveFails(t *testing.T) {
	now := time.Date(2026, 6, 12, 13, 15, 0, 0, time.UTC)
	saveErr := errors.New("refreshed state save failed")
	called := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	got, err := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    server.URL,
		State: &fakeStateStore{
			loadState: State{
				CurrentVersion: "0.1.0",
				LastCheckedAt:  now.Add(-time.Hour),
				Status:         StatusLatest,
			},
			saveErrs: []error{saveErr, nil},
		},
		Client: assetsHostClient(t, server),
		Now:    func() time.Time { return now },
	}.Check(context.Background(), true)
	if err == nil {
		t.Fatal("expected refreshed state save error")
	}
	if called {
		t.Fatal("automatic check fetched manifest despite throttle path save failure")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if !strings.Contains(got.LastError, saveErr.Error()) {
		t.Fatalf("LastError=%q, want state save error", got.LastError)
	}
}

func TestServiceAutomaticCheckDefaultsToDailyThrottle(t *testing.T) {
	now := time.Date(2026, 6, 12, 13, 30, 0, 0, time.UTC)
	called := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	if err := store.Save(State{
		CurrentVersion: "0.1.0",
		LastCheckedAt:  now.Add(-time.Hour),
		Status:         StatusLatest,
	}); err != nil {
		t.Fatalf("Save prior state: %v", err)
	}

	got, err := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    server.URL,
		State:          store,
		Client:         assetsHostClient(t, server),
		Now:            func() time.Time { return now },
	}.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check automatic: %v", err)
	}
	if called {
		t.Fatal("automatic check made network request despite default daily throttle")
	}
	if got.CurrentVersion != "0.1.1" {
		t.Fatalf("CurrentVersion=%q, want refreshed current version", got.CurrentVersion)
	}
}

func TestServiceCheckReturnsErrorStateWhenCheckingStateSaveFails(t *testing.T) {
	now := time.Date(2026, 6, 12, 13, 45, 0, 0, time.UTC)
	saveErr := errors.New("checking state save failed")
	called := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	got, err := Service{
		CurrentVersion: "0.1.1",
		ManifestURL:    server.URL,
		State:          &fakeStateStore{loadState: State{Status: StatusIdle}, saveErrs: []error{saveErr, nil}},
		Client:         assetsHostClient(t, server),
		Now:            func() time.Time { return now },
	}.Check(context.Background(), false)
	if err == nil {
		t.Fatal("expected checking state save error")
	}
	if called {
		t.Fatal("Check fetched manifest despite checking state save failure")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if !strings.Contains(got.LastError, saveErr.Error()) {
		t.Fatalf("LastError=%q, want checking state save error", got.LastError)
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
	manifest := Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	now := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       cacheDir,
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         assetsHostClient(t, server),
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

func TestServiceDownloadRejectsRedirectToDisallowedHostBeforeRequestingTarget(t *testing.T) {
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)
	redirectedHostRequested := false

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://downloads.example.test/agentserver-app-0.1.2-setup.exe", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	client := assetsHostClient(t, server)
	client.Transport = disallowedRedirectRecordingTransport{
		base: client.Transport,
		onDisallowed: func(req *http.Request) (*http.Response, error) {
			redirectedHostRequested = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Request:    req,
			}, nil
		},
	}

	startCalled := false
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       filepath.Join(t.TempDir(), "cache"),
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         client,
		StartInstaller: func(context.Context, string) error {
			startCalled = true
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	})
	if err == nil {
		t.Fatal("expected redirect host rejection")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if redirectedHostRequested {
		t.Fatal("download requested disallowed redirect target")
	}
	if startCalled {
		t.Fatal("StartInstaller called after disallowed redirect")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("error=%q, want allowlist rejection", err)
	}
}

func TestServiceDownloadStopsAllowedRedirectLoop(t *testing.T) {
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"), http.StatusFound)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       filepath.Join(t.TempDir(), "cache"),
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         assetsHostClient(t, server),
		StartInstaller: func(context.Context, string) error {
			t.Fatal("StartInstaller called after redirect loop")
			return nil
		},
	}.DownloadAndStart(ctx, Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	})
	if err == nil {
		t.Fatal("expected redirect limit error")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("error=%q, want default redirect limit", err)
	}
}

func TestServiceDownloadRejectsNonNewerUpdateBeforeNetwork(t *testing.T) {
	for _, tt := range []struct {
		name          string
		current       string
		updateVersion string
		wantErr       string
	}{
		{name: "equal", current: "0.1.2", updateVersion: "0.1.2", wantErr: "not newer"},
		{name: "older", current: "0.1.3", updateVersion: "0.1.2", wantErr: "not newer"},
		{name: "invalid current", current: "dev", updateVersion: "0.1.2", wantErr: "invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			transport := &recordingInstallerTransport{}
			startCalled := false
			got, err := Service{
				CurrentVersion: tt.current,
				CacheDir:       filepath.Join(t.TempDir(), "cache"),
				State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
				Client:         &http.Client{Transport: transport},
				StartInstaller: func(context.Context, string) error {
					startCalled = true
					return nil
				},
			}.DownloadAndStart(context.Background(), Manifest{
				Version: tt.updateVersion,
				URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
				SHA256:  strings.Repeat("a", 64),
				Size:    1,
			})
			if err == nil {
				t.Fatal("expected non-newer update rejection")
			}
			if got.Status != StatusError {
				t.Fatalf("Status=%q, want %q", got.Status, StatusError)
			}
			if transport.called {
				t.Fatal("download started for non-newer update")
			}
			if startCalled {
				t.Fatal("StartInstaller called for non-newer update")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error=%q, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestServiceDownloadCallsBeforeInstallerStartAfterPromotion(t *testing.T) {
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	var beforePath string
	var startedPath string
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       filepath.Join(t.TempDir(), "cache"),
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         assetsHostClient(t, server),
		BeforeInstallerStart: func(ctx context.Context, m Manifest, installerPath string) error {
			beforePath = installerPath
			if _, err := os.Stat(installerPath); err != nil {
				t.Fatalf("installer was not promoted before callback: %v", err)
			}
			if m.Version != "0.1.2" {
				t.Fatalf("callback manifest version=%q, want 0.1.2", m.Version)
			}
			return nil
		},
		StartInstaller: func(ctx context.Context, path string) error {
			startedPath = path
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	})
	if err != nil {
		t.Fatalf("DownloadAndStart: %v", err)
	}
	if got.Status != StatusInstallerStarted {
		t.Fatalf("Status=%q, want %q", got.Status, StatusInstallerStarted)
	}
	if beforePath == "" {
		t.Fatal("BeforeInstallerStart was not called")
	}
	if startedPath != beforePath {
		t.Fatalf("StartInstaller path=%q, want callback path %q", startedPath, beforePath)
	}
}

func TestServiceDownloadDoesNotStartInstallerWhenBeforeInstallerStartFails(t *testing.T) {
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)
	beforeErr := errors.New("before start failed")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	startCalled := false
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       filepath.Join(t.TempDir(), "cache"),
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         assetsHostClient(t, server),
		BeforeInstallerStart: func(context.Context, Manifest, string) error {
			return beforeErr
		},
		StartInstaller: func(context.Context, string) error {
			startCalled = true
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	})
	if !errors.Is(err, beforeErr) {
		t.Fatalf("error=%v, want %v", err, beforeErr)
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if startCalled {
		t.Fatal("StartInstaller called after BeforeInstallerStart failed")
	}
}

func TestServiceDownloadDoesNotFollowPredictablePartSymlink(t *testing.T) {
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("Mkdir cache: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	outsideBody := []byte("outside original")
	if err := os.WriteFile(outsidePath, outsideBody, 0o644); err != nil {
		t.Fatalf("Write outside file: %v", err)
	}
	oldPartPath := filepath.Join(cacheDir, "agentserver-app-0.1.2-setup.exe.part")
	if err := os.Symlink(outsidePath, oldPartPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       cacheDir,
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         assetsHostClient(t, server),
		StartInstaller: func(ctx context.Context, path string) error {
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	})
	if err != nil {
		t.Fatalf("DownloadAndStart: %v", err)
	}
	if got.Status != StatusInstallerStarted {
		t.Fatalf("Status=%q, want %q", got.Status, StatusInstallerStarted)
	}
	gotOutside, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("Read outside file: %v", err)
	}
	if string(gotOutside) != string(outsideBody) {
		t.Fatalf("outside file was modified: got %q, want %q", gotOutside, outsideBody)
	}
	linkTarget, err := os.Readlink(oldPartPath)
	if err != nil {
		t.Fatalf("Read old predictable part symlink: %v", err)
	}
	if linkTarget != outsidePath {
		t.Fatalf("old predictable part symlink target=%q, want %q", linkTarget, outsidePath)
	}
}

func TestServiceDownloadRejectsZeroSize(t *testing.T) {
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("download should not start for zero manifest size")
	}))
	t.Cleanup(server.Close)
	startCalled := false
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       filepath.Join(t.TempDir(), "cache"),
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         assetsHostClient(t, server),
		StartInstaller: func(ctx context.Context, path string) error {
			startCalled = true
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
	})
	if err == nil {
		t.Fatal("expected zero size error")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if !strings.Contains(err.Error(), "size") {
		t.Fatalf("error=%q, want size error", err)
	}
	if startCalled {
		t.Fatal("StartInstaller called despite zero size")
	}
}

func TestServiceDownloadRejectsSizeMismatch(t *testing.T) {
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       filepath.Join(t.TempDir(), "cache"),
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         assetsHostClient(t, server),
		StartInstaller: func(ctx context.Context, path string) error {
			t.Fatal("StartInstaller should not be called on size mismatch")
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body) + 1),
	})
	if err == nil {
		t.Fatal("expected size mismatch error")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("error=%q, want size mismatch", err)
	}
}

func TestServiceDownloadRejectsOversizedResponseBeforeVerification(t *testing.T) {
	body := []byte("installer bytes")
	declaredBody := body[:len(body)-1]
	sum := sha256.Sum256(declaredBody)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       cacheDir,
		State:          NewStateStore(filepath.Join(t.TempDir(), "state.json")),
		Client:         assetsHostClient(t, server),
		StartInstaller: func(ctx context.Context, path string) error {
			t.Fatal("StartInstaller should not be called on oversized response")
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(declaredBody)),
	})
	if err == nil {
		t.Fatal("expected oversized response error")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if !strings.Contains(err.Error(), "larger than declared size") {
		t.Fatalf("error=%q, want oversized response error", err)
	}
	assertCacheDirEmpty(t, cacheDir)
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
	cacheDir := filepath.Join(t.TempDir(), "cache")
	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       cacheDir,
		State:          store,
		Client:         assetsHostClient(t, server),
		StartInstaller: func(ctx context.Context, path string) error {
			t.Fatal("StartInstaller should not be called on hash mismatch")
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
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
	assertCacheDirEmpty(t, cacheDir)
}

func TestServiceDownloadReturnsStateSaveErrorBeforeInstallerStart(t *testing.T) {
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	stateParentConflict := filepath.Join(t.TempDir(), "state-parent-conflict")
	if err := os.WriteFile(stateParentConflict, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("Write state conflict: %v", err)
	}
	startCalled := false
	_, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       filepath.Join(t.TempDir(), "cache"),
		State:          NewStateStore(filepath.Join(stateParentConflict, "state.json")),
		Client:         assetsHostClient(t, server),
		StartInstaller: func(ctx context.Context, path string) error {
			startCalled = true
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	})
	if err == nil {
		t.Fatal("expected state save error")
	}
	if startCalled {
		t.Fatal("StartInstaller called despite downloading state save failure")
	}
}

func TestServiceErrorStateSaveFailureSurfacesBothErrors(t *testing.T) {
	stateParentConflict := filepath.Join(t.TempDir(), "state-parent-conflict")
	if err := os.WriteFile(stateParentConflict, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("Write state conflict: %v", err)
	}
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       filepath.Join(t.TempDir(), "cache"),
		State:          NewStateStore(filepath.Join(stateParentConflict, "state.json")),
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     "https://assets.agent.cs.ac.cn/agentserver-app/windows/agentserver-app-0.1.2-setup.exe",
		SHA256:  "not-hex",
		Size:    123,
	})
	if err == nil {
		t.Fatal("expected validation and state save errors")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("error=%q, want original validation error", err)
	}
	if !strings.Contains(err.Error(), "state-parent-conflict") {
		t.Fatalf("error=%q, want state save error", err)
	}
}

func TestServiceDownloadReturnsFinalStateSaveError(t *testing.T) {
	body := []byte("installer bytes")
	sum := sha256.Sum256(body)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Fatalf("Write body: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	started := false
	stateRoot := filepath.Join(t.TempDir(), "state-root")
	statePath := filepath.Join(stateRoot, "state.json")
	got, err := Service{
		CurrentVersion: "0.1.1",
		CacheDir:       filepath.Join(t.TempDir(), "cache"),
		State:          NewStateStore(statePath),
		Client:         assetsHostClient(t, server),
		StartInstaller: func(ctx context.Context, path string) error {
			started = true
			if err := os.Remove(statePath); err != nil {
				t.Fatalf("Remove state file: %v", err)
			}
			if err := os.Remove(stateRoot); err != nil {
				t.Fatalf("Remove state dir: %v", err)
			}
			if err := os.WriteFile(stateRoot, []byte("not a directory"), 0o644); err != nil {
				t.Fatalf("Write state root conflict: %v", err)
			}
			return nil
		},
	}.DownloadAndStart(context.Background(), Manifest{
		Version: "0.1.2",
		URL:     assetsInstallerURL("/agentserver-app-0.1.2-setup.exe"),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	})
	if !started {
		t.Fatal("StartInstaller was not called before final save failure")
	}
	if err == nil {
		t.Fatal("expected final save error")
	}
	if got.Status != StatusError {
		t.Fatalf("Status=%q, want %q", got.Status, StatusError)
	}
	if !strings.Contains(got.LastError, "state-root") {
		t.Fatalf("LastError=%q, want final save error", got.LastError)
	}
	if !strings.Contains(err.Error(), "state-root") {
		t.Fatalf("error=%q, want final state save error", err)
	}
}

func assertCacheDirEmpty(t *testing.T, cacheDir string) {
	t.Helper()
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("Read cache dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("cache files remain: %v", names)
	}
}

type fakeStateStore struct {
	loadState State
	loadErr   error
	saveErrs  []error
	saved     []State
}

func (s *fakeStateStore) Load() (State, error) {
	if s.loadErr != nil {
		return State{}, s.loadErr
	}
	return s.loadState, nil
}

func (s *fakeStateStore) Save(state State) error {
	s.saved = append(s.saved, state)
	if len(s.saveErrs) == 0 {
		return nil
	}
	err := s.saveErrs[0]
	s.saveErrs = s.saveErrs[1:]
	return err
}

func assetsInstallerURL(path string) string {
	return "https://" + AssetsHost + path
}

func assetsHostClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := server.Client()
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = assetsHostRewriteTransport{base: base, target: target}
	return client
}

type assetsHostRewriteTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (t assetsHostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.EqualFold(req.URL.Hostname(), AssetsHost) {
		clone := req.Clone(req.Context())
		u := *clone.URL
		u.Scheme = t.target.Scheme
		u.Host = t.target.Host
		clone.URL = &u
		clone.Host = ""
		return t.base.RoundTrip(clone)
	}
	return t.base.RoundTrip(req)
}

type disallowedRedirectRecordingTransport struct {
	base         http.RoundTripper
	onDisallowed func(*http.Request) (*http.Response, error)
}

func (t disallowedRedirectRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.EqualFold(req.URL.Hostname(), AssetsHost) && t.onDisallowed != nil {
		return t.onDisallowed(req)
	}
	return t.base.RoundTrip(req)
}

type recordingInstallerTransport struct {
	called bool
}

func (t *recordingInstallerTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.called = true
	return nil, errors.New("unexpected installer download")
}
