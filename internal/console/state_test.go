package console

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func TestControllerStateAggregatesQuotaAndWorkspace(t *testing.T) {
	ms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects":
			w.Write([]byte(`{"data":[{"id":"proj-1","name":"Default project"}]}`))
		case "/api/v1/projects/proj-1/subscription/usage":
			w.Write([]byte(`{"data":[{"window":"5h","percentage":58.2},{"window":"7d","percentage":22}]}`))
		default:
			t.Fatalf("modelserver unexpected path %s", r.URL.Path)
		}
	}))
	defer ms.Close()
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces" {
			t.Fatalf("agentserver unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"data":[{"id":"ws-1","name":"Default workspace"}]}`))
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
	if err := sec.Set("agentserver_ws_api_key", "as-token"); err != nil {
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

func TestControllerOpenSubscriptionRequiresURL(t *testing.T) {
	dir := t.TempDir()
	c := NewController(Deps{
		State:   state.NewStore(filepath.Join(dir, "state.json")),
		Secrets: newTestSecrets(),
	})

	err := c.OpenSubscription(context.Background())
	if err == nil || err.Error() != "console: subscription URL unavailable" {
		t.Fatalf("OpenSubscription err=%v", err)
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
