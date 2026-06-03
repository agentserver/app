package ui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func TestConfigureVSCodeWritesSettings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	// fake code that just records args
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte("#!/bin/bash\nexit 0\n"), 0o755)

	// fake codex download server (avoid hitting real GitHub for 246MB)
	fakeCodexBody := []byte("fake-codex-binary-body")
	codexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "22")
		w.Write(fakeCodexBody)
	}))
	defer codexSrv.Close()

	store := state.NewStore(filepath.Join(dir, "state.json"))
	store.Update(func(s *state.State) error {
		s.VSCode.Path = codeExe
		s.VSCode.UserDataDir = filepath.Join(dir, "data")
		s.VSCode.ExtensionsDir = filepath.Join(dir, "ext")
		return nil
	})
	// embedded vsix stub file
	vsix := filepath.Join(dir, "stub.vsix")
	os.WriteFile(vsix, []byte("PK\x03\x04stub"), 0o644)

	codexPath := filepath.Join(dir, "bin", "codex")
	r := &realOrchestrator{d: Deps{
		State:             store,
		CodexAbsPath:      codexPath,
		CodexDownloadURL:  codexSrv.URL + "/codex",
		VSCodeUserDataDir: filepath.Join(dir, "data"),
		VSCodeExtDir:      filepath.Join(dir, "ext"),
		EmbeddedVSIXPath:  vsix,
		CodexConfigPath:   filepath.Join(dir, "codex-config.toml"),
	}}
	if err := r.ConfigureVSCode(context.Background()); err != nil {
		t.Fatalf("configure: %v", err)
	}
	settings := filepath.Join(dir, "data", "User", "settings.json")
	if _, err := os.Stat(settings); err != nil {
		t.Errorf("settings not written: %v", err)
	}
	// codex.exe got downloaded to the right place
	if got, err := os.ReadFile(codexPath); err != nil {
		t.Errorf("codex not downloaded: %v", err)
	} else if string(got) != string(fakeCodexBody) {
		t.Errorf("codex content mismatch: got %q", got)
	}
	// Second call should be a no-op (codex already present); no re-download
	if err := r.ConfigureVSCode(context.Background()); err != nil {
		t.Fatalf("re-configure: %v", err)
	}
}

// EnsureVSCode unit test is light because the real path needs Windows;
// here we just exercise the early-return when VS Code is already installed.
func TestEnsureVSCode_AlreadyInstalled(t *testing.T) {
	dir := t.TempDir()
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte("#!/bin/bash\necho 1.96.0\n"), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	store := state.NewStore(filepath.Join(dir, "state.json"))
	r := &realOrchestrator{d: Deps{State: store}}
	if err := r.EnsureVSCode(context.Background(), nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("vscode_installed") {
		t.Errorf("step not marked complete")
	}
}

func TestFinalize_NoDepsJustMarksComplete(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	r := &realOrchestrator{d: Deps{State: store}}
	if err := r.Finalize(context.Background()); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	s, _ := store.Load()
	if s.Onboarding.Status != state.StatusComplete {
		t.Errorf("status %q want %q", s.Onboarding.Status, state.StatusComplete)
	}
	if !s.Onboarding.HasCompleted("shortcuts_created") {
		t.Errorf("step not added")
	}
}

// Used by the SSE handler indirectly; keep imports referenced.
var _ = httptest.NewServer
var _ = http.StatusOK

// freeUIPort returns a port that's free *at the moment of call*. Race-prone
// in theory; fine in practice because the orchestrator binds it immediately.
func freeUIPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestLoginModelserver_StartsListenerOpensBrowser(t *testing.T) {
	port := freeUIPort(t)

	var openedURL string
	var openedOnce sync.WaitGroup
	openedOnce.Add(1)
	openBrowser := func(u string) {
		openedURL = u
		openedOnce.Done()
	}

	cfg := oauth.AuthCodeConfig{
		Endpoint:     "https://hydra.example",
		AuthPath:     "/oauth2/auth",
		TokenPath:    "/oauth2/token",
		ClientID:     "5321f7e6-3d79-4ac9-a742-04809dbf9025",
		Scope:        "project:inference offline_access",
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{port},
		LoginTimeout: 2 * time.Second,
	}

	dir := t.TempDir()
	r := &realOrchestrator{d: Deps{
		State:       state.NewStore(filepath.Join(dir, "state.json")),
		Secrets:     secrets.New(filepath.Join(dir, "secrets.json")),
		MSOAuth:     cfg,
		OpenBrowser: openBrowser,
	}}

	if err := r.LoginModelserver(context.Background()); err != nil {
		t.Fatalf("LoginModelserver: %v", err)
	}
	defer r.cleanupMS()

	// Browser should have been invoked async with the auth URL.
	done := make(chan struct{})
	go func() { openedOnce.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OpenBrowser was not called")
	}
	u, _ := url.Parse(openedURL)
	q := u.Query()
	if q.Get("client_id") != "5321f7e6-3d79-4ac9-a742-04809dbf9025" {
		t.Errorf("client_id missing or wrong: %q", openedURL)
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if !strings.HasPrefix(q.Get("redirect_uri"), fmt.Sprintf("http://127.0.0.1:%d/oauth/modelserver/callback", port)) {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
}

func TestPollModelserverLogin_FullPKCE(t *testing.T) {
	port := freeUIPort(t)

	// Fake modelserver: serves both Hydra /oauth2/token and the
	// admin /api/v1/projects + /api/v1/projects/{id}/keys.
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "authorization_code" ||
			r.PostForm.Get("code") != "code-abc" ||
			r.PostForm.Get("code_verifier") == "" {
			t.Errorf("/oauth2/token bad form: %v", r.PostForm)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-at","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(201)
			w.Write([]byte(`{"data":{"id":"proj-1","name":"default"}}`))
			return
		}
		w.Write([]byte(`{"data":[]}`))
	})
	mux.HandleFunc("/api/v1/projects/proj-1/keys", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte(`{"data":{"id":"k1","key_suffix":"wxyz"},"key":"ms-fakekey-xxx"}`))
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	openBrowser := func(string) {} // no-op; we issue the callback directly

	cfg := oauth.AuthCodeConfig{
		Endpoint:     fake.URL,
		AuthPath:     "/oauth2/auth",
		TokenPath:    "/oauth2/token",
		ClientID:     "client-x",
		Scope:        "project:inference offline_access",
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{port},
		LoginTimeout: 3 * time.Second,
	}

	dir := t.TempDir()
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	store := state.NewStore(filepath.Join(dir, "state.json"))

	r := &realOrchestrator{d: Deps{
		State:       store,
		Secrets:     sec,
		MS:          modelserver.New(fake.URL),
		MSOAuth:     cfg,
		OpenBrowser: openBrowser,
	}}

	if err := r.LoginModelserver(context.Background()); err != nil {
		t.Fatalf("LoginModelserver: %v", err)
	}

	// Simulate the browser hitting the callback.
	go func() {
		// Tiny delay so PollModelserverLogin gets to the select first
		// (not strictly required since the channel is buffered).
		time.Sleep(50 * time.Millisecond)
		callbackURL := fmt.Sprintf("http://127.0.0.1:%d/oauth/modelserver/callback?code=code-abc&state=%s",
			port, r.msSession.State)
		_, _ = http.Get(callbackURL)
	}()

	key, err := r.PollModelserverLogin(context.Background())
	if err != nil {
		t.Fatalf("PollModelserverLogin: %v", err)
	}
	if key.Secret != "ms-fakekey-xxx" {
		t.Errorf("key.Secret = %q", key.Secret)
	}
	if got, _ := sec.Get("modelserver_api_key"); got != "ms-fakekey-xxx" {
		t.Errorf("secret not stored: %q", got)
	}
	s, _ := store.Load()
	if s.Modelserver.ProjectID != "proj-1" {
		t.Errorf("project id = %q", s.Modelserver.ProjectID)
	}
	if s.Modelserver.APIKeySuffix != "wxyz" {
		t.Errorf("key suffix = %q", s.Modelserver.APIKeySuffix)
	}
	if !s.Onboarding.HasCompleted("modelserver_login") {
		t.Errorf("step not marked completed")
	}
}
