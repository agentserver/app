package ui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/slave"
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

func TestServerConsoleSlaveMutationsRequireCompletedConsole(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/console/slaves", "application/json", bytes.NewBufferString(`{"folder":"/tmp/repo","name":"worker"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		t.Fatalf("plain server should not report slave mutation success: status=%d", resp.StatusCode)
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

func TestServerConsoleSelectFolderEndpoint(t *testing.T) {
	cc := &fakeConsoleController{selectedFolder: `C:\Users\me\repo`}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/console/select-folder", "application/json", nil)
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
	if body["folder"] != `C:\Users\me\repo` || !cc.selectedFolderCalled {
		t.Fatalf("body=%+v called=%v", body, cc.selectedFolderCalled)
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

func TestServerConsoleSlavesEndpointReturnsMachineAndSlaves(t *testing.T) {
	cc := &fakeConsoleController{
		machine: slave.Machine{MachineID: "machine-1", ComputerName: "PC"},
		slaves: []slave.Slave{
			{ID: "slave-1", Name: "repo", DisplayName: "PC-repo", Status: slave.StatusRunning, PID: 1234, CreatedAt: time.Unix(1, 0).UTC()},
		},
	}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/console/slaves")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body struct {
		Machine slave.Machine `json:"machine"`
		Slaves  []slave.Slave `json:"slaves"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Machine.ComputerName != "PC" || len(body.Slaves) != 1 || body.Slaves[0].ID != "slave-1" {
		t.Fatalf("body=%+v", body)
	}
	if !cc.listedSlaves {
		t.Fatal("Slaves was not called")
	}
}

func TestServerConsoleCreateSlaveEndpointForwardsInput(t *testing.T) {
	cc := &fakeConsoleController{
		createdSlave: slave.Slave{ID: "slave-1", Name: "worker", Status: slave.StatusAuthRequired},
	}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/console/slaves", "application/json", bytes.NewBufferString(`{"folder":"/tmp/repo","name":"worker"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body slave.Slave
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ID != "slave-1" || cc.createdInput.Folder != "/tmp/repo" || cc.createdInput.Name != "worker" {
		t.Fatalf("body=%+v input=%+v", body, cc.createdInput)
	}
}

func TestServerConsoleMutationsRejectCrossOriginBrowserRequests(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "create slave", method: http.MethodPost, path: "/api/console/slaves", body: `{"folder":"/tmp/repo","name":"worker"}`},
		{name: "restart slave", method: http.MethodPost, path: "/api/console/slaves/slave-1/restart"},
		{name: "pause slave", method: http.MethodPost, path: "/api/console/slaves/slave-1/pause"},
		{name: "delete slave", method: http.MethodDelete, path: "/api/console/slaves/slave-1"},
		{name: "select folder", method: http.MethodPost, path: "/api/console/select-folder"},
		{name: "open frontend", method: http.MethodPost, path: "/api/console/open-frontend"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc := &fakeConsoleController{}
			srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
			defer srv.Close()

			req, err := http.NewRequest(tt.method, srv.URL+tt.path, strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", "https://evil.example")
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status=%d", resp.StatusCode)
			}
			if cc.createdInput.Folder != "" || cc.restartedID != "" || cc.pausedID != "" ||
				cc.deletedID != "" || cc.selectedFolderCalled || cc.openedFrontend {
				t.Fatalf("cross-origin request reached controller: %+v", cc)
			}
		})
	}
}

func TestServerOnboardingMutationsRequirePostAndTrustedOrigin(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "modelserver login", path: "/api/step/modelserver_login"},
		{name: "agentserver login", path: "/api/step/agentserver_login"},
		{name: "frontend install", path: "/api/step/frontend_install"},
		{name: "frontend configure", path: "/api/step/frontend_configure"},
		{name: "legacy vscode install", path: "/api/step/vscode_install"},
		{name: "legacy vscode configure", path: "/api/step/vscode_configure"},
		{name: "finalize", path: "/api/finalize"},
		{name: "abort", path: "/api/abort"},
		{name: "launch", path: "/api/launch"},
		{name: "legacy launch vscode", path: "/api/launch-vscode"},
	}

	for _, tt := range tests {
		t.Run(tt.name+" rejects get", func(t *testing.T) {
			srv := httptest.NewServer(NewServer(noopOrchestrator{}))
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
		})

		t.Run(tt.name+" rejects cross origin post", func(t *testing.T) {
			srv := httptest.NewServer(NewServer(noopOrchestrator{}))
			defer srv.Close()

			req, err := http.NewRequest(http.MethodPost, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", "https://evil.example")
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status=%d", resp.StatusCode)
			}
		})
	}
}

func TestServerConsoleMutationsAllowSameOriginBrowserRequests(t *testing.T) {
	cc := &fakeConsoleController{}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/console/open-frontend", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if !cc.openedFrontend {
		t.Fatal("same-origin request should reach controller")
	}
}

func TestServerConsoleCreateSlaveEndpointReturnsBadRequestForValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "invalid name",
			err:  errWrap("slave name contains invalid path characters", slave.ErrInvalidCreateInput),
		},
		{
			name: "missing folder",
			err:  errWrap("folder unavailable", slave.ErrInvalidCreateInput),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc := &fakeConsoleController{createErr: tt.err}
			srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
			defer srv.Close()

			resp, err := http.Post(srv.URL+"/api/console/slaves", "application/json", bytes.NewBufferString(`{"folder":"/tmp/missing","name":"bad/name"}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d", resp.StatusCode)
			}
		})
	}
}

func TestServerConsoleCreateSlaveEndpointReturnsConflictForDuplicateDisplayName(t *testing.T) {
	cc := &fakeConsoleController{createErr: errWrap("slave display name already exists: PC-worker", slave.ErrSlaveConflict)}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/console/slaves", "application/json", bytes.NewBufferString(`{"folder":"/tmp/repo","name":"worker"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestServerConsoleCreateSlaveEndpointDoesNotClassifySimilarValidationMessage(t *testing.T) {
	cc := &fakeConsoleController{createErr: errors.New("folder unavailable: disk failed")}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/console/slaves", "application/json", bytes.NewBufferString(`{"folder":"/tmp/repo","name":"worker"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestServerConsoleCreateSlaveEndpointDoesNotTreatUnclassifiedNotExistAsMissingRoute(t *testing.T) {
	cc := &fakeConsoleController{createErr: errWrap("machine identity missing", os.ErrNotExist)}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/console/slaves", "application/json", bytes.NewBufferString(`{"folder":"/tmp/repo","name":"worker"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("create os.ErrNotExist should not be reported as endpoint not found")
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestServerConsoleSlaveActionEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus slave.Status
		called     func(*fakeConsoleController) string
	}{
		{
			name:       "restart",
			path:       "/api/console/slaves/slave-1/restart",
			wantStatus: slave.StatusStarting,
			called:     func(cc *fakeConsoleController) string { return cc.restartedID },
		},
		{
			name:       "pause",
			path:       "/api/console/slaves/slave-1/pause",
			wantStatus: slave.StatusPaused,
			called:     func(cc *fakeConsoleController) string { return cc.pausedID },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc := &fakeConsoleController{}
			srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
			defer srv.Close()

			resp, err := http.Post(srv.URL+tt.path, "application/json", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("status=%d", resp.StatusCode)
			}
			var body slave.Slave
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.ID != "slave-1" || body.Status != tt.wantStatus || tt.called(cc) != "slave-1" {
				t.Fatalf("body=%+v controller=%+v", body, cc)
			}
		})
	}
}

func TestServerConsoleDeleteSlaveEndpoint(t *testing.T) {
	cc := &fakeConsoleController{}
	srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/console/slaves/slave-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
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
	if body["state"] != "deleted" || cc.deletedID != "slave-1" {
		t.Fatalf("body=%+v deletedID=%q", body, cc.deletedID)
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
			name: "select folder",
			path: "/api/console/select-folder",
			called: func(cc *fakeConsoleController) bool {
				return cc.selectedFolderCalled
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

func TestServerConsoleSlaveEndpointsRequireAllowedMethods(t *testing.T) {
	tests := []struct {
		name  string
		req   func(string) (*http.Request, error)
		allow string
	}{
		{
			name: "list root rejects put",
			req: func(base string) (*http.Request, error) {
				return http.NewRequest(http.MethodPut, base+"/api/console/slaves", nil)
			},
			allow: "GET, POST",
		},
		{
			name: "restart rejects get",
			req: func(base string) (*http.Request, error) {
				return http.NewRequest(http.MethodGet, base+"/api/console/slaves/slave-1/restart", nil)
			},
			allow: http.MethodPost,
		},
		{
			name: "pause rejects delete",
			req: func(base string) (*http.Request, error) {
				return http.NewRequest(http.MethodDelete, base+"/api/console/slaves/slave-1/pause", nil)
			},
			allow: http.MethodPost,
		},
		{
			name: "delete rejects post",
			req: func(base string) (*http.Request, error) {
				return http.NewRequest(http.MethodPost, base+"/api/console/slaves/slave-1", nil)
			},
			allow: http.MethodDelete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc := &fakeConsoleController{}
			srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, cc))
			defer srv.Close()
			req, err := tt.req(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("status=%d", resp.StatusCode)
			}
			if resp.Header.Get("Allow") != tt.allow {
				t.Fatalf("Allow=%q", resp.Header.Get("Allow"))
			}
			if cc.createdInput.Folder != "" || cc.restartedID != "" || cc.pausedID != "" || cc.deletedID != "" {
				t.Fatalf("controller should not be called: %+v", cc)
			}
		})
	}
}

func TestServerConsoleSlaveEndpointUnknownShapesReturnNotFound(t *testing.T) {
	tests := []string{
		"/api/console/slaves/",
		"/api/console/slaves/slave-1/unknown",
		"/api/console/slaves/slave-1/restart/extra",
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			srv := httptest.NewServer(NewServerWithConsole(noopOrchestrator{}, &fakeConsoleController{}))
			defer srv.Close()
			resp, err := http.Post(srv.URL+path, "application/json", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status=%d", resp.StatusCode)
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
	selectedFolderCalled bool
	selectedFolder       string
	loggedOutModelserver bool
	quit                 bool
	machine              slave.Machine
	slaves               []slave.Slave
	listedSlaves         bool
	createdInput         slave.CreateInput
	createdSlave         slave.Slave
	createErr            error
	restartedID          string
	pausedID             string
	deletedID            string
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
func (f *fakeConsoleController) SelectFolder(context.Context) (string, error) {
	f.selectedFolderCalled = true
	return f.selectedFolder, nil
}
func (f *fakeConsoleController) LogoutModelserver(context.Context) error {
	f.loggedOutModelserver = true
	return nil
}
func (f *fakeConsoleController) Quit(context.Context) error {
	f.quit = true
	return nil
}
func (f *fakeConsoleController) Slaves(context.Context) (slave.Machine, []slave.Slave, error) {
	f.listedSlaves = true
	return f.machine, f.slaves, nil
}
func (f *fakeConsoleController) CreateSlave(_ context.Context, in slave.CreateInput) (slave.Slave, error) {
	f.createdInput = in
	if f.createErr != nil {
		return slave.Slave{}, f.createErr
	}
	if f.createdSlave.ID != "" {
		return f.createdSlave, nil
	}
	return slave.Slave{ID: "slave-1", Name: in.Name, Folder: in.Folder, Status: slave.StatusAuthRequired}, nil
}
func (f *fakeConsoleController) RestartSlave(_ context.Context, id string) (slave.Slave, error) {
	f.restartedID = id
	return slave.Slave{ID: id, Status: slave.StatusStarting}, nil
}
func (f *fakeConsoleController) PauseSlave(_ context.Context, id string) (slave.Slave, error) {
	f.pausedID = id
	return slave.Slave{ID: id, Status: slave.StatusPaused}, nil
}
func (f *fakeConsoleController) DeleteSlave(_ context.Context, id string) error {
	f.deletedID = id
	return nil
}

func errWrap(msg string, err error) error {
	return fmt.Errorf("%s: %w", msg, err)
}
