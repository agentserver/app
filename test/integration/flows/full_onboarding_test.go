//go:build integration

package flows

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/ui"
	"github.com/agentserver/agentserver-pkg/test/integration/fakeserver"
)

func TestFullOnboarding_MS_AS(t *testing.T) {
	fake := fakeserver.Start()
	defer fake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	sec := secrets.New(filepath.Join(dir, "secrets.json"))

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	msPort := l.Addr().(*net.TCPAddr).Port
	l.Close()

	deps := ui.Deps{
		State: store, Secrets: sec,
		MS:      modelserver.New(fake.MSURL()),
		MSProxy: modelserver.New(fake.MSURL()),
		AS:      agentserver.New(fake.ASURL()),
		MSOAuth: oauth.AuthCodeConfig{
			Endpoint:     fake.MSURL(),
			AuthPath:     "/oauth2/auth",
			TokenPath:    "/oauth2/token",
			ClientID:     "test",
			Scope:        "project:inference offline_access",
			CallbackPath: "/oauth/modelserver/callback",
			Ports:        []int{msPort},
			LoginTimeout: 3 * time.Second,
		},
		ASOAuth: oauth.Config{Endpoint: fake.ASURL(),
			AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
			ClientID: "test", Scope: "openid"},
		OpenBrowser: func(url string) { _, _ = http.Get(url) },
	}
	orch := ui.NewRealOrchestrator(deps)

	srv := httptest.NewServer(ui.NewServer(orch))
	defer srv.Close()

	// STEP 1 MS login
	mustPost(t, srv.URL+"/api/step/modelserver_login")
	pollUntilSuccess(t, srv.URL+"/api/step/modelserver_login/status")

	// STEP 2 AS login
	mustPost(t, srv.URL+"/api/step/agentserver_login")
	pollUntilSuccess(t, srv.URL+"/api/step/agentserver_login/status")

	// Verify state
	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("modelserver_login") ||
		!s.Onboarding.HasCompleted("agentserver_login") {
		t.Errorf("steps not complete: %+v", s.Onboarding.CompletedSteps)
	}
	if s.Modelserver.ProjectID == "" {
		t.Fatalf("modelserver project id missing")
	}
	if s.Agentserver.WorkspaceID == "" {
		t.Fatalf("agentserver workspace id missing")
	}
	if s.Modelserver.APIKeySuffix == "" || s.Agentserver.WorkspaceAPIKeySuffix == "" {
		t.Errorf("missing key suffixes: %+v", s)
	}
	// Verify secrets stored
	if v, err := sec.Get("modelserver_api_key"); err != nil || v == "" {
		t.Errorf("ms key not stored: %v", err)
	}
	if v, err := sec.Get("modelserver_refresh_token"); err != nil || v == "" {
		t.Errorf("ms refresh token not stored: %v", err)
	}
	if v, err := sec.Get("modelserver_access_token_expires_at"); err != nil || v == "" {
		t.Errorf("ms token expiry not stored: %v", err)
	}
	if v, err := sec.Get("agentserver_ws_api_key"); err != nil || v == "" {
		t.Errorf("ws key not stored: %v", err)
	}
}

func mustPost(t *testing.T, url string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: status %d: %s", url, resp.StatusCode, body)
	}
}

func pollUntilSuccess(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Post(url, "application/json", nil)
		if err != nil {
			t.Fatalf("POST %s: %v", url, err)
		}
		var body map[string]any
		json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if body["state"] == "success" {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("step %s did not reach success in time", url)
}

// Used to keep imports alive should helpers shrink.
var _ = bytes.NewReader
var _ = os.Open
