//go:build integration

package flows

import (
	"net"
	"net/http"
	"net/http/httptest"
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

func TestResumeAfterRestart(t *testing.T) {
	fake := fakeserver.Start()
	defer fake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	sec := secrets.New(filepath.Join(dir, "secrets.json"))

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	msPort := l.Addr().(*net.TCPAddr).Port
	l.Close()

	mkServer := func() *httptest.Server {
		deps := ui.Deps{
			State: store, Secrets: sec,
			MS: modelserver.New(fake.MSURL()),
			AS: agentserver.New(fake.ASURL()),
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
				ClientID: "test"},
			OpenBrowser: func(url string) { _, _ = http.Get(url) },
		}
		return httptest.NewServer(ui.NewServer(ui.NewRealOrchestrator(deps), nil))
	}

	srv := mkServer()
	mustPost(t, srv.URL+"/api/step/modelserver_login")
	pollUntilSuccess(t, srv.URL+"/api/step/modelserver_login/status")
	srv.Close() // simulate kill

	srv2 := mkServer()
	defer srv2.Close()
	// state retains MS done; AS should still work
	mustPost(t, srv2.URL+"/api/step/agentserver_login")
	pollUntilSuccess(t, srv2.URL+"/api/step/agentserver_login/status")

	s, _ := store.Load()
	if !s.Onboarding.HasCompleted("modelserver_login") ||
		!s.Onboarding.HasCompleted("agentserver_login") {
		t.Errorf("steps after resume: %+v", s.Onboarding.CompletedSteps)
	}
}
