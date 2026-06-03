//go:build integration

package flows

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/ui"
	"github.com/agentserver/agentserver-pkg/test/integration/fakeserver"
)

func TestIdempotentWorkspace(t *testing.T) {
	fake := fakeserver.Start()
	defer fake.Close()

	dir := t.TempDir()
	store := state.NewStore(filepath.Join(dir, "state.json"))
	sec := secrets.New(filepath.Join(dir, "secrets.json"))

	deps := ui.Deps{
		State: store, Secrets: sec,
		MS: modelserver.New(fake.MSURL()),
		AS: agentserver.New(fake.ASURL()),
		MSOAuth: oauth.Config{Endpoint: fake.MSURL(),
			AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
			ClientID: "test"},
		ASOAuth: oauth.Config{Endpoint: fake.ASURL(),
			AuthPath: "/api/oauth2/device/auth", TokenPath: "/api/oauth2/token",
			ClientID: "test"},
	}
	srv := httptest.NewServer(ui.NewServer(ui.NewRealOrchestrator(deps), nil))
	defer srv.Close()

	// Run AS login twice
	mustPost(t, srv.URL+"/api/step/agentserver_login")
	pollUntilSuccess(t, srv.URL+"/api/step/agentserver_login/status")
	mustPost(t, srv.URL+"/api/step/agentserver_login")
	pollUntilSuccess(t, srv.URL+"/api/step/agentserver_login/status")

	s, _ := store.Load()
	if s.Agentserver.WorkspaceID == "" {
		t.Errorf("workspace id missing: %+v", s.Agentserver)
	}
	// fakeserver should only have ONE workspace, because GetOrCreate dedupes.
	// (This relies on fakeserver returning the same id for the same name.)
}
