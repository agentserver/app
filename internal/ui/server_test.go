package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
)

func TestServerStateEndpoint(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	var s SanitizedState
	json.NewDecoder(resp.Body).Decode(&s)
}

func TestServerConsoleHealthEndpointRequiresCompletedConsole(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/console/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err == nil && resp.StatusCode/100 == 2 && body["state"] == "ok" {
		t.Fatalf("plain server should not report completed-console health: status=%d body=%+v", resp.StatusCode, body)
	}
}

func TestServerConsoleHealthEndpointReportsHealthyCompletedConsole(t *testing.T) {
	cc := &fakeConsoleController{healthy: true}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/console/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["state"] != "ok" {
		t.Fatalf("body=%+v", body)
	}
}

func TestServerStepEndpoint(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}))
	defer srv.Close()

	// MS login
	resp, err := http.Post(srv.URL+"/api/step/modelserver_login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("MS status %d", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["state"] != "started" {
		t.Errorf("MS got %+v, want state=started", body)
	}
	if body["oauth_url"] == nil || body["oauth_url"] == "" {
		t.Errorf("MS missing oauth_url: %+v", body)
	}

	// AS login (same response shape now)
	resp2, err := http.Post(srv.URL+"/api/step/agentserver_login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("AS status %d", resp2.StatusCode)
	}
	var body2 map[string]any
	json.NewDecoder(resp2.Body).Decode(&body2)
	if body2["state"] != "started" {
		t.Errorf("AS got %+v, want state=started", body2)
	}
	if body2["oauth_url"] == nil || body2["oauth_url"] == "" {
		t.Errorf("AS missing oauth_url: %+v", body2)
	}
}

func TestServerStaticAsset(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
}

func TestServerVSCodeInstallReportsErrorsOnSSE(t *testing.T) {
	srv := httptest.NewServer(NewServer(vscodeInstallErrorOrchestrator{}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/step/vscode_install", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	eventsResp, err := http.Get(srv.URL + "/api/events?stream=" + body["stream_id"])
	if err != nil {
		t.Fatal(err)
	}
	defer eventsResp.Body.Close()

	scanner := bufio.NewScanner(eventsResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev ProgressEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Stage == "error" && strings.Contains(ev.Msg, "download incomplete") {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("expected vscode install error event on SSE stream")
}

type vscodeInstallErrorOrchestrator struct{ noopOrchestrator }

func (vscodeInstallErrorOrchestrator) EnsureVSCode(context.Context, chan<- ProgressEvent) error {
	return errors.New("download incomplete: got 3145728 bytes, want 104934400")
}

func (vscodeInstallErrorOrchestrator) EnsureFrontend(context.Context, chan<- ProgressEvent) error {
	return errors.New("download incomplete: got 3145728 bytes, want 104934400")
}

var _ Orchestrator = vscodeInstallErrorOrchestrator{}
var _ = modelserver.APIKey{}
var _ = agentserver.WorkspaceAPIKey{}

func TestServerStateIncludesFrontendMode(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s SanitizedState
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.FrontendMode != "codex_desktop" {
		t.Fatalf("FrontendMode=%q", s.FrontendMode)
	}
	if s.FrontendName != "Codex Desktop" {
		t.Fatalf("FrontendName=%q", s.FrontendName)
	}
}

func TestServerFrontendInstallReportsErrorsOnSSE(t *testing.T) {
	srv := httptest.NewServer(NewServer(frontendInstallErrorOrchestrator{}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/step/frontend_install", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	eventsResp, err := http.Get(srv.URL + "/api/events?stream=" + body["stream_id"])
	if err != nil {
		t.Fatal(err)
	}
	defer eventsResp.Body.Close()

	scanner := bufio.NewScanner(eventsResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev ProgressEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Stage == "error" && strings.Contains(ev.Msg, "winget missing") {
			return
		}
	}
	t.Fatal("expected frontend install error event on SSE stream")
}

type frontendInstallErrorOrchestrator struct{ noopOrchestrator }

func (frontendInstallErrorOrchestrator) EnsureFrontend(context.Context, chan<- ProgressEvent) error {
	return errors.New("winget missing")
}

func TestServerConsoleStateEndpoint(t *testing.T) {
	cc := &fakeConsoleController{
		state: console.State{
			FrontendMode:    "codex_desktop",
			SubscriptionURL: "https://code.cs.ac.cn/projects/proj-1/subscription",
		},
	}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/console/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["subscription_url"] == "" {
		t.Fatalf("body=%+v", body)
	}
}

func TestServerConsoleOpenFrontendEndpoint(t *testing.T) {
	cc := &fakeConsoleController{}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/console/open-frontend", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !cc.openedFrontend {
		t.Fatal("open frontend not called")
	}
}

func TestServerConsoleLogoutModelserverEndpoint(t *testing.T) {
	cc := &fakeConsoleController{}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/console/logout-modelserver", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["state"] != "logged_out" {
		t.Fatalf("body=%+v", body)
	}
	if !cc.loggedOutModelserver {
		t.Fatal("logout modelserver not called")
	}
}

func TestServerConsoleActionEndpointsRequirePost(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		called func(*fakeConsoleController) bool
	}{
		{
			name: "refresh",
			path: "/api/console/refresh",
			called: func(cc *fakeConsoleController) bool {
				return cc.refreshed
			},
		},
		{
			name: "open frontend",
			path: "/api/console/open-frontend",
			called: func(cc *fakeConsoleController) bool {
				return cc.openedFrontend
			},
		},
		{
			name: "open subscription",
			path: "/api/console/open-subscription",
			called: func(cc *fakeConsoleController) bool {
				return cc.openedSubscription
			},
		},
		{
			name: "logout modelserver",
			path: "/api/console/logout-modelserver",
			called: func(cc *fakeConsoleController) bool {
				return cc.loggedOutModelserver
			},
		},
		{
			name: "quit",
			path: "/api/console/quit",
			called: func(cc *fakeConsoleController) bool {
				return cc.quit
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc := &fakeConsoleController{}
			srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
			defer srv.Close()

			resp, err := http.Get(srv.URL + tt.path)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("status=%d", resp.StatusCode)
			}
			if resp.Header.Get("Allow") != http.MethodPost {
				t.Fatalf("Allow=%q", resp.Header.Get("Allow"))
			}
			if tt.called(cc) {
				t.Fatal("controller method was called")
			}
		})
	}
}

type fakeConsoleController struct {
	state                console.State
	healthy              bool
	refreshed            bool
	openedFrontend       bool
	openedSubscription   bool
	loggedOutModelserver bool
	quit                 bool
}

func (f *fakeConsoleController) State(context.Context) (console.State, error) {
	return f.state, nil
}
func (f *fakeConsoleController) Refresh(context.Context) (console.State, error) {
	f.refreshed = true
	return f.state, nil
}
func (f *fakeConsoleController) Healthy(context.Context) bool {
	return f.healthy
}
func (f *fakeConsoleController) OpenFrontend(context.Context) error {
	f.openedFrontend = true
	return nil
}
func (f *fakeConsoleController) OpenSubscription(context.Context) error {
	f.openedSubscription = true
	return nil
}
func (f *fakeConsoleController) LogoutModelserver(context.Context) error {
	f.loggedOutModelserver = true
	return nil
}
func (f *fakeConsoleController) Quit(context.Context) error {
	f.quit = true
	return nil
}
