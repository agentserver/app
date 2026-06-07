# Tray Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a persistent browser-based 星池指挥官 dashboard with Windows tray status, quota reminders, startup single-instance behavior, and OAuth project/workspace ID persistence.

**Architecture:** Keep the existing embedded Vue UI and Go HTTP server, but add a completed-state dashboard and local console APIs. `launcher.exe` becomes a single-instance console host after onboarding completion; `open-folder.exe` ensures that console host is running before opening a folder. A platform-isolated tray package owns Windows tray UI and notification calls, with a no-op implementation for non-Windows tests.

**Tech Stack:** Go 1.26, Vue 3, Element Plus, Windows Shell APIs via `golang.org/x/sys/windows` or a pure-Go Win32 binding, existing modelserver/agentserver HTTP clients, existing Inno/Make packaging.

---

## File Structure

- `internal/modelserver/types.go`, `internal/modelserver/client.go`, `internal/modelserver/client_test.go`: add subscription usage types and API client method.
- `internal/agentserver/workspace.go`, `internal/agentserver/workspace_test.go`: add workspace ID resolver from JWT claim or workspace list.
- `internal/ui/orchestrator_real.go`, `internal/ui/orchestrator_real_test.go`: require OAuth flows to write `Modelserver.ProjectID` and `Agentserver.WorkspaceID` before marking login steps complete.
- `internal/console/state.go`, `internal/console/state_test.go`: aggregate local state, remote project/workspace details, quota usage, and subscription URL.
- `internal/console/reminder.go`, `internal/console/reminder_test.go`: decide when 50%/80% quota reminders fire.
- `internal/console/instance.go`, `internal/console/instance_test.go`: discover existing console instances via a port file and health endpoint.
- `internal/tray/tray.go`, `internal/tray/tray_other.go`, `internal/tray/tray_windows.go`: platform boundary for tray menu, tooltip, quit, and notifications.
- `internal/ui/server.go`, `internal/ui/server_test.go`: add console HTTP endpoints while preserving onboarding endpoints.
- `internal/ui/web/src/api.ts`: add console API functions and types.
- `internal/ui/web/src/components/Dashboard.vue`: render completed-state dashboard.
- `internal/ui/web/src/components/QuotaCard.vue`: render one quota window.
- `internal/ui/web/src/__tests__/Dashboard.spec.ts`, `internal/ui/web/src/__tests__/api.spec.ts`: frontend coverage.
- `cmd/launcher/main.go`, `cmd/launcher/main_test.go`: add `--background`, single-instance reuse, persistent console server, and completed-mode launch behavior.
- `cmd/open-folder/main.go`, `cmd/open-folder/main_test.go`: ensure console host before opening a folder.
- `Makefile`: build `launcher.exe` and `open-folder.exe` as Windows GUI subsystem binaries.

---

### Task 1: modelserver quota client and agentserver workspace resolver

**Files:**
- Modify: `internal/modelserver/types.go`
- Modify: `internal/modelserver/client.go`
- Modify: `internal/modelserver/client_test.go`
- Create: `internal/agentserver/workspace.go`
- Create: `internal/agentserver/workspace_test.go`

- [ ] **Step 1: Write failing modelserver subscription usage tests**

Append this test to `internal/modelserver/client_test.go`:

```go
func TestSubscriptionUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/projects/p1/subscription/usage" {
			t.Fatalf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer AT" {
			t.Fatalf("auth %q", got)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"window": "5h", "percentage": 58.2, "resets_at": "2026-06-07T12:34:56Z"},
				{"window": "7d", "percentage": 22.0},
			},
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	got, err := c.SubscriptionUsage(context.Background(), "AT", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Window != "5h" || got[0].Percentage != 58.2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].ResetsAt != "2026-06-07T12:34:56Z" {
		t.Fatalf("resets_at=%q", got[0].ResetsAt)
	}
}
```

- [ ] **Step 2: Run modelserver test and verify it fails**

Run:

```bash
go test ./internal/modelserver -run TestSubscriptionUsage -count=1
```

Expected: FAIL with `c.SubscriptionUsage undefined`.

- [ ] **Step 3: Implement modelserver usage type and method**

Add to `internal/modelserver/types.go`:

```go
type SubscriptionUsageWindow struct {
	Window     string  `json:"window"`
	Percentage float64 `json:"percentage"`
	ResetsAt   string  `json:"resets_at,omitempty"`
}
```

Add to `internal/modelserver/client.go`:

```go
func (c *Client) SubscriptionUsage(ctx context.Context, token, projectID string) ([]SubscriptionUsageWindow, error) {
	var wrap struct {
		Data []SubscriptionUsageWindow `json:"data"`
	}
	if projectID == "" {
		return nil, fmt.Errorf("modelserver: projectID required")
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/subscription/usage", token, nil, &wrap); err != nil {
		return nil, err
	}
	return wrap.Data, nil
}
```

- [ ] **Step 4: Write failing agentserver workspace resolver tests**

Create `internal/agentserver/workspace_test.go`:

```go
package agentserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func jwtWithWorkspace(t *testing.T, workspaceID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadBytes, err := json.Marshal(map[string]string{"workspace_id": workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".sig"
}

func TestWorkspaceIDFromTokenClaim(t *testing.T) {
	got, ok := WorkspaceIDFromToken(jwtWithWorkspace(t, "ws-claim"))
	if !ok || got != "ws-claim" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestResolveWorkspaceIDUsesTokenClaim(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		t.Fatalf("ListWorkspaces should not be called when token has workspace_id")
	}))
	defer srv.Close()

	got, err := ResolveWorkspaceID(context.Background(), New(srv.URL), jwtWithWorkspace(t, "ws-claim"), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "ws-claim" || called {
		t.Fatalf("got %+v called=%v", got, called)
	}
}

func TestResolveWorkspaceIDFallsBackToWorkspaceList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/workspaces" {
			t.Fatalf("got %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []Workspace{{ID: "ws-1", Name: "Default workspace"}},
		})
	}))
	defer srv.Close()

	got, err := ResolveWorkspaceID(context.Background(), New(srv.URL), "opaque-token", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "ws-1" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveWorkspaceIDPrefersExistingID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": []Workspace{{ID: "ws-1", Name: "A"}, {ID: "ws-2", Name: "B"}},
		})
	}))
	defer srv.Close()

	got, err := ResolveWorkspaceID(context.Background(), New(srv.URL), "opaque-token", "ws-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "ws-2" {
		t.Fatalf("got %+v", got)
	}
}
```

- [ ] **Step 5: Run agentserver resolver tests and verify they fail**

Run:

```bash
go test ./internal/agentserver -run 'TestWorkspaceIDFromTokenClaim|TestResolveWorkspaceID' -count=1
```

Expected: FAIL with `WorkspaceIDFromToken undefined` or `ResolveWorkspaceID undefined`.

- [ ] **Step 6: Implement workspace resolver**

Create `internal/agentserver/workspace.go`:

```go
package agentserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func WorkspaceIDFromToken(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", false
	}
	v, ok := claims["workspace_id"].(string)
	return v, ok && v != ""
}

func ResolveWorkspaceID(ctx context.Context, c *Client, token, existingID string) (Workspace, error) {
	if id, ok := WorkspaceIDFromToken(token); ok {
		return Workspace{ID: id}, nil
	}
	if c == nil {
		return Workspace{}, fmt.Errorf("agentserver: client required")
	}
	ws, err := c.ListWorkspaces(ctx, token)
	if err != nil {
		return Workspace{}, err
	}
	if existingID != "" {
		for _, w := range ws {
			if w.ID == existingID {
				return w, nil
			}
		}
	}
	if len(ws) == 0 {
		return Workspace{}, fmt.Errorf("agentserver: no workspaces available")
	}
	return ws[0], nil
}
```

- [ ] **Step 7: Run focused tests**

Run:

```bash
go test ./internal/modelserver ./internal/agentserver -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/modelserver internal/agentserver
git commit -m "feat(api): add quota and workspace resolvers"
```

---

### Task 2: OAuth flows must persist project and workspace IDs

**Files:**
- Modify: `internal/ui/orchestrator_real.go`
- Modify: `internal/ui/orchestrator_real_test.go`
- Modify: `test/integration/flows/full_onboarding_test.go`
- Modify: `test/integration/flows/idempotent_test.go`

- [ ] **Step 1: Update modelserver PKCE test to require ProjectID**

In `internal/ui/orchestrator_real_test.go`, update `TestPollModelserverLogin_FullPKCE`:

```go
mux.HandleFunc("/api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		t.Errorf("/api/v1/projects got method %s", r.Method)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer fake-at" {
		t.Errorf("/api/v1/projects auth %q", got)
	}
	w.Write([]byte(`{"data":[{"id":"proj-1","name":"default"}]}`))
})
```

Replace the old empty-project assertion with:

```go
if s.Modelserver.ProjectID != "proj-1" {
	t.Errorf("project id = %q, want proj-1", s.Modelserver.ProjectID)
}
if !s.Onboarding.HasCompleted("modelserver_login") {
	t.Errorf("step not marked completed")
}
```

- [ ] **Step 2: Add failing modelserver project resolution failure test**

Append this test to `internal/ui/orchestrator_real_test.go`:

```go
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
```

- [ ] **Step 3: Add failing agentserver workspace ID tests**

Append this helper and tests to `internal/ui/orchestrator_real_test.go`:

```go
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
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"` + jwtWithASWorkspace(t, "ws-claim") + `","token_type":"Bearer","expires_in":3600}`))
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
	r.asChallenge = oauth.DeviceCodeChallenge{DeviceCode: "dev", Interval: 1}
	key, err := r.PollAgentserverLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if key.Secret == "" {
		t.Fatal("secret missing")
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
	r.asChallenge = oauth.DeviceCodeChallenge{DeviceCode: "dev", Interval: 1}
	if _, err := r.PollAgentserverLogin(context.Background()); err == nil {
		t.Fatal("expected workspace resolution error")
	}
	s, _ := store.Load()
	if s.Onboarding.HasCompleted("agentserver_login") {
		t.Fatal("agentserver_login should not complete without WorkspaceID")
	}
}
```

Also add imports if missing:

```go
import (
	"encoding/base64"
	"encoding/json"
)
```

- [ ] **Step 4: Run tests and verify failures**

Run:

```bash
go test ./internal/ui -run 'TestPollModelserverLogin|TestPollAgentserverLogin' -count=1
```

Expected: FAIL because project/workspace IDs are still not written.

- [ ] **Step 5: Implement ProjectID write in `PollModelserverLogin`**

In `internal/ui/orchestrator_real.go`, after `tokenrefresh.StoreToken` succeeds and before `State.Update`, resolve a project:

```go
project, err := r.d.MS.PickOrCreateProject(ctx, tok.AccessToken, "default")
if err != nil {
	r.cleanupMS()
	return modelserver.APIKey{}, fmt.Errorf("resolve modelserver project: %w", err)
}
if project.ID == "" {
	r.cleanupMS()
	return modelserver.APIKey{}, fmt.Errorf("resolve modelserver project: empty project id")
}
```

Inside the existing `State.Update`, set:

```go
s.Modelserver.ProjectID = project.ID
s.Modelserver.APIKeySuffix = key.KeySuffix
s.Onboarding.AddCompleted("modelserver_login")
```

- [ ] **Step 6: Implement WorkspaceID write in `PollAgentserverLogin`**

In `internal/ui/orchestrator_real.go`, after saving `agentserver_ws_api_key` and before `State.Update`, load current state and resolve the workspace:

```go
current, err := r.d.State.Load()
if err != nil {
	return agentserver.WorkspaceAPIKey{}, err
}
workspace, err := agentserver.ResolveWorkspaceID(ctx, r.d.AS, tok.AccessToken, current.Agentserver.WorkspaceID)
if err != nil {
	return agentserver.WorkspaceAPIKey{}, fmt.Errorf("resolve agentserver workspace: %w", err)
}
if workspace.ID == "" {
	return agentserver.WorkspaceAPIKey{}, fmt.Errorf("resolve agentserver workspace: empty workspace id")
}
```

Inside `State.Update`, set:

```go
s.Agentserver.WorkspaceID = workspace.ID
s.Agentserver.WorkspaceAPIKeySuffix = key.KeySuffix
s.Onboarding.AddCompleted("agentserver_login")
```

- [ ] **Step 7: Update integration tests that expected empty IDs**

In `test/integration/flows/full_onboarding_test.go` and `test/integration/flows/idempotent_test.go`, remove comments saying IDs are intentionally empty and assert that IDs are non-empty:

```go
if s.Modelserver.ProjectID == "" {
	t.Fatalf("modelserver project id missing")
}
if s.Agentserver.WorkspaceID == "" {
	t.Fatalf("agentserver workspace id missing")
}
```

Update `test/integration/fakeserver/fakeserver.go` so PKCE/device-token flows can resolve IDs. In `Start()`, initialize defaults before registering handlers:

```go
s := &Server{
	projects: []map[string]string{{"id": "proj-1", "name": "default"}},
	wsList:   []map[string]string{{"id": "ws-1", "name": "Default workspace"}},
}
```

- [ ] **Step 8: Run focused tests**

Run:

```bash
go test ./internal/ui ./test/integration/flows -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/ui test/integration/flows
git commit -m "fix(oauth): persist project and workspace ids"
```

---

### Task 3: Console state aggregation and quota reminders

**Files:**
- Create: `internal/console/state.go`
- Create: `internal/console/state_test.go`
- Create: `internal/console/reminder.go`
- Create: `internal/console/reminder_test.go`
- Modify: `internal/paths/paths.go`
- Modify: `internal/paths/paths_test.go`

- [ ] **Step 1: Add console paths tests**

Extend `internal/paths/paths_test.go`:

```go
func TestPathsIncludesConsoleRuntimeFiles(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.ConsolePortFile == "" {
		t.Fatal("ConsolePortFile empty")
	}
	if p.ConsoleNotificationsFile == "" {
		t.Fatal("ConsoleNotificationsFile empty")
	}
}
```

- [ ] **Step 2: Run paths test and verify it fails**

Run:

```bash
go test ./internal/paths -run TestPathsIncludesConsoleRuntimeFiles -count=1
```

Expected: FAIL because fields do not exist.

- [ ] **Step 3: Add console paths**

Modify `internal/paths/paths.go`:

```go
// Add these fields to Paths immediately after CacheDir.
ConsolePortFile          string
ConsoleNotificationsFile string
```

Set them in `Default()`:

```go
ConsolePortFile:          filepath.Join(root, "console-port.json"),
ConsoleNotificationsFile: filepath.Join(root, "console-notifications.json"),
```

- [ ] **Step 4: Write failing console state tests**

Create `internal/console/state_test.go`:

```go
package console

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
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
	sec := secrets.New(p.SecretsFile)
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
	sec := secrets.New(p.SecretsFile)
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
```

- [ ] **Step 5: Write failing reminder tests**

Create `internal/console/reminder_test.go`:

```go
package console

import (
	"path/filepath"
	"testing"
)

func TestReminderThresholdsFireOncePerWindow(t *testing.T) {
	store := NewMemoryReminderStore()
	r := ReminderEngine{Store: store}
	got := r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 49, ResetsAt: "r1"}})
	if len(got) != 0 {
		t.Fatalf("49%% should not notify: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 50, ResetsAt: "r1"}})
	if len(got) != 1 || got[0].Threshold != 50 {
		t.Fatalf("50%% notification missing: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 70, ResetsAt: "r1"}})
	if len(got) != 0 {
		t.Fatalf("same threshold repeated: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 80, ResetsAt: "r1"}})
	if len(got) != 1 || got[0].Threshold != 80 {
		t.Fatalf("80%% notification missing: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 82, ResetsAt: "r1"}})
	if len(got) != 0 {
		t.Fatalf("80%% repeated: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "5h", Percentage: 50, ResetsAt: "r2"}})
	if len(got) != 1 || got[0].Threshold != 50 {
		t.Fatalf("new reset window should notify again: %+v", got)
	}
}

func TestReminderTreatsUsageDropWithoutResetAsNewWindow(t *testing.T) {
	store := NewMemoryReminderStore()
	r := ReminderEngine{Store: store}
	got := r.Evaluate([]QuotaWindow{{Window: "7d", Percentage: 82}})
	if len(got) != 2 {
		t.Fatalf("initial 82%% should notify 50 and 80: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "7d", Percentage: 40}})
	if len(got) != 0 {
		t.Fatalf("drop below threshold should only reset cycle: %+v", got)
	}
	got = r.Evaluate([]QuotaWindow{{Window: "7d", Percentage: 50}})
	if len(got) != 1 || got[0].Threshold != 50 {
		t.Fatalf("new local cycle should notify 50 again: %+v", got)
	}
}

func TestFileReminderStorePersistsSeenState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-notifications.json")
	store := NewFileReminderStore(path)
	store.Mark("5h", "r1", 50)
	store.SetLastPercentage("5h", 58)

	reloaded := NewFileReminderStore(path)
	if !reloaded.Seen("5h", "r1", 50) {
		t.Fatal("seen threshold was not persisted")
	}
	if got, ok := reloaded.LastPercentage("5h"); !ok || got != 58 {
		t.Fatalf("last percentage got %v ok=%v", got, ok)
	}
}
```

- [ ] **Step 6: Run console tests and verify failures**

Run:

```bash
go test ./internal/console ./internal/paths -count=1
```

Expected: FAIL because `internal/console` does not exist and new path fields are missing.

- [ ] **Step 7: Implement console state and reminders**

Create `internal/console/state.go` with these public types and behavior:

```go
package console

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

type Deps struct {
	State secretsStateStore
	Secrets secrets.Store
	MS *modelserver.Client
	AS *agentserver.Client
	ModelserverWebBaseURL string
	OpenFrontend func(context.Context) error
	OpenURL func(string) error
	Quit func()
}

type secretsStateStore interface {
	Load() (*state.State, error)
	Update(func(*state.State) error) error
}

type State struct {
	FrontendMode string `json:"frontend_mode"`
	FrontendName string `json:"frontend_name"`
	OnboardingStatus string `json:"onboarding_status"`
	Modelserver ModelserverView `json:"modelserver"`
	Agentserver AgentserverView `json:"agentserver"`
	Quotas []QuotaWindow `json:"quotas"`
	QuotaError string `json:"quota_error,omitempty"`
	SubscriptionURL string `json:"subscription_url,omitempty"`
	LastRefreshedAt string `json:"last_refreshed_at"`
}

type ModelserverView struct {
	ProjectID string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
}

type AgentserverView struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
}

type QuotaWindow struct {
	Window string `json:"window"`
	Percentage float64 `json:"percentage"`
	RemainingPercentage float64 `json:"remaining_percentage"`
	ResetsAt string `json:"resets_at,omitempty"`
}

type Controller struct { d Deps }

func NewController(d Deps) *Controller { return &Controller{d: d} }

func (c *Controller) State(ctx context.Context) (State, error) {
	st, err := c.d.State.Load()
	if err != nil {
		return State{}, err
	}
	out := State{
		FrontendMode: string(state.NormalizeFrontendMode(st.FrontendMode)),
		FrontendName: frontendName(state.NormalizeFrontendMode(st.FrontendMode)),
		OnboardingStatus: string(st.Onboarding.Status),
		Modelserver: ModelserverView{ProjectID: st.Modelserver.ProjectID},
		Agentserver: AgentserverView{WorkspaceID: st.Agentserver.WorkspaceID},
		LastRefreshedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if out.Modelserver.ProjectID != "" {
		out.SubscriptionURL = strings.TrimRight(defaultString(c.d.ModelserverWebBaseURL, "https://code.cs.ac.cn"), "/") +
			"/projects/" + out.Modelserver.ProjectID + "/subscription"
	}
	msToken, _ := c.d.Secrets.Get("modelserver_api_key")
	asToken, _ := c.d.Secrets.Get("agentserver_ws_api_key")
	if c.d.MS != nil && msToken != "" {
		projects, err := c.d.MS.ListProjects(ctx, msToken)
		if err == nil {
			for _, p := range projects {
				if p.ID == out.Modelserver.ProjectID {
					out.Modelserver.ProjectName = p.Name
				}
			}
		}
		if out.Modelserver.ProjectID != "" {
			usage, err := c.d.MS.SubscriptionUsage(ctx, msToken, out.Modelserver.ProjectID)
			if err != nil {
				out.QuotaError = err.Error()
			} else {
				out.Quotas = quotaWindows(usage)
			}
		}
	}
	if c.d.AS != nil && asToken != "" {
		workspaces, err := c.d.AS.ListWorkspaces(ctx, asToken)
		if err == nil {
			for _, w := range workspaces {
				if w.ID == out.Agentserver.WorkspaceID {
					out.Agentserver.WorkspaceName = w.Name
				}
			}
		}
	}
	return out, nil
}

func (c *Controller) Refresh(ctx context.Context) (State, error) {
	return c.State(ctx)
}

func (c *Controller) OpenFrontend(ctx context.Context) error {
	if c.d.OpenFrontend == nil {
		return nil
	}
	return c.d.OpenFrontend(ctx)
}

func (c *Controller) OpenSubscription(ctx context.Context) error {
	st, err := c.State(ctx)
	if err != nil {
		return err
	}
	if st.SubscriptionURL == "" {
		return errors.New("console: subscription URL unavailable")
	}
	if c.d.OpenURL == nil {
		return nil
	}
	return c.d.OpenURL(st.SubscriptionURL)
}

func (c *Controller) Quit(context.Context) error {
	if c.d.Quit != nil {
		c.d.Quit()
	}
	return nil
}

func quotaWindows(in []modelserver.SubscriptionUsageWindow) []QuotaWindow {
	out := make([]QuotaWindow, 0, len(in))
	for _, w := range in {
		remaining := math.Round(math.Max(0, 100-w.Percentage)*100) / 100
		out = append(out, QuotaWindow{
			Window: w.Window, Percentage: w.Percentage,
			RemainingPercentage: remaining, ResetsAt: w.ResetsAt,
		})
	}
	return out
}

func frontendName(mode state.FrontendMode) string {
	if state.NormalizeFrontendMode(mode) == state.FrontendModeMinimalVSCode {
		return "极简界面"
	}
	return "Codex Desktop"
}

func defaultString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
```

Create `internal/console/reminder.go`:

```go
package console

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Reminder struct {
	Window string
	Threshold int
	Percentage float64
}

type ReminderStore interface {
	Seen(window, resetKey string, threshold int) bool
	Mark(window, resetKey string, threshold int)
	LastPercentage(window string) (float64, bool)
	SetLastPercentage(window string, percentage float64)
	ClearWindow(window string)
}

type ReminderEngine struct {
	Store ReminderStore
}

func (r ReminderEngine) Evaluate(windows []QuotaWindow) []Reminder {
	var out []Reminder
	for _, w := range windows {
		resetKey := w.ResetsAt
		if resetKey == "" {
			resetKey = "current"
			if last, ok := r.Store.LastPercentage(w.Window); ok && w.Percentage < last {
				r.Store.ClearWindow(w.Window)
			}
			r.Store.SetLastPercentage(w.Window, w.Percentage)
		}
		for _, threshold := range []int{50, 80} {
			if w.Percentage < float64(threshold) || r.Store.Seen(w.Window, resetKey, threshold) {
				continue
			}
			r.Store.Mark(w.Window, resetKey, threshold)
			out = append(out, Reminder{Window: w.Window, Threshold: threshold, Percentage: w.Percentage})
		}
	}
	return out
}

type MemoryReminderStore struct {
	seen map[string]bool
	last map[string]float64
}

func NewMemoryReminderStore() *MemoryReminderStore {
	return &MemoryReminderStore{seen: map[string]bool{}, last: map[string]float64{}}
}

func (m *MemoryReminderStore) Seen(window, resetKey string, threshold int) bool {
	return m.seen[window+"|"+resetKey+"|"+strconv.Itoa(threshold)]
}

func (m *MemoryReminderStore) Mark(window, resetKey string, threshold int) {
	m.seen[window+"|"+resetKey+"|"+strconv.Itoa(threshold)] = true
}

func (m *MemoryReminderStore) LastPercentage(window string) (float64, bool) {
	v, ok := m.last[window]
	return v, ok
}

func (m *MemoryReminderStore) SetLastPercentage(window string, percentage float64) {
	m.last[window] = percentage
}

func (m *MemoryReminderStore) ClearWindow(window string) {
	prefix := window + "|"
	for key := range m.seen {
		if strings.HasPrefix(key, prefix) {
			delete(m.seen, key)
		}
	}
}

type FileReminderStore struct {
	path string
	mem *MemoryReminderStore
}

type reminderDiskState struct {
	Seen map[string]bool `json:"seen"`
	Last map[string]float64 `json:"last"`
}

func NewFileReminderStore(path string) *FileReminderStore {
	f := &FileReminderStore{path: path, mem: NewMemoryReminderStore()}
	f.load()
	return f
}

func (f *FileReminderStore) load() {
	b, err := os.ReadFile(f.path)
	if err != nil {
		return
	}
	var disk reminderDiskState
	if err := json.Unmarshal(b, &disk); err != nil {
		return
	}
	if disk.Seen != nil {
		f.mem.seen = disk.Seen
	}
	if disk.Last != nil {
		f.mem.last = disk.Last
	}
}

func (f *FileReminderStore) save() {
	if f.path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(f.path), 0o755)
	disk := reminderDiskState{Seen: f.mem.seen, Last: f.mem.last}
	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(f.path, b, 0o644)
}

func (f *FileReminderStore) Seen(window, resetKey string, threshold int) bool {
	return f.mem.Seen(window, resetKey, threshold)
}

func (f *FileReminderStore) Mark(window, resetKey string, threshold int) {
	f.mem.Mark(window, resetKey, threshold)
	f.save()
}

func (f *FileReminderStore) LastPercentage(window string) (float64, bool) {
	return f.mem.LastPercentage(window)
}

func (f *FileReminderStore) SetLastPercentage(window string, percentage float64) {
	f.mem.SetLastPercentage(window, percentage)
	f.save()
}

func (f *FileReminderStore) ClearWindow(window string) {
	f.mem.ClearWindow(window)
	f.save()
}
```

- [ ] **Step 8: Run focused tests**

Run:

```bash
go test ./internal/console ./internal/paths -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/console internal/paths
git commit -m "feat(console): aggregate dashboard state"
```

---

### Task 4: Console HTTP endpoints

**Files:**
- Modify: `internal/ui/server.go`
- Modify: `internal/ui/server_test.go`
- Create: `internal/ui/console.go`

- [ ] **Step 1: Write failing server endpoint test**

Append to `internal/ui/server_test.go`:

```go
func TestServerConsoleStateEndpoint(t *testing.T) {
	cc := &fakeConsoleController{
		state: console.State{
			FrontendMode: "codex_desktop",
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

type fakeConsoleController struct {
	state console.State
	openedFrontend bool
}

func (f *fakeConsoleController) State(context.Context) (console.State, error) {
	return f.state, nil
}
func (f *fakeConsoleController) Refresh(context.Context) (console.State, error) {
	return f.state, nil
}
func (f *fakeConsoleController) OpenFrontend(context.Context) error {
	f.openedFrontend = true
	return nil
}
func (f *fakeConsoleController) OpenSubscription(context.Context) error { return nil }
func (f *fakeConsoleController) Quit(context.Context) error { return nil }
```

Ensure the `internal/ui/server_test.go` import block includes:

```go
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/console"
)
```

- [ ] **Step 2: Run server tests and verify failures**

Run:

```bash
go test ./internal/ui -run TestServerConsole -count=1
```

Expected: FAIL because `NewServerWithConsole` is missing.

- [ ] **Step 3: Add console controller interface**

Create `internal/ui/console.go`:

```go
package ui

import (
	"context"

	"github.com/agentserver/agentserver-pkg/internal/console"
)

type ConsoleController interface {
	State(context.Context) (console.State, error)
	Refresh(context.Context) (console.State, error)
	OpenFrontend(context.Context) error
	OpenSubscription(context.Context) error
	Quit(context.Context) error
}

type noopConsoleController struct{}

func (noopConsoleController) State(context.Context) (console.State, error) { return console.State{}, nil }
func (noopConsoleController) Refresh(context.Context) (console.State, error) { return console.State{}, nil }
func (noopConsoleController) OpenFrontend(context.Context) error { return nil }
func (noopConsoleController) OpenSubscription(context.Context) error { return nil }
func (noopConsoleController) Quit(context.Context) error { return nil }
```

- [ ] **Step 4: Wire console endpoints**

Modify `internal/ui/server.go`:

```go
func NewServer(o Orchestrator) http.Handler {
	return NewServerWithConsole(o, noopConsoleController{})
}

func NewServerWithConsole(o Orchestrator, c ConsoleController) http.Handler {
	s := &server{o: o, c: c, sse: newSSEHub()}
	mux := http.NewServeMux()
	staticFS, _ := fs.Sub(assetsFS, "assets/dist")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/step/modelserver_login", s.handleMSLogin)
	mux.HandleFunc("/api/step/modelserver_login/status", s.handleMSStatus)
	mux.HandleFunc("/api/step/agentserver_login", s.handleASLogin)
	mux.HandleFunc("/api/step/agentserver_login/status", s.handleASStatus)
	mux.HandleFunc("/api/step/frontend_install", s.handleFrontendInstall)
	mux.HandleFunc("/api/step/frontend_configure", s.handleFrontendConfigure)
	mux.HandleFunc("/api/step/vscode_install", s.handleFrontendInstall)
	mux.HandleFunc("/api/step/vscode_configure", s.handleFrontendConfigure)
	mux.HandleFunc("/api/finalize", s.handleFinalize)
	mux.HandleFunc("/api/abort", s.handleAbort)
	mux.HandleFunc("/api/launch", s.handleLaunch)
	mux.HandleFunc("/api/launch-vscode", s.handleLaunch)

	mux.HandleFunc("/api/console/state", s.handleConsoleState)
	mux.HandleFunc("/api/console/refresh", s.handleConsoleRefresh)
	mux.HandleFunc("/api/console/open-frontend", s.handleConsoleOpenFrontend)
	mux.HandleFunc("/api/console/open-subscription", s.handleConsoleOpenSubscription)
	mux.HandleFunc("/api/console/quit", s.handleConsoleQuit)

	mux.HandleFunc("/api/events", s.sse.handle)
	return mux
}
```

Extend `server`:

```go
type server struct {
	o Orchestrator
	c ConsoleController
	sse *sseHub
}
```

Add handlers:

```go
func (s *server) handleConsoleState(w http.ResponseWriter, r *http.Request) {
	st, err := s.c.State(r.Context())
	if err != nil { writeErr(w, 500, err); return }
	writeJSON(w, 200, st)
}

func (s *server) handleConsoleRefresh(w http.ResponseWriter, r *http.Request) {
	st, err := s.c.Refresh(r.Context())
	if err != nil { writeErr(w, 500, err); return }
	writeJSON(w, 200, st)
}

func (s *server) handleConsoleOpenFrontend(w http.ResponseWriter, r *http.Request) {
	if err := s.c.OpenFrontend(r.Context()); err != nil { writeErr(w, 500, err); return }
	writeJSON(w, 200, map[string]string{"state": "opened"})
}

func (s *server) handleConsoleOpenSubscription(w http.ResponseWriter, r *http.Request) {
	if err := s.c.OpenSubscription(r.Context()); err != nil { writeErr(w, 500, err); return }
	writeJSON(w, 200, map[string]string{"state": "opened"})
}

func (s *server) handleConsoleQuit(w http.ResponseWriter, r *http.Request) {
	if err := s.c.Quit(r.Context()); err != nil { writeErr(w, 500, err); return }
	writeJSON(w, 200, map[string]string{"state": "quitting"})
}
```

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./internal/ui -run TestServerConsole -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui
git commit -m "feat(ui): add console endpoints"
```

---

### Task 5: Completed-state dashboard UI

**Files:**
- Modify: `internal/ui/web/src/api.ts`
- Modify: `internal/ui/web/src/App.vue`
- Create: `internal/ui/web/src/components/Dashboard.vue`
- Create: `internal/ui/web/src/components/QuotaCard.vue`
- Create: `internal/ui/web/src/__tests__/Dashboard.spec.ts`
- Modify: `internal/ui/web/src/__tests__/api.spec.ts`

- [ ] **Step 1: Write failing API tests**

Append to `internal/ui/web/src/__tests__/api.spec.ts`:

```ts
it('getConsoleState returns dashboard state', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue({
    ok: true,
    status: 200,
    json: async () => ({
      frontend_mode: 'codex_desktop',
      frontend_name: 'Codex Desktop',
      subscription_url: 'https://code.cs.ac.cn/projects/proj-1/subscription',
      quotas: [{ window: '5h', percentage: 58, remaining_percentage: 42 }],
    }),
  } as Response);
  const s = await api.getConsoleState();
  expect(s.quotas[0].window).toBe('5h');
});

it('openConsoleFrontend POSTs to console endpoint', async () => {
  const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
    ok: true,
    status: 200,
    json: async () => ({ state: 'opened' }),
  } as Response);
  await api.openConsoleFrontend();
  expect(fetchSpy).toHaveBeenCalledWith('/api/console/open-frontend', expect.objectContaining({ method: 'POST' }));
});
```

- [ ] **Step 2: Write failing dashboard component test**

Create `internal/ui/web/src/__tests__/Dashboard.spec.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import Dashboard from '../components/Dashboard.vue';
import * as api from '../api';

describe('Dashboard', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('renders project, workspace, quota, and subscription action', async () => {
    vi.spyOn(api, 'getConsoleState').mockResolvedValue({
      frontend_mode: 'codex_desktop',
      frontend_name: 'Codex Desktop',
      onboarding_status: 'complete',
      modelserver: { project_id: 'proj-1', project_name: 'Default project' },
      agentserver: { workspace_id: 'ws-1', workspace_name: 'Default workspace' },
      subscription_url: 'https://code.cs.ac.cn/projects/proj-1/subscription',
      quotas: [
        { window: '5h', percentage: 58, remaining_percentage: 42 },
        { window: '7d', percentage: 22, remaining_percentage: 78 },
      ],
      last_refreshed_at: '2026-06-07T12:00:00Z',
    });
    const w = mount(Dashboard);
    await Promise.resolve();
    await Promise.resolve();
    expect(w.text()).toContain('Default project');
    expect(w.text()).toContain('Default workspace');
    expect(w.text()).toContain('5小时');
    expect(w.text()).toContain('剩余约 42%');
  });
});
```

- [ ] **Step 3: Run frontend tests and verify failures**

Run:

```bash
cd internal/ui/web && npm test -- api.spec.ts Dashboard.spec.ts
```

Expected: FAIL because console API and dashboard components are missing.

- [ ] **Step 4: Add console API types and functions**

Modify `internal/ui/web/src/api.ts`:

```ts
export interface ConsoleQuota {
  window: string;
  percentage: number;
  remaining_percentage: number;
  resets_at?: string;
}

export interface ConsoleState {
  frontend_mode: 'codex_desktop' | 'minimal_vscode';
  frontend_name: string;
  onboarding_status: string;
  modelserver: { project_id?: string; project_name?: string };
  agentserver: { workspace_id?: string; workspace_name?: string };
  quotas: ConsoleQuota[];
  quota_error?: string;
  subscription_url?: string;
  last_refreshed_at?: string;
}

export const getConsoleState = () => request<ConsoleState>('/api/console/state');
export const refreshConsoleState = () => request<ConsoleState>('/api/console/refresh', { method: 'POST' });
export const openConsoleFrontend = () => request<{ state: 'opened' }>('/api/console/open-frontend', { method: 'POST' });
export const openConsoleSubscription = () => request<{ state: 'opened' }>('/api/console/open-subscription', { method: 'POST' });
```

- [ ] **Step 5: Add quota card**

Create `internal/ui/web/src/components/QuotaCard.vue`:

```vue
<script setup lang="ts">
import type { ConsoleQuota } from '../api';

defineProps<{ quota: ConsoleQuota }>();

function label(window: string) {
  if (window === '5h') return '5小时额度';
  if (window === '7d') return '7天额度';
  return `${window} 额度`;
}
</script>

<template>
  <section class="quota-card">
    <div class="quota-head">
      <strong>{{ label(quota.window) }}</strong>
      <span>已用 {{ quota.percentage }}%</span>
    </div>
    <el-progress :percentage="quota.percentage" :stroke-width="10" />
    <div class="quota-meta">
      <span>剩余约 {{ quota.remaining_percentage }}%</span>
      <span v-if="quota.resets_at">重置 {{ new Date(quota.resets_at).toLocaleString() }}</span>
    </div>
  </section>
</template>
```

- [ ] **Step 6: Add dashboard component**

Create `internal/ui/web/src/components/Dashboard.vue`:

```vue
<script setup lang="ts">
import { onMounted, ref } from 'vue';
import * as api from '../api';
import QuotaCard from './QuotaCard.vue';

const state = ref<api.ConsoleState | null>(null);
const error = ref('');
const opening = ref(false);

async function load() {
  try {
    state.value = await api.getConsoleState();
    error.value = '';
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
}

async function refresh() {
  try {
    state.value = await api.refreshConsoleState();
    error.value = '';
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
}

async function openFrontend() {
  opening.value = true;
  try {
    await api.openConsoleFrontend();
  } finally {
    opening.value = false;
  }
}

async function openSubscription() {
  await api.openConsoleSubscription();
}

onMounted(load);
</script>

<template>
  <div class="dashboard">
    <header class="dashboard-head">
      <div>
        <h1>星池指挥官</h1>
        <p>{{ state?.frontend_name || '正在读取状态' }}</p>
      </div>
      <div class="dashboard-actions">
        <el-button @click="refresh">刷新状态</el-button>
        <el-button type="primary" :loading="opening" @click="openFrontend">
          打开 {{ state?.frontend_name || '前端' }}
        </el-button>
      </div>
    </header>

    <el-alert v-if="error" type="error" :title="error" :closable="false" show-icon />
    <el-alert v-if="state?.quota_error" type="warning" :title="state.quota_error" :closable="false" show-icon />

    <section class="quota-grid">
      <QuotaCard v-for="q in state?.quotas || []" :key="q.window" :quota="q" />
    </section>

    <section class="connection-grid">
      <div class="info-block">
        <span>modelserver 项目</span>
        <strong>{{ state?.modelserver.project_name || state?.modelserver.project_id || '未读取到项目' }}</strong>
      </div>
      <div class="info-block">
        <span>agentserver 工作空间</span>
        <strong>{{ state?.agentserver.workspace_name || state?.agentserver.workspace_id || '未读取到工作空间' }}</strong>
      </div>
    </section>

    <el-button :disabled="!state?.subscription_url" @click="openSubscription">打开订阅页</el-button>
  </div>
</template>
```

- [ ] **Step 7: Render dashboard when onboarding complete**

Modify `internal/ui/web/src/App.vue`:

```vue
<script setup lang="ts">
import Dashboard from './components/Dashboard.vue';
// keep existing imports
</script>

<template>
  <div class="container">
    <Dashboard v-if="onboarding.isComplete.value" />
    <template v-else>
      <!-- existing onboarding h1, alerts, StepCard loop -->
    </template>
  </div>
</template>
```

Remove `SuccessBanner` usage from completed-state rendering. Leave the component in the repo until unused cleanup is safe.

- [ ] **Step 8: Run frontend tests**

Run:

```bash
cd internal/ui/web && npm test
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/ui/web/src
git commit -m "feat(ui): show completed dashboard"
```

---

### Task 6: Single-instance console discovery

**Files:**
- Create: `internal/console/instance.go`
- Create: `internal/console/instance_test.go`

- [ ] **Step 1: Write failing instance tests**

Create `internal/console/instance_test.go`:

```go
package console

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverInstanceUsesHealthyPortFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/console/health" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		w.Write([]byte(`{"state":"ok"}`))
	}))
	defer srv.Close()
	port := serverPort(t, srv.URL)
	dir := t.TempDir()
	path := filepath.Join(dir, "console-port.json")
	if err := WriteInstanceInfo(path, InstanceInfo{Port: port, PID: 123}); err != nil {
		t.Fatal(err)
	}
	got, ok := DiscoverInstance(context.Background(), path)
	if !ok || got.Port != port {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestDiscoverInstanceDeletesStalePortFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console-port.json")
	if err := WriteInstanceInfo(path, InstanceInfo{Port: 1, PID: 123}); err != nil {
		t.Fatal(err)
	}
	if _, ok := DiscoverInstance(context.Background(), path); ok {
		t.Fatal("stale instance should not be healthy")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale port file should be removed, err=%v", err)
	}
}

func serverPort(t *testing.T, raw string) int {
	t.Helper()
	var port int
	if _, err := fmt.Sscanf(raw, "http://127.0.0.1:%d", &port); err != nil {
		t.Fatal(err)
	}
	return port
}
```

- [ ] **Step 2: Run instance tests and verify failures**

Run:

```bash
go test ./internal/console -run TestDiscoverInstance -count=1
```

Expected: FAIL because instance functions are missing.

- [ ] **Step 3: Implement instance discovery**

Create `internal/console/instance.go`:

```go
package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type InstanceInfo struct {
	Port int `json:"port"`
	PID int `json:"pid"`
	StartedAt string `json:"started_at,omitempty"`
}

func WriteInstanceInfo(path string, info InstanceInfo) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info.StartedAt == "" {
		info.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func DiscoverInstance(ctx context.Context, path string) (InstanceInfo, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return InstanceInfo{}, false
	}
	var info InstanceInfo
	if err := json.Unmarshal(b, &info); err != nil || info.Port <= 0 {
		_ = os.Remove(path)
		return InstanceInfo{}, false
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/console/health", info.Port), nil)
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode/100 != 2 {
		if resp != nil {
			resp.Body.Close()
		}
		_ = os.Remove(path)
		return InstanceInfo{}, false
	}
	resp.Body.Close()
	return info, true
}
```

- [ ] **Step 4: Run focused tests**

Run:

```bash
go test ./internal/console -run TestDiscoverInstance -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/console/instance.go internal/console/instance_test.go
git commit -m "feat(console): discover running instance"
```

---

### Task 7: Launcher persistent console mode

**Files:**
- Modify: `cmd/launcher/main.go`
- Modify: `cmd/launcher/main_test.go`

- [ ] **Step 1: Write failing launcher option tests**

Append to `cmd/launcher/main_test.go`:

```go
func TestLauncherOptionsDefaultOpensPageAndFrontend(t *testing.T) {
	got := parseLauncherOptions([]string{})
	if got.Background || !got.OpenPage || !got.OpenFrontend {
		t.Fatalf("options=%+v", got)
	}
}

func TestLauncherOptionsBackgroundDoesNotOpenPageOrFrontend(t *testing.T) {
	got := parseLauncherOptions([]string{"--background"})
	if !got.Background || got.OpenPage || got.OpenFrontend {
		t.Fatalf("options=%+v", got)
	}
}
```

- [ ] **Step 2: Write failing existing-instance behavior test**

Append:

```go
func TestCompletedLauncherReusesExistingConsole(t *testing.T) {
	called := launcherCalls{}
	err := runCompletedConsole(context.Background(), completedConsoleDeps{
		Options: launcherOptions{OpenPage: true, OpenFrontend: true},
		PortFile: "ignored",
		Discover: func(context.Context, string) (console.InstanceInfo, bool) {
			return console.InstanceInfo{Port: 34567}, true
		},
		OpenBrowser: func(url string) error {
			called.openedURL = url
			return nil
		},
		Post: func(ctx context.Context, url string) error {
			called.posted = append(called.posted, url)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(called.openedURL, "127.0.0.1:34567") {
		t.Fatalf("openedURL=%q", called.openedURL)
	}
	if len(called.posted) != 1 || !strings.Contains(called.posted[0], "/api/console/open-frontend") {
		t.Fatalf("posted=%+v", called.posted)
	}
}

type launcherCalls struct {
	openedURL string
	posted []string
}
```

Use this import addition in `cmd/launcher/main_test.go`:

```go
import (
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/console"
)
```

- [ ] **Step 3: Run launcher tests and verify failures**

Run:

```bash
go test ./cmd/launcher -run 'TestLauncherOptions|TestCompletedLauncherReusesExistingConsole' -count=1
```

Expected: FAIL because helpers are missing.

- [ ] **Step 4: Add launcher option parsing and completed console deps**

In `cmd/launcher/main.go`, add:

```go
type launcherOptions struct {
	Background bool
	OpenPage bool
	OpenFrontend bool
}

func parseLauncherOptions(args []string) launcherOptions {
	opts := launcherOptions{OpenPage: true, OpenFrontend: true}
	for _, arg := range args {
		if arg == "--background" {
			opts.Background = true
			opts.OpenPage = false
			opts.OpenFrontend = false
		}
	}
	return opts
}
```

Change `run()` to:

```go
func run() error {
	return runWithOptions(context.Background(), parseLauncherOptions(os.Args[1:]))
}

func runWithOptions(ctx context.Context, opts launcherOptions) error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.InstallRoot, 0o755); err != nil {
		return err
	}
	exe, _ := os.Executable()
	installDir := osDir(exe)
	store := state.NewStore(p.StateFile)
	if err := installmode.SyncStoreIfPresent(store, installmode.PathForExecutable(exe)); err != nil {
		return err
	}
	s, err := store.Load()
	if err != nil {
		return err
	}

	if s.Onboarding.Status == state.StatusComplete {
		err := runCompletedConsole(ctx, completedConsoleDeps{
			Options: opts,
			PortFile: p.ConsolePortFile,
			OpenBrowser: browser.Open,
			Post: postConsole,
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, errNoRunningConsole) {
			return err
		}
		return serveCompletedConsole(ctx, completedServeInput{
			Paths: p,
			State: store,
			Secrets: secrets.New(p.SecretsFile),
			InstallDir: installDir,
			Options: opts,
		})
	}

	return serveOnboarding(p, store)
}
```

- [ ] **Step 5: Add existing-instance reuse helper**

Add to `cmd/launcher/main.go`:

```go
type completedConsoleDeps struct {
	Options launcherOptions
	PortFile string
	Discover func(context.Context, string) (console.InstanceInfo, bool)
	OpenBrowser func(string) error
	Post func(context.Context, string) error
}

func runCompletedConsole(ctx context.Context, d completedConsoleDeps) error {
	discover := d.Discover
	if discover == nil {
		discover = console.DiscoverInstance
	}
	post := d.Post
	if post == nil {
		post = postConsole
	}
	if info, ok := discover(ctx, d.PortFile); ok {
		base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)
		if d.Options.OpenPage && d.OpenBrowser != nil {
			if err := d.OpenBrowser(base + "/"); err != nil {
				return err
			}
		}
		if d.Options.OpenFrontend {
			if err := post(ctx, base+"/api/console/open-frontend"); err != nil {
				return err
			}
		}
		return nil
	}
	return errNoRunningConsole
}

func postConsole(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("console POST %s: status %d", url, resp.StatusCode)
	}
	return nil
}
```

Add:

```go
var errNoRunningConsole = errors.New("no running console")
```

When `runWithOptions` sees completed onboarding, call `runCompletedConsole`; if it returns `errNoRunningConsole`, start a new persistent console server.

- [ ] **Step 6: Start persistent console server for completed state**

The completed branch in `runWithOptions` now calls `serveCompletedConsole`; keep direct `launchCompletedFrontend(...)` usage only inside the console controller's `OpenFrontend` callback.

Ensure the existing `cmd/launcher/main.go` import block includes:

```go
	"errors"
	"github.com/agentserver/agentserver-pkg/internal/console"
```

Create `serveCompletedConsole` in `cmd/launcher/main.go`:

```go
type completedServeInput struct {
	Paths paths.Paths
	State *state.Store
	Secrets secrets.Store
	InstallDir string
	Options launcherOptions
	OpenBrowser func(string) error
}

func serveCompletedConsole(ctx context.Context, in completedServeInput) error {
	sec := in.Secrets
	if sec == nil {
		sec = secrets.New(in.Paths.SecretsFile)
	}
	openBrowser := in.OpenBrowser
	if openBrowser == nil {
		openBrowser = browser.Open
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{}
	ctrl := console.NewController(console.Deps{
		State: in.State,
		Secrets: sec,
		MS: modelserver.New("https://codeapi.cs.ac.cn"),
		AS: agentserver.New("https://agent.cs.ac.cn"),
		ModelserverWebBaseURL: "https://code.cs.ac.cn",
		OpenURL: openBrowser,
		OpenFrontend: func(ctx context.Context) error {
			current, err := in.State.Load()
			if err != nil {
				return err
			}
			return launchCompletedFrontend(ctx, current, in.Paths, sec,
				joinExe(in.InstallDir, "token-refresher.exe"),
				joinExe(in.InstallDir, "agentserver-vscode.vsix"),
				nil)
		},
		Quit: func() {
			go srv.Shutdown(context.Background())
		},
	})
	srv.Handler = ui.NewServerWithConsole(ui.NewNoopOrchestrator(), ctrl)

	port := ln.Addr().(*net.TCPAddr).Port
	if err := console.WriteInstanceInfo(in.Paths.ConsolePortFile, console.InstanceInfo{Port: port, PID: os.Getpid()}); err != nil {
		ln.Close()
		return err
	}
	defer os.Remove(in.Paths.ConsolePortFile)

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if in.Options.OpenPage {
		go func() { _ = openBrowser(base + "/") }()
	}
	if in.Options.OpenFrontend {
		go func() { _ = ctrl.OpenFrontend(ctx) }()
	}

	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
```

- [ ] **Step 7: Add console health endpoint**

In `internal/ui/server.go`, add:

```go
mux.HandleFunc("/api/console/health", func(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"state": "ok"})
})
```

- [ ] **Step 8: Run launcher and UI tests**

Run:

```bash
go test ./cmd/launcher ./internal/ui ./internal/console -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add cmd/launcher internal/ui
git commit -m "feat(launcher): host persistent console"
```

---

### Task 8: open-folder starts background console when needed

**Files:**
- Modify: `cmd/open-folder/main.go`
- Modify: `cmd/open-folder/main_test.go`

- [ ] **Step 1: Write failing open-folder background tests**

Append to `cmd/open-folder/main_test.go`:

```go
func TestEnsureConsoleStartsLauncherWhenMissing(t *testing.T) {
	calls := 0
	err := ensureConsoleBackground(context.Background(), consoleBackgroundDeps{
		LauncherExe: "launcher.exe",
		PortFile: "console-port.json",
		Discover: func(context.Context, string) (console.InstanceInfo, bool) {
			return console.InstanceInfo{}, false
		},
		Start: func(string, ...string) error {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("start calls=%d", calls)
	}
}

func TestEnsureConsoleDoesNotStartWhenHealthy(t *testing.T) {
	calls := 0
	err := ensureConsoleBackground(context.Background(), consoleBackgroundDeps{
		LauncherExe: "launcher.exe",
		PortFile: "console-port.json",
		Discover: func(context.Context, string) (console.InstanceInfo, bool) {
			return console.InstanceInfo{Port: 1234}, true
		},
		Start: func(string, ...string) error {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("start calls=%d", calls)
	}
}
```

Add import `github.com/agentserver/agentserver-pkg/internal/console`.

- [ ] **Step 2: Run tests and verify failures**

Run:

```bash
go test ./cmd/open-folder -run TestEnsureConsole -count=1
```

Expected: FAIL because helper types/functions are missing.

- [ ] **Step 3: Implement background console helper**

In `cmd/open-folder/main.go`, add:

```go
type consoleBackgroundDeps struct {
	LauncherExe string
	PortFile string
	Discover func(context.Context, string) (console.InstanceInfo, bool)
	Start func(string, ...string) error
}

func ensureConsoleBackground(ctx context.Context, d consoleBackgroundDeps) error {
	discover := d.Discover
	if discover == nil {
		discover = console.DiscoverInstance
	}
	if _, ok := discover(ctx, d.PortFile); ok {
		return nil
	}
	if d.LauncherExe == "" {
		return nil
	}
	start := d.Start
	if start == nil {
		start = startDetached
	}
	return start(d.LauncherExe, "--background")
}

func startDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
```

Call it in `main()` after `paths.Default()` and executable path discovery:

```go
_ = ensureConsoleBackground(context.Background(), consoleBackgroundDeps{
	LauncherExe: filepath.Join(installDir, "launcher.exe"),
	PortFile: p.ConsolePortFile,
})
```

- [ ] **Step 4: Run tests**

Run:

```bash
go test ./cmd/open-folder -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/open-folder
git commit -m "feat(open-folder): start console background"
```

---

### Task 9: Tray abstraction, Windows tray, and quota notification loop

**Files:**
- Create: `internal/tray/tray.go`
- Create: `internal/tray/tray_other.go`
- Create: `internal/tray/tray_windows.go`
- Modify: `cmd/launcher/main.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Create no-op tray boundary with tests by compile**

Create `internal/tray/tray.go`:

```go
package tray

import "context"

type State struct {
	Tooltip string
	FiveHour string
	SevenDay string
}

type Actions struct {
	OpenDashboard func()
	OpenFrontend func()
	OpenSubscription func()
	Quit func()
}

type App interface {
	Run(context.Context, Actions) error
	Update(State)
	Notify(title, message string) error
}
```

Create `internal/tray/tray_other.go`:

```go
//go:build !windows

package tray

import "context"

type noopApp struct{}

func New(iconPath string) App { return noopApp{} }
func (noopApp) Run(ctx context.Context, actions Actions) error {
	<-ctx.Done()
	return ctx.Err()
}
func (noopApp) Update(State) {}
func (noopApp) Notify(string, string) error { return nil }
```

Run:

```bash
go test ./internal/tray -count=1
```

Expected: PASS on Linux.

- [ ] **Step 2: Add Windows tray implementation**

Implement `internal/tray/tray_windows.go` using a pure-Go Windows API path. Keep all Windows-only code behind `//go:build windows`.

Preferred approach:

- Use `golang.org/x/sys/windows` for `Shell_NotifyIconW`, `LoadImageW`, and message-loop calls.
- Create a hidden window class.
- Add one notify icon with `NIF_MESSAGE | NIF_ICON | NIF_TIP`.
- On right click, show a popup menu with:
  - 打开控制台
  - 启动 Codex Desktop / 启动极简界面
  - 打开订阅页
  - disabled quota status rows
  - 退出星池指挥官
- Use `NIF_INFO` for balloon notification text.

If the implementation needs a binding package, use a pure-Go binding that cross-compiles from Linux to Windows. After adding the package, immediately verify:

```bash
GOOS=windows GOARCH=amd64 go test ./internal/tray -run TestDoesNotExist -count=1
```

Expected: command exits 0 or reports no tests, and does not fail at compile time.

- [ ] **Step 3: Add tray state formatting helper**

In `cmd/launcher/main.go` or a small `internal/console/tray_state.go`, add:

```go
func trayStateFromConsole(st console.State) tray.State {
	state := tray.State{Tooltip: "星池指挥官\n额度暂不可用", FiveHour: "5小时额度：暂不可用", SevenDay: "7天额度：暂不可用"}
	for _, q := range st.Quotas {
		line := fmt.Sprintf("%s额度：已用 %.0f%%，剩余约 %.0f%%", quotaLabel(q.Window), q.Percentage, q.RemainingPercentage)
		if q.Window == "5h" {
			state.FiveHour = line
		}
		if q.Window == "7d" {
			state.SevenDay = line
		}
	}
	state.Tooltip = "星池指挥官\n" + state.FiveHour + "\n" + state.SevenDay
	return state
}

func quotaLabel(window string) string {
	switch window {
	case "5h":
		return "5小时"
	case "7d":
		return "7天"
	default:
		return window
	}
}
```

- [ ] **Step 4: Wire tray loop in completed console server**

When `serveCompletedConsole` starts:

- create tray app with `tray.New(preferredIconPath(installDir))`,
- start it in a goroutine,
- create reminders with `console.ReminderEngine{Store: console.NewFileReminderStore(in.Paths.ConsoleNotificationsFile)}`,
- refresh console state every 60 seconds,
- call `app.Update(trayStateFromConsole(state))`,
- run `ReminderEngine.Evaluate(state.Quotas)` and call `app.Notify(...)` for each reminder.

Notification text:

```go
title := "星池指挥官额度提醒"
message := fmt.Sprintf("%s额度已用 %d%%", quotaLabel(reminder.Window), reminder.Threshold)
```

- [ ] **Step 5: Run compile verification**

Run:

```bash
go test ./internal/tray ./cmd/launcher ./internal/console -count=1
GOOS=windows GOARCH=amd64 go test ./internal/tray ./cmd/launcher -run TestDoesNotExist -count=1
```

Expected: both commands complete without compile errors.

- [ ] **Step 6: Commit**

```bash
git add internal/tray cmd/launcher go.mod go.sum
git commit -m "feat(tray): add dashboard tray status"
```

---

### Task 10: Windows GUI subsystem build and package verification

**Files:**
- Modify: `Makefile`
- Modify: `scripts/package-windows.sh` if packaging needs an extra preflight for tray assets
- Modify: `scripts/package-windows-zip.sh` if portable packaging needs the same preflight

- [ ] **Step 1: Write Makefile change**

Modify the `cross-windows` loop:

```make
cross-windows: ui-build
	@mkdir -p $(DIST)/windows
	@for cmd in $(CMDS); do \
		echo "==> cross-building $$cmd (windows/amd64)"; \
		ldflags="$(LDFLAGS)"; \
		case "$$cmd" in launcher|open-folder) ldflags="$(LDFLAGS) -H=windowsgui" ;; esac; \
		GOOS=$(GOOS_WIN) GOARCH=$(GOARCH) \
		  $(GO) build $(GOFLAGS) -ldflags="$$ldflags" \
		  -o $(DIST)/windows/$$cmd.exe ./cmd/$$cmd ; \
	done
```

- [ ] **Step 2: Verify local tests still pass**

Run:

```bash
go test ./cmd/launcher ./cmd/open-folder ./internal/tray ./internal/console -count=1
```

Expected: PASS.

- [ ] **Step 3: Verify Windows cross build**

Run:

```bash
make cross-windows
```

Expected: produces `dist/windows/launcher.exe` and `dist/windows/open-folder.exe` without console-subsystem build errors.

- [ ] **Step 4: Verify package script preflight**

Run:

```bash
bash scripts/package-windows.sh
```

Expected: produces `packaging/windows/Output/agentserver-vscode-0.1.0-setup.exe`.

- [ ] **Step 5: Commit**

```bash
git add Makefile scripts/package-windows.sh scripts/package-windows-zip.sh
git commit -m "fix(build): hide launcher console windows"
```

---

### Task 11: End-to-end Windows smoke verification

**Files:**
- Modify: `test/e2e/windows/e2e_test.go` if automated checks are practical
- No commit needed if this is a manual verification pass with no code changes

- [ ] **Step 1: Upload installer to the Windows test machine**

Run from repo root:

```bash
scp -P 2222 packaging/windows/Output/agentserver-vscode-0.1.0-setup.exe 61414@10.128.185.173:C:/Users/61414/Downloads/agentserver-vscode-0.1.0-setup-tray.exe
```

Expected: file copies successfully.

- [ ] **Step 2: Install and launch**

Run:

```bash
ssh -p 2222 61414@10.128.185.173 'powershell -NoProfile -Command "Start-Process -FilePath $env:USERPROFILE\Downloads\agentserver-vscode-0.1.0-setup-tray.exe -ArgumentList ''/silent'' -Wait"'
```

Expected: install exits 0.

- [ ] **Step 3: Verify no console window and process is persistent**

Run:

```bash
ssh -p 2222 61414@10.128.185.173 'powershell -NoProfile -Command "Start-Process -FilePath $env:LOCALAPPDATA\Programs\agentserver-vscode\launcher.exe; Start-Sleep 5; Get-Process launcher -ErrorAction SilentlyContinue | Select-Object -First 1 ProcessName,Id"'
```

Expected: a `launcher` process remains running. On the desktop session, no black command window should be visible.

- [ ] **Step 4: Verify port file and health endpoint**

Run:

```bash
ssh -p 2222 61414@10.128.185.173 'powershell -NoProfile -Command "$p = Join-Path $env:USERPROFILE ''.agentserver-vscode\console-port.json''; Get-Content $p; $j = Get-Content $p | ConvertFrom-Json; Invoke-RestMethod http://127.0.0.1:$($j.port)/api/console/health"'
```

Expected: JSON contains `state = ok`.

- [ ] **Step 5: Verify right-click helper starts background console**

Kill launcher, invoke `open-folder.exe`, then verify launcher is back:

```bash
ssh -p 2222 61414@10.128.185.173 'powershell -NoProfile -Command "Get-Process launcher -ErrorAction SilentlyContinue | Stop-Process -Force; Start-Sleep 2; & $env:LOCALAPPDATA\Programs\agentserver-vscode\open-folder.exe C:\tmp; Start-Sleep 5; Get-Process launcher -ErrorAction SilentlyContinue | Select-Object -First 1 ProcessName,Id"'
```

Expected: launcher process exists after `open-folder.exe`.

- [ ] **Step 6: Record verification**

If every check passes, no code commit is needed. If a failure requires code changes, fix with a new TDD task and run the full focused suite again:

```bash
go test ./internal/modelserver ./internal/agentserver ./internal/ui ./internal/console ./internal/tray ./cmd/launcher ./cmd/open-folder -count=1
cd internal/ui/web && npm test
make cross-windows
bash scripts/package-windows.sh
```

Expected: all commands pass before calling the implementation complete.

---

## Self-Review Checklist

- Spec coverage:
  - OAuth ID persistence: Tasks 1-2.
  - Completed dashboard page: Tasks 3-5.
  - Single-instance launcher: Tasks 6-7.
  - Right-click starts console: Task 8.
  - Tray status and reminders: Task 9.
  - No command window and packaging: Task 10.
  - Windows smoke verification: Task 11.
- Type consistency:
  - `console.State` matches frontend `ConsoleState`.
  - `QuotaWindow.Percentage` and `remaining_percentage` match API names.
  - `Modelserver.ProjectID` and `Agentserver.WorkspaceID` are written before login completion.
- Verification commands:
  - Go focused tests are listed per task.
  - Frontend tests are listed for UI changes.
  - Windows cross-build and package verification are listed before E2E.
