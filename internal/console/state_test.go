package console

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

func TestControllerStateAggregatesQuotaAndWorkspace(t *testing.T) {
	ms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects":
			w.Write([]byte(`{"data":[{"id":"proj-1","name":"Default project"}]}`))
		case "/v1/usage":
			w.Write([]byte(`{"credit_usage":[{"window":"5h","percentage":58.2},{"window":"7d","percentage":22}]}`))
		case "/api/v1/projects/proj-1/subscription/usage":
			w.Write([]byte(`{"data":[{"window":"5h","percentage":58.2},{"window":"7d","percentage":22}]}`))
		default:
			t.Fatalf("modelserver unexpected path %s", r.URL.Path)
		}
	}))
	defer ms.Close()
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/whoami" {
			t.Fatalf("agentserver unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sandbox-proxy-token" {
			t.Fatalf("agentserver Authorization=%q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"workspace_id":"ws-1","workspace_name":"Default workspace"}`))
	}))
	defer as.Close()

	dir := t.TempDir()
	p := paths.Paths{
		StateFile:                filepath.Join(dir, "state.json"),
		SecretsFile:              filepath.Join(dir, "secrets.json"),
		ConsoleNotificationsFile: filepath.Join(dir, "console-notifications.json"),
	}
	store := state.NewStore(p.StateFile)
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		s.Modelserver.ProjectID = "proj-1"
		s.Agentserver.WorkspaceID = "ws-1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sec := newTestSecrets()
	if err := sec.Set("modelserver_api_key", "ms-token"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set("agentserver_ws_api_key", "sandbox-proxy-token"); err != nil {
		t.Fatal(err)
	}

	c := NewController(Deps{
		State: store, Secrets: sec,
		MS: modelserver.New(ms.URL), AS: agentserver.New(as.URL),
		ModelserverWebBaseURL: "https://code.cs.ac.cn",
	})
	got, err := c.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Modelserver.ProjectID != "proj-1" || got.Modelserver.ProjectName != "Default project" {
		t.Fatalf("modelserver state=%+v", got.Modelserver)
	}
	if got.Agentserver.WorkspaceID != "ws-1" || got.Agentserver.WorkspaceName != "Default workspace" {
		t.Fatalf("agentserver state=%+v", got.Agentserver)
	}
	if got.SubscriptionURL != "https://code.cs.ac.cn/projects/proj-1/subscription" {
		t.Fatalf("SubscriptionURL=%q", got.SubscriptionURL)
	}
	if len(got.Quotas) != 2 || got.Quotas[0].RemainingPercentage != 41.8 {
		t.Fatalf("quotas=%+v", got.Quotas)
	}
}

func TestControllerStateUsesProxyUsageForOAuthTokenQuota(t *testing.T) {
	ms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects":
			http.Error(w, `{"error":{"code":"unauthorized","message":"invalid or expired token"}}`, http.StatusUnauthorized)
		case "/api/v1/projects/proj-1/subscription/usage":
			http.Error(w, `{"error":{"code":"unauthorized","message":"invalid or expired token"}}`, http.StatusUnauthorized)
		case "/v1/usage":
			w.Write([]byte(`{"credit_usage":[{"window":"5h","percentage":58.2,"window_end":"2026-06-08T17:00:00Z"},{"window":"7d","percentage":22,"window_end":"2026-06-15T12:00:00Z"}]}`))
		default:
			t.Fatalf("modelserver unexpected path %s", r.URL.Path)
		}
	}))
	defer ms.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		s.Modelserver.ProjectID = "proj-1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sec := newTestSecrets()
	if err := sec.Set("modelserver_api_key", "oauth-token"); err != nil {
		t.Fatal(err)
	}

	got, err := NewController(Deps{
		State: store, Secrets: sec, MS: modelserver.New(ms.URL),
	}).State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.QuotaError != "" {
		t.Fatalf("QuotaError=%q", got.QuotaError)
	}
	if len(got.Quotas) != 2 || got.Quotas[0].RemainingPercentage != 41.8 || got.Quotas[0].ResetsAt != "2026-06-08T17:00:00Z" {
		t.Fatalf("quotas=%+v", got.Quotas)
	}
}

func TestControllerStateUsesProxyUsageWhenProjectIDPostponed(t *testing.T) {
	ms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects":
			t.Fatalf("/api/v1/projects should not be called without a project id")
		case "/v1/usage":
			w.Write([]byte(`{"credit_usage":[{"window":"5h","percentage":80,"window_end":"2026-06-08T17:00:00Z"},{"window":"7d","percentage":50}]}`))
		default:
			t.Fatalf("modelserver unexpected path %s", r.URL.Path)
		}
	}))
	defer ms.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sec := newTestSecrets()
	if err := sec.Set("modelserver_api_key", "oauth-token"); err != nil {
		t.Fatal(err)
	}

	got, err := NewController(Deps{
		State: store, Secrets: sec, MS: modelserver.New(ms.URL),
	}).State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Modelserver.ProjectID != "" {
		t.Fatalf("project/subscription should stay empty: %+v url=%q", got.Modelserver, got.SubscriptionURL)
	}
	if got.SubscriptionURL != "https://code.cs.ac.cn/projects" {
		t.Fatalf("SubscriptionURL=%q", got.SubscriptionURL)
	}
	if len(got.Quotas) != 2 || got.Quotas[0].Percentage != 80 || got.Quotas[0].RemainingPercentage != 20 {
		t.Fatalf("quotas=%+v", got.Quotas)
	}
}

func TestControllerStateUsesSeparateProxyClientForUsage(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects":
			http.Error(w, `{"error":{"code":"unauthorized","message":"invalid or expired token"}}`, http.StatusUnauthorized)
		case "/v1/usage":
			t.Fatalf("proxy usage must not be requested from admin API host")
		default:
			t.Fatalf("modelserver admin unexpected path %s", r.URL.Path)
		}
	}))
	defer admin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" {
			t.Fatalf("modelserver proxy unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"credit_usage":[{"window":"5h","percentage":40,"window_end":"2026-06-08T17:00:00Z"},{"window":"7d","percentage":10}]}`))
	}))
	defer proxy.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sec := newTestSecrets()
	if err := sec.Set("modelserver_api_key", "oauth-token"); err != nil {
		t.Fatal(err)
	}

	got, err := NewController(Deps{
		State: store, Secrets: sec,
		MS:      modelserver.New(admin.URL),
		MSProxy: modelserver.New(proxy.URL),
	}).State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.QuotaError != "" {
		t.Fatalf("QuotaError=%q", got.QuotaError)
	}
	if len(got.Quotas) != 2 || got.Quotas[0].Percentage != 40 || got.Quotas[0].ResetsAt != "2026-06-08T17:00:00Z" {
		t.Fatalf("quotas=%+v", got.Quotas)
	}
}

func TestControllerStateProvidesProjectsURLWhenProjectIDMissing(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got, err := NewController(Deps{
		State:                 store,
		Secrets:               newTestSecrets(),
		ModelserverWebBaseURL: "https://code.cs.ac.cn/",
	}).State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Modelserver.ProjectID != "" {
		t.Fatalf("ProjectID=%q, want empty", got.Modelserver.ProjectID)
	}
	if got.SubscriptionURL != "https://code.cs.ac.cn/projects" {
		t.Fatalf("SubscriptionURL=%q", got.SubscriptionURL)
	}
}

func TestControllerStateRequiresStateStore(t *testing.T) {
	_, err := NewController(Deps{}).State(context.Background())
	if err == nil || err.Error() != "console: state store required" {
		t.Fatalf("State err=%v", err)
	}
}

func TestControllerStateKeepsLaunchUsableWhenQuotaFails(t *testing.T) {
	ms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/projects" {
			w.Write([]byte(`{"data":[{"id":"proj-1","name":"Default project"}]}`))
			return
		}
		http.Error(w, "quota unavailable", http.StatusBadGateway)
	}))
	defer ms.Close()
	dir := t.TempDir()
	p := paths.Paths{StateFile: filepath.Join(dir, "state.json"), SecretsFile: filepath.Join(dir, "secrets.json")}
	store := state.NewStore(p.StateFile)
	_ = store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		s.Modelserver.ProjectID = "proj-1"
		return nil
	})
	sec := newTestSecrets()
	_ = sec.Set("modelserver_api_key", "ms-token")
	c := NewController(Deps{State: store, Secrets: sec, MS: modelserver.New(ms.URL)})
	got, err := c.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.QuotaError == "" {
		t.Fatalf("expected quota error, got %+v", got)
	}
}

func TestControllerStateMarksModelserverReconnectWhenRefreshTokenMissing(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		s.Onboarding.CompletedSteps = []string{"modelserver_login"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "expired-access-token"); err != nil {
		t.Fatal(err)
	}

	got, err := NewController(Deps{State: store, Secrets: sec}).State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Modelserver.ReconnectRequired {
		t.Fatalf("modelserver reconnect should be required: %+v", got.Modelserver)
	}
	if got.Modelserver.AuthMessage != "大模型连接已失效，请重新连接。" {
		t.Fatalf("AuthMessage=%q", got.Modelserver.AuthMessage)
	}
}

func TestControllerStateMarksModelserverReconnectWhenRefreshFailed(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		s.Onboarding.CompletedSteps = []string{"modelserver_login"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sec := newTestSecrets()
	for key, value := range map[string]string{
		tokenrefresh.AccessTokenKey:    "expired-access-token",
		tokenrefresh.RefreshTokenKey:   "expired-refresh-token",
		tokenrefresh.ReauthRequiredKey: "true",
		tokenrefresh.RefreshErrorKey:   "token refresh: invalid_grant: refresh token expired",
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}

	got, err := NewController(Deps{State: store, Secrets: sec}).State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Modelserver.ReconnectRequired {
		t.Fatalf("modelserver reconnect should be required: %+v", got.Modelserver)
	}
	if got.Modelserver.AuthMessage != "大模型连接已失效，请重新连接。" {
		t.Fatalf("AuthMessage=%q", got.Modelserver.AuthMessage)
	}
}

func TestControllerLogoutModelserverClearsLocalLogin(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		s.Onboarding.CompletedSteps = []string{"modelserver_login", "agentserver_login"}
		s.Modelserver.ProjectID = "proj-1"
		s.Modelserver.APIKeySuffix = "tail"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sec := newTestSecrets()
	for key, value := range map[string]string{
		tokenrefresh.AccessTokenKey:          "access-token",
		tokenrefresh.RefreshTokenKey:         "refresh-token",
		tokenrefresh.AccessTokenExpiresAtKey: "2026-06-08T12:00:00Z",
		tokenrefresh.ReauthRequiredKey:       "false",
		tokenrefresh.RefreshErrorKey:         "old refresh error",
		tokenrefresh.RefreshErrorAtKey:       "2026-06-08T12:01:00Z",
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}

	c := NewController(Deps{State: store, Secrets: sec})
	if err := c.LogoutModelserver(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		tokenrefresh.AccessTokenKey,
		tokenrefresh.RefreshTokenKey,
		tokenrefresh.AccessTokenExpiresAtKey,
		tokenrefresh.RefreshErrorKey,
		tokenrefresh.RefreshErrorAtKey,
	} {
		if got, err := sec.Get(key); err == nil {
			t.Fatalf("%s still stored as %q", key, got)
		}
	}
	if got, err := sec.Get(tokenrefresh.ReauthRequiredKey); err != nil || got != "true" {
		t.Fatalf("reauth required=%q err=%v", got, err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Onboarding.HasCompleted("modelserver_login") {
		t.Fatal("modelserver_login completion should remain so console can show reconnect")
	}
	if loaded.Modelserver.ProjectID != "" || loaded.Modelserver.APIKeySuffix != "" {
		t.Fatalf("modelserver state should be cleared after logout: %+v", loaded.Modelserver)
	}
	got, err := c.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Modelserver.ReconnectRequired {
		t.Fatalf("modelserver reconnect should be required after logout: %+v", got.Modelserver)
	}
}

func TestControllerStateIncludesFrontendAndRefreshTime(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeMinimalVSCode
		s.Onboarding.Status = state.StatusComplete
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c := NewController(Deps{
		State:   store,
		Secrets: newTestSecrets(),
	})

	got, err := c.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.FrontendMode != string(state.FrontendModeMinimalVSCode) {
		t.Fatalf("FrontendMode=%q", got.FrontendMode)
	}
	if got.FrontendName == "" {
		t.Fatal("FrontendName empty")
	}
	if got.OnboardingStatus != string(state.StatusComplete) {
		t.Fatalf("OnboardingStatus=%q", got.OnboardingStatus)
	}
	if _, err := time.Parse(time.RFC3339, got.LastRefreshedAt); err != nil {
		t.Fatalf("LastRefreshedAt=%q err=%v", got.LastRefreshedAt, err)
	}
}

func TestControllerActionsInvokeCallbacks(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	if err := store.Update(func(s *state.State) error {
		s.Modelserver.ProjectID = "proj-1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sec := newTestSecrets()
	var openedFrontend bool
	var openedURL string
	var quit bool
	c := NewController(Deps{
		State: store, Secrets: sec,
		ModelserverWebBaseURL: "https://code.cs.ac.cn/",
		OpenFrontend: func(context.Context) error {
			openedFrontend = true
			return nil
		},
		OpenURL: func(url string) error {
			openedURL = url
			return nil
		},
		Quit: func() {
			quit = true
		},
	})

	if err := c.OpenFrontend(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !openedFrontend {
		t.Fatal("OpenFrontend callback was not invoked")
	}
	if err := c.OpenSubscription(context.Background()); err != nil {
		t.Fatal(err)
	}
	if openedURL != "https://code.cs.ac.cn/projects/proj-1/subscription" {
		t.Fatalf("openedURL=%q", openedURL)
	}
	if err := c.Quit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !quit {
		t.Fatal("Quit callback was not invoked")
	}
}

func TestControllerListsAndControlsSlaves(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	runner := &testSlaveRunner{pid: 1111, authURL: "https://agent.cs.ac.cn/device?user_code=ABCD"}
	manager := slave.NewManager(slave.ManagerDeps{
		Machines:  slave.NewMachineStore(filepath.Join(dir, "machine.json")),
		Registry:  slave.NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves")),
		Runner:    runner,
		SlaveExe:  filepath.Join(dir, "slave-agent.exe"),
		ServerURL: "https://agent.cs.ac.cn",
		CodexBin:  "codex",
	})
	if _, err := manager.Machines.Ensure("PC"); err != nil {
		t.Fatal(err)
	}
	c := NewController(Deps{Slaves: manager})

	created, err := c.CreateSlave(context.Background(), slave.CreateInput{Folder: folder, Name: "worker"})
	if err != nil {
		t.Fatalf("CreateSlave: %v", err)
	}
	if created.Status != slave.StatusAuthRequired || created.PID != 1111 || created.AuthURL != runner.authURL {
		t.Fatalf("created slave=%+v", created)
	}

	machine, slaves, err := c.Slaves(context.Background())
	if err != nil {
		t.Fatalf("Slaves: %v", err)
	}
	if machine.ComputerName != "PC" {
		t.Fatalf("machine=%+v", machine)
	}
	if len(slaves) != 1 || slaves[0].ID != created.ID {
		t.Fatalf("slaves=%+v", slaves)
	}

	paused, err := c.PauseSlave(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("PauseSlave: %v", err)
	}
	if paused.Status != slave.StatusPaused || paused.PID != 0 || !runner.stopped[1111] {
		t.Fatalf("paused=%+v stopped=%+v", paused, runner.stopped)
	}

	runner.pid = 2222
	runner.authURL = ""
	restarted, err := c.RestartSlave(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("RestartSlave: %v", err)
	}
	if restarted.Status != slave.StatusStarting || restarted.PID != 2222 {
		t.Fatalf("restarted=%+v", restarted)
	}

	if err := c.DeleteSlave(context.Background(), created.ID); err != nil {
		t.Fatalf("DeleteSlave: %v", err)
	}
	_, slaves, err = c.Slaves(context.Background())
	if err != nil {
		t.Fatalf("Slaves after delete: %v", err)
	}
	if len(slaves) != 0 {
		t.Fatalf("slaves after delete=%+v", slaves)
	}
}

func TestControllerSlaveMethodsRequireManager(t *testing.T) {
	c := NewController(Deps{})
	ctx := context.Background()
	if _, _, err := c.Slaves(ctx); err == nil || err.Error() != "console: slave manager unavailable" {
		t.Fatalf("Slaves err=%v", err)
	}
	if _, err := c.CreateSlave(ctx, slave.CreateInput{}); err == nil || err.Error() != "console: slave manager unavailable" {
		t.Fatalf("CreateSlave err=%v", err)
	}
	if _, err := c.RestartSlave(ctx, "slave-1"); err == nil || err.Error() != "console: slave manager unavailable" {
		t.Fatalf("RestartSlave err=%v", err)
	}
	if _, err := c.PauseSlave(ctx, "slave-1"); err == nil || err.Error() != "console: slave manager unavailable" {
		t.Fatalf("PauseSlave err=%v", err)
	}
	if err := c.DeleteSlave(ctx, "slave-1"); err == nil || err.Error() != "console: slave manager unavailable" {
		t.Fatalf("DeleteSlave err=%v", err)
	}
}

func TestControllerOpenSubscriptionFallsBackToProjectsWhenProjectIDMissing(t *testing.T) {
	dir := t.TempDir()
	var openedURL string
	c := NewController(Deps{
		State:                 state.NewStore(filepath.Join(dir, "state.json")),
		Secrets:               newTestSecrets(),
		ModelserverWebBaseURL: "https://code.cs.ac.cn/",
		OpenURL: func(url string) error {
			openedURL = url
			return nil
		},
	})

	if err := c.OpenSubscription(context.Background()); err != nil {
		t.Fatalf("OpenSubscription: %v", err)
	}
	if openedURL != "https://code.cs.ac.cn/projects" {
		t.Fatalf("openedURL=%q", openedURL)
	}
}

func TestQuotaWindowsRoundsRemainingAndClampsAtZero(t *testing.T) {
	got := quotaWindows([]modelserver.SubscriptionUsageWindow{
		{Window: "5h", Percentage: 33.333, ResetsAt: "r1"},
		{Window: "7d", Percentage: 101},
	})

	if len(got) != 2 {
		t.Fatalf("quota windows=%+v", got)
	}
	if got[0].RemainingPercentage != 66.67 || got[0].ResetsAt != "r1" {
		t.Fatalf("first quota=%+v", got[0])
	}
	if got[1].RemainingPercentage != 0 {
		t.Fatalf("second quota=%+v", got[1])
	}
}

type testSlaveRunner struct {
	pid           int
	authURL       string
	startedConfig string
	stopped       map[int]bool
}

func (r *testSlaveRunner) Start(context.Context, slave.StartRequest) (slave.StartResult, error) {
	if r.stopped == nil {
		r.stopped = map[int]bool{}
	}
	return slave.StartResult{PID: r.pid, AuthURL: r.authURL}, nil
}

func (r *testSlaveRunner) Stop(_ context.Context, pid int) error {
	if r.stopped == nil {
		r.stopped = map[int]bool{}
	}
	r.stopped[pid] = true
	return nil
}

func mkdir(path string) error {
	return os.MkdirAll(path, 0o755)
}

type testSecrets struct {
	values map[string]string
}

func newTestSecrets() *testSecrets {
	return &testSecrets{values: map[string]string{}}
}

func (s *testSecrets) Get(key string) (string, error) {
	v, ok := s.values[key]
	if !ok {
		return "", errors.New("secret not found")
	}
	return v, nil
}

func (s *testSecrets) Set(key, value string) error {
	s.values[key] = value
	return nil
}

func (s *testSecrets) Delete(key string) error {
	delete(s.values, key)
	return nil
}
