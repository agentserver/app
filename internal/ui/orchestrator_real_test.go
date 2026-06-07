package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
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

func TestConfigureVSCodeCopiesBundledCodexBeforeDownloading(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte("#!/bin/bash\nexit 0\n"), 0o755)

	codexSrvHits := 0
	codexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		codexSrvHits++
		http.Error(w, "should not download when bundled codex exists", http.StatusInternalServerError)
	}))
	defer codexSrv.Close()

	store := state.NewStore(filepath.Join(dir, "state.json"))
	store.Update(func(s *state.State) error {
		s.VSCode.Path = codeExe
		return nil
	})

	vsix := filepath.Join(dir, "stub.vsix")
	os.WriteFile(vsix, []byte("PK\x03\x04stub"), 0o644)
	bundledCodex := filepath.Join(dir, "install", "codex")
	os.MkdirAll(filepath.Dir(bundledCodex), 0o755)
	os.WriteFile(bundledCodex, []byte("bundled-codex"), 0o644)
	codexPath := filepath.Join(dir, "bin", "codex")
	os.MkdirAll(filepath.Dir(codexPath), 0o755)
	os.WriteFile(codexPath+".part", []byte("partial-download"), 0o644)
	os.WriteFile(codexPath+".meta", []byte("{}"), 0o644)

	r := &realOrchestrator{d: Deps{
		State:             store,
		CodexAbsPath:      codexPath,
		BundledCodexPath:  bundledCodex,
		CodexDownloadURL:  codexSrv.URL + "/codex",
		VSCodeUserDataDir: filepath.Join(dir, "data"),
		VSCodeExtDir:      filepath.Join(dir, "ext"),
		EmbeddedVSIXPath:  vsix,
		CodexConfigPath:   filepath.Join(dir, "codex-config.toml"),
	}}

	if err := r.ConfigureVSCode(context.Background()); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if got, err := os.ReadFile(codexPath); err != nil {
		t.Errorf("codex not copied: %v", err)
	} else if string(got) != "bundled-codex" {
		t.Errorf("codex content=%q, want bundled-codex", got)
	}
	if _, err := os.Stat(codexPath + ".part"); !os.IsNotExist(err) {
		t.Errorf("stale partial download should be removed, err=%v", err)
	}
	if _, err := os.Stat(codexPath + ".meta"); !os.IsNotExist(err) {
		t.Errorf("stale download metadata should be removed, err=%v", err)
	}
	if codexSrvHits != 0 {
		t.Fatalf("download server was hit %d times; bundled codex should avoid network", codexSrvHits)
	}
}

func TestConfigureVSCodeSetsCurrentProcessOpenAIAPIKey(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte("#!/bin/bash\nexit 0\n"), 0o755)
	vsix := filepath.Join(dir, "stub.vsix")
	os.WriteFile(vsix, []byte("PK\x03\x04stub"), 0o644)
	codexPath := filepath.Join(dir, "bin", "codex")
	os.MkdirAll(filepath.Dir(codexPath), 0o755)
	os.WriteFile(codexPath, []byte("codex"), 0o755)
	t.Setenv("OPENAI_API_KEY", "")

	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Set("modelserver_api_key", "fake-token-for-current-process"); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(filepath.Join(dir, "state.json"))
	store.Update(func(s *state.State) error {
		s.VSCode.Path = codeExe
		return nil
	})
	r := &realOrchestrator{d: Deps{
		State:             store,
		Secrets:           sec,
		CodexAbsPath:      codexPath,
		VSCodeUserDataDir: filepath.Join(dir, "data"),
		VSCodeExtDir:      filepath.Join(dir, "ext"),
		EmbeddedVSIXPath:  vsix,
		CodexConfigPath:   filepath.Join(dir, "codex-config.toml"),
	}}

	if err := r.ConfigureVSCode(context.Background()); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "fake-token-for-current-process" {
		t.Fatalf("OPENAI_API_KEY=%q, want current process token", got)
	}
}

func TestConfigureVSCodeDetectsVSCodeWhenStatePathMissing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses PATH code stub")
	}
	dir := t.TempDir()
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte("#!/bin/bash\necho 1.96.0\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	vsix := filepath.Join(dir, "stub.vsix")
	os.WriteFile(vsix, []byte("PK\x03\x04stub"), 0o644)
	codexPath := filepath.Join(dir, "bin", "codex")
	os.MkdirAll(filepath.Dir(codexPath), 0o755)
	os.WriteFile(codexPath, []byte("codex"), 0o755)

	store := state.NewStore(filepath.Join(dir, "state.json"))
	r := &realOrchestrator{d: Deps{
		State:             store,
		CodexAbsPath:      codexPath,
		VSCodeUserDataDir: filepath.Join(dir, "data"),
		VSCodeExtDir:      filepath.Join(dir, "ext"),
		EmbeddedVSIXPath:  vsix,
		CodexConfigPath:   filepath.Join(dir, "codex-config.toml"),
	}}

	if err := r.ConfigureVSCode(context.Background()); err != nil {
		t.Fatalf("configure: %v", err)
	}
	s, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.VSCode.Path != codeExe {
		t.Fatalf("VSCode.Path=%q, want detected %q", s.VSCode.Path, codeExe)
	}
	if s.VSCode.Version != "1.96.0" {
		t.Fatalf("VSCode.Version=%q, want 1.96.0", s.VSCode.Version)
	}
	if !s.Onboarding.HasCompleted("vscode_installed") {
		t.Fatalf("vscode_installed not marked complete")
	}
	if !s.Onboarding.HasCompleted("vscode_configured") {
		t.Fatalf("vscode_configured not marked complete")
	}
}

func TestLaunchAndShutdownInjectsOpenAIAPIKeyAndLocale(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	envFile := filepath.Join(dir, "env.txt")
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte(fmt.Sprintf(`#!/bin/bash
printf '%%s\n' "$@" > %q
printf '%%s\n' "$OPENAI_API_KEY" > %q
exit 0
`, argsFile, envFile)), 0o755)

	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Set("modelserver_api_key", "launch-token"); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(filepath.Join(dir, "state.json"))
	store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeMinimalVSCode
		s.VSCode.Path = codeExe
		return nil
	})
	r := &realOrchestrator{d: Deps{
		State:             store,
		Secrets:           sec,
		VSCodeUserDataDir: filepath.Join(dir, "data"),
		VSCodeExtDir:      filepath.Join(dir, "ext"),
	}}

	if err := r.LaunchAndShutdown(context.Background()); err != nil {
		t.Fatalf("launch: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(envFile); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	envBody, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(envBody)) != "launch-token" {
		t.Fatalf("OPENAI_API_KEY child env = %q", envBody)
	}
	argsBody, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBody)), "\n")
	if !containsSequence(args, "--locale", "zh-cn") {
		t.Fatalf("launch args missing --locale zh-cn: %v", args)
	}
}

func containsSequence(items []string, first, second string) bool {
	for i := 0; i+1 < len(items); i++ {
		if items[i] == first && items[i+1] == second {
			return true
		}
	}
	return false
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

	if _, err := r.LoginModelserver(context.Background()); err != nil {
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

	// Fake modelserver: serves Hydra /oauth2/token and project resolution.
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "authorization_code" ||
			r.PostForm.Get("code") != "code-abc" ||
			r.PostForm.Get("code_verifier") == "" {
			t.Errorf("/oauth2/token bad form: %v", r.PostForm)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-at","token_type":"Bearer","refresh_token":"fake-rt","expires_in":3600}`))
	})
	mux.HandleFunc("/api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("/api/v1/projects got method %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fake-at" {
			t.Errorf("/api/v1/projects auth %q", got)
		}
		w.Write([]byte(`{"data":[{"id":"proj-1","name":"default"}]}`))
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

	if _, err := r.LoginModelserver(context.Background()); err != nil {
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
	if key.Secret != "fake-at" {
		t.Errorf("key.Secret = %q, want access_token", key.Secret)
	}
	if got, _ := sec.Get("modelserver_api_key"); got != "fake-at" {
		t.Errorf("secret not stored: %q", got)
	}
	if got, _ := sec.Get("modelserver_refresh_token"); got != "fake-rt" {
		t.Errorf("refresh token not stored: %q", got)
	}
	if got, _ := sec.Get("modelserver_access_token_expires_at"); got == "" {
		t.Errorf("access token expiry not stored")
	}
	s, _ := store.Load()
	if s.Modelserver.ProjectID != "proj-1" {
		t.Errorf("project id = %q, want proj-1", s.Modelserver.ProjectID)
	}
	if s.Modelserver.APIKeySuffix != "e-at" {
		t.Errorf("key suffix = %q, want last 4 of 'fake-at'", s.Modelserver.APIKeySuffix)
	}
	if !s.Onboarding.HasCompleted("modelserver_login") {
		t.Errorf("step not marked completed")
	}
}

func TestPollModelserverLoginRequiresProjectIDBeforeCompletion(t *testing.T) {
	port := freeUIPort(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-at","refresh_token":"fake-rt","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	r := &realOrchestrator{d: Deps{
		State:   store,
		Secrets: secrets.New(filepath.Join(dir, "secrets.json")),
		MS:      modelserver.New(fake.URL),
		MSOAuth: oauth.AuthCodeConfig{
			Endpoint: fake.URL, AuthPath: "/oauth2/auth", TokenPath: "/oauth2/token",
			ClientID: "client-x", CallbackPath: "/oauth/modelserver/callback",
			Ports: []int{port}, LoginTimeout: 3 * time.Second,
		},
		OpenBrowser: func(string) {},
	}}
	if _, err := r.LoginModelserver(context.Background()); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = http.Get(fmt.Sprintf("http://127.0.0.1:%d/oauth/modelserver/callback?code=x&state=%s", port, r.msSession.State))
	}()
	if _, err := r.PollModelserverLogin(context.Background()); err == nil {
		t.Fatal("expected project resolution error")
	}
	s, _ := store.Load()
	if s.Onboarding.HasCompleted("modelserver_login") {
		t.Fatal("modelserver_login should not complete without ProjectID")
	}
}

func jwtWithASWorkspace(t *testing.T, workspaceID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, err := json.Marshal(map[string]string{"workspace_id": workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestPollAgentserverLoginStoresWorkspaceID(t *testing.T) {
	accessToken := jwtWithASWorkspace(t, "ws-claim")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"` + accessToken + `","token_type":"Bearer","expires_in":3600}`))
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	r := &realOrchestrator{d: Deps{
		State:   store,
		Secrets: sec,
		AS:      agentserver.New(fake.URL),
		ASOAuth: oauth.Config{Endpoint: fake.URL, TokenPath: "/api/oauth2/token", ClientID: "client-x"},
	}}
	r.asChallenge = oauth.DeviceCodeChallenge{DeviceCode: "dev", ExpiresIn: 30, Interval: 1}
	key, err := r.PollAgentserverLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if key.Secret == "" {
		t.Fatal("secret missing")
	}
	if got, _ := sec.Get("agentserver_ws_api_key"); got != accessToken {
		t.Fatalf("agentserver_ws_api_key=%q, want access token", got)
	}
	s, _ := store.Load()
	if s.Agentserver.WorkspaceID != "ws-claim" {
		t.Fatalf("WorkspaceID=%q, want ws-claim", s.Agentserver.WorkspaceID)
	}
	if !s.Onboarding.HasCompleted("agentserver_login") {
		t.Fatal("agentserver_login not completed")
	}
}

func TestPollAgentserverLoginRequiresWorkspaceID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"opaque-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/workspaces", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	r := &realOrchestrator{d: Deps{
		State:   store,
		Secrets: secrets.New(filepath.Join(dir, "secrets.json")),
		AS:      agentserver.New(fake.URL),
		ASOAuth: oauth.Config{Endpoint: fake.URL, TokenPath: "/api/oauth2/token", ClientID: "client-x"},
	}}
	r.asChallenge = oauth.DeviceCodeChallenge{DeviceCode: "dev", ExpiresIn: 30, Interval: 1}
	if _, err := r.PollAgentserverLogin(context.Background()); err == nil {
		t.Fatal("expected workspace resolution error")
	}
	s, _ := store.Load()
	if s.Onboarding.HasCompleted("agentserver_login") {
		t.Fatal("agentserver_login should not complete without WorkspaceID")
	}
}

// TestPollModelserverLogin_SurvivesLoginCtxCancel is a regression test for the
// bug fixed in 7b5a7e7: StartListening was receiving the HTTP request context
// (loginCtx) instead of context.Background(). As soon as the POST handler
// returned and cancelled loginCtx, the listener's internal timeout (derived
// from it) was also cancelled, closing the result channel. A subsequent
// PollModelserverLogin would then receive a closed channel and return
// "登录会话已结束".
//
// This test reproduces the scenario: LoginModelserver is called with a
// cancellable context that is cancelled immediately (simulating the POST
// handler returning), and the callback arrives only after the cancellation.
// Poll must still succeed.
func TestPollModelserverLogin_SurvivesLoginCtxCancel(t *testing.T) {
	port := freeUIPort(t)

	// Fake modelserver — same endpoints as TestPollModelserverLogin_FullPKCE.
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "authorization_code" ||
			r.PostForm.Get("code") != "code-regression" ||
			r.PostForm.Get("code_verifier") == "" {
			t.Errorf("/oauth2/token bad form: %v", r.PostForm)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-at","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(201)
			w.Write([]byte(`{"data":{"id":"proj-reg","name":"default"}}`))
			return
		}
		w.Write([]byte(`{"data":[]}`))
	})
	mux.HandleFunc("/api/v1/projects/proj-reg/keys", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte(`{"data":{"id":"k2","key_suffix":"abcd"},"key":"ms-fakekey-xxx"}`))
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	cfg := oauth.AuthCodeConfig{
		Endpoint:     fake.URL,
		AuthPath:     "/oauth2/auth",
		TokenPath:    "/oauth2/token",
		ClientID:     "client-reg",
		Scope:        "project:inference offline_access",
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{port},
		LoginTimeout: 5 * time.Second,
	}

	dir := t.TempDir()
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	store := state.NewStore(filepath.Join(dir, "state.json"))

	r := &realOrchestrator{d: Deps{
		State:       store,
		Secrets:     sec,
		MS:          modelserver.New(fake.URL),
		MSOAuth:     cfg,
		OpenBrowser: func(string) {}, // no-op
	}}

	// Use a cancellable context — this simulates the HTTP POST handler's ctx.
	loginCtx, cancel := context.WithCancel(context.Background())

	if _, err := r.LoginModelserver(loginCtx); err != nil {
		t.Fatalf("LoginModelserver: %v", err)
	}

	// Cancel the login context immediately, simulating the POST handler
	// returning. If StartListening used this ctx, the listener would die here.
	cancel()

	// Capture state before the goroutine reads it to avoid a data race.
	state_ := r.msSession.State

	// Simulate the browser hitting the callback *after* loginCtx is cancelled.
	go func() {
		// Small delay so PollModelserverLogin reaches its select first.
		time.Sleep(50 * time.Millisecond)
		callbackURL := fmt.Sprintf("http://127.0.0.1:%d/oauth/modelserver/callback?code=code-regression&state=%s",
			port, state_)
		_, _ = http.Get(callbackURL)
	}()

	// Poll must succeed — not return "登录会话已结束".
	key, err := r.PollModelserverLogin(context.Background())
	if err != nil {
		t.Fatalf("PollModelserverLogin: %v (bug regression: listener must outlive login ctx)", err)
	}
	// New behavior: PKCE access_token is the secret directly.
	if key.Secret != "fake-at" {
		t.Errorf("key.Secret = %q, want access_token", key.Secret)
	}
}

// TestLoginModelserver_RetryReleasesPreviousListener verifies that calling
// LoginModelserver twice in a row on a single-port config does NOT exhaust
// ports — the second call must release the first listener before binding.
func TestLoginModelserver_RetryReleasesPreviousListener(t *testing.T) {
	port := freeUIPort(t)

	cfg := oauth.AuthCodeConfig{
		Endpoint:     "https://hydra.example",
		AuthPath:     "/oauth2/auth",
		TokenPath:    "/oauth2/token",
		ClientID:     "client-x",
		Scope:        "project:inference offline_access",
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{port}, // single port — second login MUST reuse it
		LoginTimeout: 30 * time.Second,
	}

	dir := t.TempDir()
	r := &realOrchestrator{d: Deps{
		State:       state.NewStore(filepath.Join(dir, "state.json")),
		Secrets:     secrets.New(filepath.Join(dir, "secrets.json")),
		MSOAuth:     cfg,
		OpenBrowser: func(string) {},
	}}

	if _, err := r.LoginModelserver(context.Background()); err != nil {
		t.Fatalf("first login: %v", err)
	}
	firstSession := r.msSession
	if firstSession == nil {
		t.Fatal("first login did not store session")
	}

	// Without the fix, this second call would fail with ErrAllPortsBusy
	// because the first listener still holds the only port.
	// Allow a brief moment for the OS to release the socket: shutdown() is
	// synchronous, but a racing watcher goroutine may hold the FD a few ms.
	var retryErr error
	for i := 0; i < 10; i++ {
		if _, retryErr = r.LoginModelserver(context.Background()); retryErr == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if retryErr != nil {
		t.Fatalf("second login (retry): %v", retryErr)
	}
	if r.msSession == firstSession {
		t.Error("second login did not start a new session — same pointer")
	}
	if r.msSession == nil {
		t.Fatal("second login did not store session")
	}

	// Cleanup
	r.cleanupMS()
}

// TestAbortReleasesListener verifies that Abort() releases the in-flight
// PKCE listener (so the port becomes immediately available for a fresh login).
func TestAbortReleasesListener(t *testing.T) {
	port := freeUIPort(t)

	cfg := oauth.AuthCodeConfig{
		Endpoint:     "https://hydra.example",
		AuthPath:     "/oauth2/auth",
		TokenPath:    "/oauth2/token",
		ClientID:     "client-x",
		Scope:        "project:inference offline_access",
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{port},
		LoginTimeout: 30 * time.Second,
	}

	dir := t.TempDir()
	r := &realOrchestrator{d: Deps{
		State:       state.NewStore(filepath.Join(dir, "state.json")),
		Secrets:     secrets.New(filepath.Join(dir, "secrets.json")),
		MSOAuth:     cfg,
		OpenBrowser: func(string) {},
	}}

	if _, err := r.LoginModelserver(context.Background()); err != nil {
		t.Fatalf("login: %v", err)
	}
	if r.msSession == nil {
		t.Fatal("login did not store session")
	}

	if err := r.Abort(context.Background()); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if r.msSession != nil || r.msShutdown != nil {
		t.Errorf("Abort did not clean up: msSession=%v msShutdown=non-nil",
			r.msSession)
	}
	// Port should be re-bindable now. Allow a brief moment for the OS to
	// fully release the socket (http.Server.Shutdown is synchronous but a
	// racing watcher goroutine may hold the FD a few ms longer).
	var loginErr error
	for i := 0; i < 10; i++ {
		if _, loginErr = r.LoginModelserver(context.Background()); loginErr == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if loginErr != nil {
		t.Errorf("login after Abort should reuse port: %v", loginErr)
	}
	r.cleanupMS()
}

func TestEnsureFrontendCodexDesktopSkipsVSCode(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := &realOrchestrator{d: Deps{
		State: store,
		CodexDesktopEnsure: func(ctx context.Context) (codexdesktop.Detected, error) {
			return codexdesktop.Detected{Installed: true, Version: "9.9.9"}, nil
		},
	}}
	if err := r.EnsureFrontend(context.Background(), nil); err != nil {
		t.Fatalf("EnsureFrontend: %v", err)
	}
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("codex_desktop_installed") {
		t.Fatalf("codex_desktop_installed not marked")
	}
	if s.VSCode.Path != "" {
		t.Fatalf("VSCode.Path should remain empty in Codex Desktop mode, got %q", s.VSCode.Path)
	}
}

func TestConfigureCodexDesktopWritesSharedConfigOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "")
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Set("modelserver_api_key", "desktop-token"); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := &realOrchestrator{d: Deps{
		State:           store,
		Secrets:         sec,
		CodexConfigPath: filepath.Join(dir, ".codex", "config.toml"),
	}}
	if err := r.ConfigureFrontend(context.Background()); err != nil {
		t.Fatalf("ConfigureFrontend: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `model_provider = "modelserver"`) {
		t.Fatalf("config missing modelserver provider:\n%s", b)
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "desktop-token" {
		t.Fatalf("OPENAI_API_KEY=%q", got)
	}
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("codex_desktop_configured") {
		t.Fatalf("codex_desktop_configured not marked")
	}
}

func TestLaunchAndShutdownCodexDesktopUsesDeepLink(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var opened string
	r := &realOrchestrator{d: Deps{
		State: store,
		CodexDesktopOpen: func(url string) error {
			opened = url
			return nil
		},
	}}
	if err := r.LaunchAndShutdown(context.Background()); err != nil {
		t.Fatalf("LaunchAndShutdown: %v", err)
	}
	if opened != "codex://threads/new" {
		t.Fatalf("opened=%q", opened)
	}
}

// TestLoginAgentserver_ReturnsURL verifies the new oauth_url contract:
// LoginAgentserver returns the verification_uri_complete from the
// device-auth response so the front-end can render a fallback link.
func TestLoginAgentserver_ReturnsURL(t *testing.T) {
	// Fake agentserver: respond to /api/oauth2/device/auth with a known URL.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/device/auth", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"device_code": "dev-xyz",
			"user_code": "ABCDEFGH",
			"verification_uri": "https://agent.example/oauth2/device/verify",
			"verification_uri_complete": "https://agent.example/oauth2/device/verify?user_code=ABCDEFGH",
			"expires_in": 600,
			"interval": 5
		}`))
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	browserCh := make(chan string, 1)
	openBrowser := func(u string) { browserCh <- u }

	dir := t.TempDir()
	r := &realOrchestrator{d: Deps{
		State:   state.NewStore(filepath.Join(dir, "state.json")),
		Secrets: secrets.New(filepath.Join(dir, "secrets.json")),
		ASOAuth: oauth.Config{
			Endpoint:  fake.URL,
			AuthPath:  "/api/oauth2/device/auth",
			TokenPath: "/api/oauth2/token",
			ClientID:  "test-client",
			Scope:     "openid",
		},
		OpenBrowser: openBrowser,
	}}

	url, err := r.LoginAgentserver(context.Background())
	if err != nil {
		t.Fatalf("LoginAgentserver: %v", err)
	}
	want := "https://agent.example/oauth2/device/verify?user_code=ABCDEFGH"
	if url != want {
		t.Errorf("returned url = %q, want %q", url, want)
	}
	// Browser-open is async; wait for the goroutine to deliver the URL.
	select {
	case openedURL := <-browserCh:
		if openedURL != want {
			t.Errorf("OpenBrowser called with %q, want %q", openedURL, want)
		}
	case <-time.After(time.Second):
		t.Error("OpenBrowser was not called within 1s")
	}
}
