package ui

import (
	"archive/tar"
	"compress/gzip"
	"context"
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
	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/opencode"
	"github.com/agentserver/agentserver-pkg/internal/opencodedesktop"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/slave"
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

	fakeCodexBody := []byte("fake-codex-binary-body")

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
		CodexManifestPath: filepath.Join(dir, "codex-manifest.json"),
		CodexRuntimeEnsure: func(ctx context.Context, manifestPath, destRoot, cacheDir string) error {
			if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
				return err
			}
			return os.WriteFile(codexPath, fakeCodexBody, 0o755)
		},
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

	runtimeEnsureCalls := 0
	r := &realOrchestrator{d: Deps{
		State:            store,
		CodexAbsPath:     codexPath,
		BundledCodexPath: bundledCodex,
		CodexRuntimeEnsure: func(ctx context.Context, manifestPath, destRoot, cacheDir string) error {
			runtimeEnsureCalls++
			return fmt.Errorf("should not install runtime when bundled codex exists")
		},
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
	if runtimeEnsureCalls != 0 {
		t.Fatalf("runtime installer was called %d times; bundled codex should avoid runtime install", runtimeEnsureCalls)
	}
}

func TestConfigureVSCodeUsesCodexRuntimeInstallerWhenCodexMissing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte("#!/bin/bash\nexit 0\n"), 0o755)
	store := state.NewStore(filepath.Join(dir, "state.json"))
	store.Update(func(s *state.State) error {
		s.VSCode.Path = codeExe
		return nil
	})
	vsix := filepath.Join(dir, "stub.vsix")
	os.WriteFile(vsix, []byte("PK\x03\x04stub"), 0o644)
	codexPath := filepath.Join(dir, "agentserver-app", "bin", "codex.exe")
	calls := 0
	r := &realOrchestrator{d: Deps{
		State:             store,
		CodexAbsPath:      codexPath,
		CodexManifestPath: filepath.Join(dir, "codex-manifest.json"),
		CodexRuntimeEnsure: func(ctx context.Context, manifestPath, destRoot, cacheDir string) error {
			calls++
			if manifestPath == "" || destRoot == "" || cacheDir == "" {
				return fmt.Errorf("missing codex runtime args")
			}
			if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
				return err
			}
			return os.WriteFile(codexPath, []byte("codex"), 0o755)
		},
		VSCodeUserDataDir: filepath.Join(dir, "data"),
		VSCodeExtDir:      filepath.Join(dir, "ext"),
		EmbeddedVSIXPath:  vsix,
		CodexConfigPath:   filepath.Join(dir, "codex-config.toml"),
	}}
	if err := r.ConfigureVSCode(context.Background()); err != nil {
		t.Fatalf("ConfigureVSCode: %v", err)
	}
	if calls != 1 {
		t.Fatalf("codex runtime ensure calls=%d", calls)
	}
}

func TestConfigureVSCodeSetsStableLocalCodexKey(t *testing.T) {
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
	t.Setenv("OPENAI_API_KEY", "old-openai-token")
	t.Setenv(codex.LocalProxyAPIKeyEnv, "")

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
	if got := os.Getenv(codex.LocalProxyAPIKeyEnv); got != codex.LegacyLocalProxyAPIKeyValue {
		t.Fatalf("%s=%q, want stable local key", codex.LocalProxyAPIKeyEnv, got)
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "old-openai-token" {
		t.Fatalf("OPENAI_API_KEY=%q, want unchanged old-openai-token", got)
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

func TestLaunchAndShutdownInjectsStableLocalCodexKeyAndLocale(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	envFile := filepath.Join(dir, "env.txt")
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte(fmt.Sprintf(`#!/bin/bash
printf '%%s\n' "$@" > %q
	printf '%%s\n' "$AGENTSERVER_CODEX_LOCAL_API_KEY" > %q
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
	if strings.TrimSpace(string(envBody)) != codex.LegacyLocalProxyAPIKeyValue {
		t.Fatalf("%s child env = %q", codex.LocalProxyAPIKeyEnv, envBody)
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

func TestFinalizeStartsCompletedConsoleAndShutsDown(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	var started bool
	var shutdown bool
	r := &realOrchestrator{d: Deps{
		State: store,
		StartCompletedConsole: func(context.Context) error {
			started = true
			s, err := store.Load()
			if err != nil {
				return err
			}
			if s.Onboarding.Status != state.StatusComplete {
				t.Fatalf("completed console started before state was complete: %q", s.Onboarding.Status)
			}
			return nil
		},
		Shutdown: func() {
			shutdown = true
		},
	}}

	if err := r.Finalize(context.Background()); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if !started {
		t.Fatal("completed console was not started")
	}
	if !shutdown {
		t.Fatal("onboarding server was not asked to shut down")
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

	// Fake modelserver: serves Hydra /oauth2/token only. Project lookup is
	// postponed until modelserver exposes OAuth-token project context.
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
		t.Fatalf("/api/v1/projects should not be called during modelserver login")
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
	if s.Modelserver.ProjectID != "" {
		t.Errorf("project id = %q, want empty while project lookup is postponed", s.Modelserver.ProjectID)
	}
	if s.Modelserver.APIKeySuffix != "e-at" {
		t.Errorf("key suffix = %q, want last 4 of 'fake-at'", s.Modelserver.APIKeySuffix)
	}
	if !s.Onboarding.HasCompleted("modelserver_login") {
		t.Errorf("step not marked completed")
	}
}

func TestPollModelserverLoginRejectsMissingRefreshToken(t *testing.T) {
	port := freeUIPort(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-at","token_type":"Bearer","expires_in":3600}`))
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

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
		MSOAuth:     cfg,
		OpenBrowser: func(string) {},
	}}

	if _, err := r.LoginModelserver(context.Background()); err != nil {
		t.Fatalf("LoginModelserver: %v", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		callbackURL := fmt.Sprintf("http://127.0.0.1:%d/oauth/modelserver/callback?code=code-no-refresh&state=%s",
			port, r.msSession.State)
		_, _ = http.Get(callbackURL)
	}()

	_, err := r.PollModelserverLogin(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refresh_token") {
		t.Fatalf("PollModelserverLogin err=%v, want missing refresh_token", err)
	}
	if got, err := sec.Get("modelserver_api_key"); err == nil {
		t.Fatalf("access token should not be stored when refresh_token is missing: %q", got)
	}
	s, _ := store.Load()
	if s.Onboarding.HasCompleted("modelserver_login") {
		t.Fatal("modelserver_login should not complete without a refresh token")
	}
}

func TestPollModelserverLoginCompletesWhenProjectLookupUnavailable(t *testing.T) {
	port := freeUIPort(t)
	projectLookupCalled := false
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-at","refresh_token":"fake-rt","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		projectLookupCalled = true
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
	if _, err := r.PollModelserverLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load()
	if s.Modelserver.ProjectID != "" {
		t.Fatalf("project id=%q, want empty while project lookup is postponed", s.Modelserver.ProjectID)
	}
	if !s.Onboarding.HasCompleted("modelserver_login") {
		t.Fatal("modelserver_login should complete while project lookup is postponed")
	}
	if projectLookupCalled {
		t.Fatal("modelserver login should not call /api/v1/projects while project lookup is postponed")
	}
}

// PollModelserverLogin now reads project_id via the OAuth profile endpoint
// (modelserver PR #63) on the proxy gateway, instead of decoding the access
// token locally. This works for both opaque and JWT tokens — no client-side
// JWT parsing required.
func TestPollModelserverLoginReadsProjectIDFromProfileEndpoint(t *testing.T) {
	port := freeUIPort(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"opaque-at","refresh_token":"fake-rt","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("/api/v1/projects must not be called on the admin host during login")
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	profileCalls := 0
	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/api/oauth/profile", func(w http.ResponseWriter, r *http.Request) {
		profileCalls++
		if got := r.Header.Get("Authorization"); got != "Bearer opaque-at" {
			t.Fatalf("profile auth = %q, want Bearer opaque-at", got)
		}
		_, _ = w.Write([]byte(`{"account":{"uuid":"u1","email":"","display_name":"","created_at":""},"project":{"uuid":"proj-from-profile","rate_limit_tier":null,"seat_tier":null,"has_extra_usage_enabled":false,"billing_type":null,"cc_onboarding_flags":{}}}`))
	})
	proxyFake := httptest.NewServer(proxyMux)
	defer proxyFake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	r := &realOrchestrator{d: Deps{
		State:   store,
		Secrets: secrets.New(filepath.Join(dir, "secrets.json")),
		MS:      modelserver.New(fake.URL),
		MSProxy: modelserver.New(proxyFake.URL),
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
	if _, err := r.PollModelserverLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load()
	if s.Modelserver.ProjectID != "proj-from-profile" {
		t.Fatalf("project id=%q, want proj-from-profile", s.Modelserver.ProjectID)
	}
	if profileCalls != 1 {
		t.Fatalf("profile endpoint called %d times, want 1", profileCalls)
	}
	if !s.Onboarding.HasCompleted("modelserver_login") {
		t.Fatal("modelserver_login should complete")
	}
}

// Login must NOT fail if /api/oauth/profile is temporarily unavailable —
// project_id is best-effort. The user can still use the proxy (which forwards
// the token directly without needing project_id).
func TestPollModelserverLoginToleratesProfileEndpointFailure(t *testing.T) {
	port := freeUIPort(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"opaque-at","refresh_token":"fake-rt","token_type":"Bearer","expires_in":3600}`))
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/api/oauth/profile", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "endpoint not deployed yet", http.StatusNotFound)
	})
	proxyFake := httptest.NewServer(proxyMux)
	defer proxyFake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	r := &realOrchestrator{d: Deps{
		State:   store,
		Secrets: secrets.New(filepath.Join(dir, "secrets.json")),
		MS:      modelserver.New(fake.URL),
		MSProxy: modelserver.New(proxyFake.URL),
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
	if _, err := r.PollModelserverLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Load()
	if s.Modelserver.ProjectID != "" {
		t.Fatalf("project id=%q, want empty when profile endpoint fails", s.Modelserver.ProjectID)
	}
	if !s.Onboarding.HasCompleted("modelserver_login") {
		t.Fatal("modelserver_login should still complete when profile fails")
	}
}

func TestPollAgentserverLoginRegistersAgentAndStoresWorkspaceName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"oauth-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/agent/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauth-token" {
			t.Fatalf("register Authorization=%q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sandbox_id":"sb-1","tunnel_token":"tunnel-token","proxy_token":"sandbox-proxy-token","workspace_id":"ws-claim","short_id":"abc123"}`))
	})
	mux.HandleFunc("/api/agent/whoami", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sandbox-proxy-token" {
			t.Fatalf("whoami Authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"workspace_id":"ws-claim","workspace_name":"Readable workspace"}`))
	})
	mux.HandleFunc("/api/workspaces", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("/api/workspaces should not be called during agentserver OAuth login")
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
	if key.Secret != "sandbox-proxy-token" {
		t.Fatalf("secret=%q, want sandbox proxy token", key.Secret)
	}
	if got, _ := sec.Get("agentserver_ws_api_key"); got != "sandbox-proxy-token" {
		t.Fatalf("agentserver_ws_api_key=%q, want sandbox proxy token", got)
	}
	if got, _ := sec.Get("agentserver_tunnel_token"); got != "tunnel-token" {
		t.Fatalf("agentserver_tunnel_token=%q, want tunnel token", got)
	}
	s, _ := store.Load()
	if s.Agentserver.SandboxID != "sb-1" {
		t.Fatalf("SandboxID=%q, want sb-1", s.Agentserver.SandboxID)
	}
	if s.Agentserver.ShortID != "abc123" {
		t.Fatalf("ShortID=%q, want abc123", s.Agentserver.ShortID)
	}
	if s.Agentserver.WorkspaceID != "ws-claim" {
		t.Fatalf("WorkspaceID=%q, want ws-claim", s.Agentserver.WorkspaceID)
	}
	if s.Agentserver.WorkspaceName != "Readable workspace" {
		t.Fatalf("WorkspaceName=%q, want Readable workspace", s.Agentserver.WorkspaceName)
	}
	if !s.Onboarding.HasCompleted("agentserver_login") {
		t.Fatal("agentserver_login not completed")
	}
}

func TestPollAgentserverLoginRefreshesLoomDriverConfigAndMCP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"oauth-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/agent/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sandbox_id":"sb-new","tunnel_token":"tunnel-new","proxy_token":"proxy-new","workspace_id":"ws-new","short_id":"new123"}`))
	})
	mux.HandleFunc("/api/agent/whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"workspace_id":"ws-new","workspace_name":"New workspace"}`))
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		s.Agentserver.SandboxID = "sb-old"
		s.Agentserver.WorkspaceID = "ws-old"
		s.Agentserver.WorkspaceName = "Old workspace"
		s.Agentserver.ShortID = "old123"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Set("agentserver_ws_api_key", "proxy-old"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set("agentserver_tunnel_token", "tunnel-old"); err != nil {
		t.Fatal(err)
	}
	driverExe := filepath.Join(dir, "install", "driver-agent.exe")
	if err := os.MkdirAll(filepath.Dir(driverExe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(driverExe, []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	loomConfig := filepath.Join(dir, ".config", "multi-agent", "driver.yaml")
	codexConfig := filepath.Join(dir, ".codex", "config.toml")
	machineFile := filepath.Join(dir, ".agentserver-app", "machine.json")
	if _, err := slave.NewMachineStore(machineFile).Ensure("TEST-PC"); err != nil {
		t.Fatal(err)
	}
	r := &realOrchestrator{d: Deps{
		State:           store,
		Secrets:         sec,
		AS:              agentserver.New(fake.URL),
		ASOAuth:         oauth.Config{Endpoint: fake.URL, TokenPath: "/api/oauth2/token", ClientID: "client-x"},
		CodexConfigPath: codexConfig,
		LoomDriverPath:  driverExe,
		LoomConfigPath:  loomConfig,
		MachineFile:     machineFile,
	}}
	r.asChallenge = oauth.DeviceCodeChallenge{DeviceCode: "dev", ExpiresIn: 30, Interval: 1}

	if _, err := r.PollAgentserverLogin(context.Background()); err != nil {
		t.Fatal(err)
	}

	loomBytes, err := os.ReadFile(loomConfig)
	if err != nil {
		t.Fatal(err)
	}
	loomText := string(loomBytes)
	for _, want := range []string{
		`proxy_token: "proxy-new"`,
		`tunnel_token: "tunnel-new"`,
		`sandbox_id: "sb-new"`,
		`workspace_id: "ws-new"`,
		`workspace_name: "New workspace"`,
		`agent_id: "driver-new123"`,
		`display_name: "TEST-PC"`,
		`description: "TEST-PC 本地协作驱动。"`,
	} {
		if !strings.Contains(loomText, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, loomText)
		}
	}
	codexBytes, err := os.ReadFile(codexConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codexBytes), `[mcp_servers.driver]`) {
		t.Fatalf("config.toml missing driver MCP:\n%s", string(codexBytes))
	}
}

func TestPollAgentserverLoginUsesRegisterWorkspaceWhenWhoamiNameUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"oauth-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/agent/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sandbox_id":"sb-1","tunnel_token":"tunnel-token","proxy_token":"sandbox-proxy-token","workspace_id":"ws-register","short_id":"abc123"}`))
	})
	mux.HandleFunc("/api/agent/whoami", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sandbox-proxy-token" {
			t.Fatalf("Authorization=%q", r.Header.Get("Authorization"))
		}
		http.Error(w, "whoami unavailable", http.StatusBadGateway)
	})
	mux.HandleFunc("/api/workspaces", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("/api/workspaces should not be called during agentserver OAuth login")
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
	if key.Secret != "sandbox-proxy-token" {
		t.Fatalf("secret=%q", key.Secret)
	}
	s, _ := store.Load()
	if s.Agentserver.WorkspaceID != "ws-register" {
		t.Fatalf("WorkspaceID=%q, want ws-register", s.Agentserver.WorkspaceID)
	}
}

func TestPollAgentserverLoginRequiresWorkspaceID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"opaque-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/agent/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sandbox_id":"sb-1","tunnel_token":"tunnel-token","proxy_token":"sandbox-proxy-token","short_id":"abc123"}`))
	})
	mux.HandleFunc("/api/agent/whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"user_id":"user-1"}`))
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

func TestPollAgentserverLoginRequiresCompleteAgentRegistration(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"opaque-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/agent/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"proxy_token":"sandbox-proxy-token","workspace_id":"ws-1"}`))
	})
	mux.HandleFunc("/api/agent/whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"workspace_id":"ws-1","workspace_name":"Readable workspace"}`))
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
		t.Fatal("expected incomplete registration error")
	}
	s, _ := store.Load()
	if s.Onboarding.HasCompleted("agentserver_login") {
		t.Fatal("agentserver_login should not complete without complete registration")
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
		w.Write([]byte(`{"access_token":"fake-at","token_type":"Bearer","refresh_token":"fake-rt","expires_in":3600}`))
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

func TestEnsureFrontendOpenCodeDesktop(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeOpenCodeDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	called := false
	r := &realOrchestrator{d: Deps{
		State: store,
		OpenCodeDesktopEnsure: func(ctx context.Context) (opencodedesktop.Detected, error) {
			called = true
			return opencodedesktop.Detected{Installed: true, Path: `C:\OpenCode\OpenCode.exe`, Version: "1.2.3"}, nil
		},
	}}
	if err := r.EnsureFrontend(context.Background(), nil); err != nil {
		t.Fatalf("EnsureFrontend: %v", err)
	}
	if !called {
		t.Fatal("OpenCode ensure was not called")
	}
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("opencode_desktop_installed") {
		t.Fatalf("opencode_desktop_installed not marked")
	}
	if !s.OpenCodeDesktop.Installed || s.OpenCodeDesktop.Path == "" || s.OpenCodeDesktop.Version != "1.2.3" {
		t.Fatalf("OpenCodeDesktop state not recorded: %+v", s.OpenCodeDesktop)
	}
}

func TestEnsureOpenCodeDesktopUsesRuntimeDownloadInstaller(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeOpenCodeDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var gotPath string
	old := ensureOpenCodeDesktopInstalled
	ensureOpenCodeDesktopInstalled = func(ctx context.Context, opts opencodedesktop.Options) (opencodedesktop.Detected, error) {
		gotPath = opts.LocalInstallerPath
		return opencodedesktop.Detected{Installed: true, Path: `C:\OpenCode\OpenCode.exe`, Version: "1.2.3"}, nil
	}
	t.Cleanup(func() { ensureOpenCodeDesktopInstalled = old })

	r := NewRealOrchestrator(Deps{
		State: store,
	}).(*realOrchestrator)
	if err := r.EnsureFrontend(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if gotPath != "" {
		t.Fatalf("LocalInstallerPath = %q, want empty so installer downloads the latest OpenCode Desktop", gotPath)
	}
}

func TestConfigureCodexDesktopWritesSharedConfigOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "old-openai-token")
	t.Setenv(codex.LocalProxyAPIKeyEnv, "")
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
	if !strings.Contains(string(b), `base_url = "`+modelproxy.DefaultBaseURL+`"`) {
		t.Fatalf("config missing local proxy base_url:\n%s", b)
	}
	if !strings.Contains(string(b), `experimental_bearer_token = "`+codex.LegacyLocalProxyAPIKeyValue+`"`) {
		t.Fatalf("config missing local proxy bearer token:\n%s", b)
	}
	if got := os.Getenv(codex.LocalProxyAPIKeyEnv); got != codex.LegacyLocalProxyAPIKeyValue {
		t.Fatalf("%s=%q, want stable local key", codex.LocalProxyAPIKeyEnv, got)
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "old-openai-token" {
		t.Fatalf("OPENAI_API_KEY=%q, want unchanged old-openai-token", got)
	}
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("codex_desktop_configured") {
		t.Fatalf("codex_desktop_configured not marked")
	}
}

func TestConfigureOpenCodeDesktopWritesOpenCodeConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(codex.LocalProxyAPIKeyEnv, "")
	proxyToken := "desktop-local-proxy-token"
	proxyTokenPath := filepath.Join(dir, ".agentserver-app", "proxy-token")
	if err := os.MkdirAll(filepath.Dir(proxyTokenPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proxyTokenPath, []byte(proxyToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeOpenCodeDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := &realOrchestrator{d: Deps{
		State:               store,
		CodexConfigPath:     filepath.Join(dir, ".codex", "config.toml"),
		LocalProxyTokenPath: proxyTokenPath,
		OpenCodeConfigPath:  filepath.Join(dir, ".config", "opencode", "opencode.jsonc"),
	}}
	if err := r.ConfigureFrontend(context.Background()); err != nil {
		t.Fatalf("ConfigureFrontend: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".config", "opencode", "opencode.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"modelserver/gpt-5.5",
		modelproxy.DefaultBaseURL,
		"{env:AGENTSERVER_LOCAL_MODEL_PROXY_API_KEY}",
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("opencode config missing %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "AGENTSERVER_CODEX_LOCAL_API_KEY") {
		t.Fatalf("opencode config should not use Codex-specific env names:\n%s", b)
	}
	if strings.Contains(string(b), proxyToken) {
		t.Fatalf("opencode config should not persist local proxy token:\n%s", b)
	}
	if got := os.Getenv(codex.LocalProxyAPIKeyEnv); got != proxyToken {
		t.Fatalf("%s=%q, want proxy token", codex.LocalProxyAPIKeyEnv, got)
	}
	if got := os.Getenv(opencode.LocalProxyAPIKeyEnv); got != proxyToken {
		t.Fatalf("%s=%q, want proxy token", opencode.LocalProxyAPIKeyEnv, got)
	}
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("opencode_desktop_configured") {
		t.Fatalf("opencode_desktop_configured not marked")
	}
}

func TestConfigureCodexDesktopWritesUILocale(t *testing.T) {
	dir := t.TempDir()
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
	globalPath := filepath.Join(dir, ".codex", ".codex-global-state.json")
	computerUsePath := filepath.Join(dir, ".codex", "computer-use", "config.json")
	r := &realOrchestrator{d: Deps{
		State:                             store,
		Secrets:                           sec,
		CodexConfigPath:                   filepath.Join(dir, ".codex", "config.toml"),
		CodexDesktopGlobalStatePath:       globalPath,
		CodexDesktopComputerUseConfigPath: computerUsePath,
	}}

	if err := r.ConfigureFrontend(context.Background()); err != nil {
		t.Fatalf("ConfigureFrontend: %v", err)
	}

	assertJSONField(t, globalPath, "localeOverride", "zh-CN")
	assertJSONField(t, computerUsePath, "locale", "zh-CN")
}

func TestConfigureCodexDesktopWritesLoomDriverConfigAndMCP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "")
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	for key, value := range map[string]string{
		"modelserver_api_key":      "desktop-token",
		"agentserver_ws_api_key":   "sandbox-proxy-token",
		"agentserver_tunnel_token": "tunnel-token",
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		s.Agentserver.SandboxID = "sb-1"
		s.Agentserver.WorkspaceID = "ws-1"
		s.Agentserver.WorkspaceName = "Readable workspace"
		s.Agentserver.ShortID = "abc123"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	driverExe := filepath.Join(dir, "install", "driver-agent.exe")
	if err := os.MkdirAll(filepath.Dir(driverExe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(driverExe, []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestTarGz(t, filepath.Join(filepath.Dir(driverExe), "driver-skills.tar.gz"), map[string]string{
		"skills/multiagent/SKILL.md": "---\nname: multiagent\n---\nUse driver tools.\n",
	})
	writeTestTarGz(t, filepath.Join(filepath.Dir(driverExe), "driver-superpower-skills.tar.gz"), map[string]string{
		"using-superpowers/SKILL.md":       "---\nname: using-superpowers\n---\nUse skills.\n",
		"test-driven-development/SKILL.md": "---\nname: test-driven-development\n---\nWrite tests first.\n",
	})
	writeTestTarGz(t, filepath.Join(filepath.Dir(driverExe), "driver-codex-prompts.tar.gz"), map[string]string{
		"prompts-codex/AGENTS.md": "# Multi-Agent Driver\n\nUse `role == \"slave\"`.\n",
	})
	loomConfig := filepath.Join(dir, ".config", "multi-agent", "driver.yaml")
	r := &realOrchestrator{d: Deps{
		State:                 store,
		Secrets:               sec,
		CodexConfigPath:       filepath.Join(dir, ".codex", "config.toml"),
		LoomDriverPath:        driverExe,
		LoomConfigPath:        loomConfig,
		CodexDesktopCodexPath: filepath.Join(dir, "codex.exe"),
	}}

	if err := r.ConfigureFrontend(context.Background()); err != nil {
		t.Fatalf("ConfigureFrontend: %v", err)
	}

	loomBytes, err := os.ReadFile(loomConfig)
	if err != nil {
		t.Fatal(err)
	}
	loomText := string(loomBytes)
	for _, want := range []string{
		`url: "https://agent.cs.ac.cn"`,
		`sandbox_id: "sb-1"`,
		`tunnel_token: "tunnel-token"`,
		`proxy_token: "sandbox-proxy-token"`,
		`workspace_id: "ws-1"`,
		`short_id: "abc123"`,
		`kind: "codex"`,
		`bin: "` + filepath.ToSlash(filepath.Join(dir, "codex.exe")) + `"`,
		`codex_home: "` + filepath.ToSlash(filepath.Join(dir, ".codex")) + `"`,
		`enabled: true`,
		`url: "https://loom.nj.cs.ac.cn:10062/"`,
		`workspace_name: "Readable workspace"`,
		`agent_id: "driver-abc123"`,
		`api_key: "sandbox-proxy-token"`,
		`token_state_path: "` + filepath.ToSlash(filepath.Join(filepath.Dir(loomConfig), "observer.token")) + `"`,
	} {
		if !strings.Contains(loomText, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, loomText)
		}
	}
	if strings.Contains(loomText, "telemetry_enabled") {
		t.Fatalf("driver.yaml contains unsupported observer telemetry field:\n%s", loomText)
	}

	codexBytes, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	codexText := string(codexBytes)
	for _, want := range []string{
		`[mcp_servers.driver]`,
		`command = "` + strings.ReplaceAll(driverExe, `\`, `\\`) + `"`,
		`args = ["serve-mcp", "--config", "` + strings.ReplaceAll(loomConfig, `\`, `\\`) + `"]`,
		`startup_timeout_sec = 30`,
		`tool_timeout_sec = 120`,
		`enabled = true`,
	} {
		if !strings.Contains(codexText, want) {
			t.Fatalf("config.toml missing %q:\n%s", want, codexText)
		}
	}
	for _, path := range []string{
		filepath.Join(dir, ".agents", "skills", "multiagent", "SKILL.md"),
		filepath.Join(dir, ".codex", "skills", "multiagent", "SKILL.md"),
		filepath.Join(dir, ".agents", "skills", "using-superpowers", "SKILL.md"),
		filepath.Join(dir, ".codex", "skills", "using-superpowers", "SKILL.md"),
		filepath.Join(dir, ".agents", "skills", "test-driven-development", "SKILL.md"),
		filepath.Join(dir, ".codex", "skills", "test-driven-development", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected Loom driver skill at %s: %v", path, err)
		}
	}
	agentsText, err := os.ReadFile(filepath.Join(dir, ".codex", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsText), "role == \"slave\"") {
		t.Fatalf("AGENTS.md missing Loom Codex driver prompt:\n%s", agentsText)
	}
}

func TestConfigureCodexDesktopDefaultsDriverCodexBinToManagedCodexPath(t *testing.T) {
	dir := t.TempDir()
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	for key, value := range map[string]string{
		"agentserver_ws_api_key":   "sandbox-proxy-token",
		"agentserver_tunnel_token": "tunnel-token",
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		s.Agentserver.SandboxID = "sb-1"
		s.Agentserver.WorkspaceID = "ws-1"
		s.Agentserver.ShortID = "abc123"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	driverExe := filepath.Join(dir, "install", "driver-agent.exe")
	if err := os.MkdirAll(filepath.Dir(driverExe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(driverExe, []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	codexAbsPath := filepath.Join(dir, "agentserver-app", "bin", "codex.exe")
	loomConfig := filepath.Join(dir, ".config", "multi-agent", "driver.yaml")
	r := &realOrchestrator{d: Deps{
		State:           store,
		Secrets:         sec,
		CodexConfigPath: filepath.Join(dir, ".codex", "config.toml"),
		CodexAbsPath:    codexAbsPath,
		LoomDriverPath:  driverExe,
		LoomConfigPath:  loomConfig,
	}}

	if err := r.ConfigureFrontend(context.Background()); err != nil {
		t.Fatalf("ConfigureFrontend: %v", err)
	}

	loomBytes, err := os.ReadFile(loomConfig)
	if err != nil {
		t.Fatal(err)
	}
	loomText := string(loomBytes)
	if !strings.Contains(loomText, `bin: "`+filepath.ToSlash(codexAbsPath)+`"`) {
		t.Fatalf("driver.yaml should use managed Codex runtime path:\n%s", loomText)
	}
	if strings.Contains(loomText, `bin: "codex"`) {
		t.Fatalf("Codex Desktop driver should not rely on PATH codex command:\n%s", loomText)
	}
}

func TestConfigureCodexDesktopRequiresAgentserverRegistrationForLoom(t *testing.T) {
	dir := t.TempDir()
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
	driverExe := filepath.Join(dir, "driver-agent.exe")
	if err := os.WriteFile(driverExe, []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &realOrchestrator{d: Deps{
		State:           store,
		Secrets:         sec,
		CodexConfigPath: filepath.Join(dir, ".codex", "config.toml"),
		LoomDriverPath:  driverExe,
		LoomConfigPath:  filepath.Join(dir, ".config", "multi-agent", "driver.yaml"),
	}}

	err := r.ConfigureFrontend(context.Background())
	if err == nil {
		t.Fatal("expected missing agentserver registration error")
	}
	if !strings.Contains(err.Error(), "agentserver registration") {
		t.Fatalf("error=%v", err)
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

func TestLaunchAndShutdownCodexDesktopWritesUILocaleBeforeOpen(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(dir, ".codex", ".codex-global-state.json")
	computerUsePath := filepath.Join(dir, ".codex", "computer-use", "config.json")
	r := &realOrchestrator{d: Deps{
		State:                             store,
		CodexDesktopGlobalStatePath:       globalPath,
		CodexDesktopComputerUseConfigPath: computerUsePath,
		CodexDesktopOpen: func(url string) error {
			assertJSONField(t, globalPath, "localeOverride", "zh-CN")
			assertJSONField(t, computerUsePath, "locale", "zh-CN")
			return nil
		},
	}}

	if err := r.LaunchAndShutdown(context.Background()); err != nil {
		t.Fatalf("LaunchAndShutdown: %v", err)
	}
}

func TestLaunchAndShutdownOpenCodeDesktopConfiguresLoomDriverBeforeOpen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "")
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	for key, value := range map[string]string{
		"agentserver_ws_api_key":   "sandbox-proxy-token",
		"agentserver_tunnel_token": "tunnel-token",
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeOpenCodeDesktop
		s.Agentserver.SandboxID = "sb-1"
		s.Agentserver.WorkspaceID = "ws-1"
		s.Agentserver.WorkspaceName = "Readable workspace"
		s.Agentserver.ShortID = "opn123"
		s.OpenCodeDesktop.Installed = true
		s.OpenCodeDesktop.Path = filepath.Join(dir, "OpenCode.exe")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	driverExe := filepath.Join(dir, "install", "driver-agent.exe")
	if err := os.MkdirAll(filepath.Dir(driverExe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(driverExe, []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestTarGz(t, filepath.Join(filepath.Dir(driverExe), "driver-skills.tar.gz"), map[string]string{
		"skills/multiagent/SKILL.md": "---\nname: multiagent\n---\nUse driver tools.\n",
	})
	writeTestTarGz(t, filepath.Join(filepath.Dir(driverExe), "driver-superpower-skills.tar.gz"), map[string]string{
		"using-superpowers/SKILL.md": "---\nname: using-superpowers\n---\nUse skills.\n",
	})
	writeTestTarGz(t, filepath.Join(filepath.Dir(driverExe), "driver-codex-prompts.tar.gz"), map[string]string{
		"prompts-codex/AGENTS.md": "# Multi-Agent Driver\n\nUse `role == \"slave\"`.\n",
	})
	loomConfig := filepath.Join(dir, ".config", "multi-agent", "driver.yaml")
	var launched bool
	r := &realOrchestrator{d: Deps{
		State:              store,
		Secrets:            sec,
		CodexConfigPath:    filepath.Join(dir, ".codex", "config.toml"),
		OpenCodeConfigPath: filepath.Join(dir, ".config", "opencode", "opencode.jsonc"),
		LoomDriverPath:     driverExe,
		LoomConfigPath:     loomConfig,
		OpenCodeDesktopLaunch: func(ctx context.Context, opts opencodedesktop.LaunchOptions) error {
			launched = true
			if opts.Config.Path != filepath.Join(dir, ".config", "opencode", "opencode.jsonc") {
				return fmt.Errorf("OpenCode config path = %q", opts.Config.Path)
			}
			if opts.Config.APIKeyEnv != "AGENTSERVER_LOCAL_MODEL_PROXY_API_KEY" {
				return fmt.Errorf("OpenCode API key env = %q", opts.Config.APIKeyEnv)
			}
			if opts.Config.APIKey == "" {
				return fmt.Errorf("OpenCode API key empty")
			}
			if _, err := os.Stat(loomConfig); err != nil {
				return fmt.Errorf("loom driver config missing before launch: %w", err)
			}
			codexBytes, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
			if err != nil {
				return err
			}
			if !strings.Contains(string(codexBytes), `[mcp_servers.driver]`) {
				return fmt.Errorf("codex mcp driver missing before launch:\n%s", codexBytes)
			}
			return nil
		},
	}}

	if err := r.LaunchAndShutdown(context.Background()); err != nil {
		t.Fatalf("LaunchAndShutdown: %v", err)
	}
	if !launched {
		t.Fatal("OpenCode launch was not called")
	}
}

func TestLaunchAndShutdownOpenCodeDesktopRequiresConfigPath(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeOpenCodeDesktop
		s.OpenCodeDesktop.Installed = true
		s.OpenCodeDesktop.Path = filepath.Join(dir, "OpenCode.exe")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := &realOrchestrator{d: Deps{
		State:           store,
		CodexConfigPath: filepath.Join(dir, ".codex", "config.toml"),
		OpenCodeDesktopLaunch: func(ctx context.Context, opts opencodedesktop.LaunchOptions) error {
			t.Fatal("OpenCode launch should not run without OpenCodeConfigPath")
			return nil
		},
	}}

	err := r.LaunchAndShutdown(context.Background())
	if err == nil || !strings.Contains(err.Error(), "OpenCodeConfigPath required") {
		t.Fatalf("err=%v, want OpenCodeConfigPath required", err)
	}
}

func assertJSONField(t *testing.T, path, key, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, b)
	}
	if got := root[key]; got != want {
		t.Fatalf("%s[%q]=%v, want %q", path, key, got, want)
	}
}

func writeTestTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()
	for name, content := range files {
		b := []byte(content)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
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
