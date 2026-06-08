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

func jwtWithWorkspaceClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadBytes, err := json.Marshal(claims)
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

func TestWorkspaceIDFromTokenHydraExtClaim(t *testing.T) {
	got, ok := WorkspaceIDFromToken(jwtWithWorkspaceClaims(t, map[string]any{
		"ext": map[string]string{"workspace_id": "ws-ext"},
	}))
	if !ok || got != "ws-ext" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestWorkspaceFromTokenHydraExtName(t *testing.T) {
	got, ok := WorkspaceFromToken(jwtWithWorkspaceClaims(t, map[string]any{
		"ext": map[string]string{
			"workspace_id":   "ws-ext",
			"workspace_name": "Readable workspace",
		},
	}))
	if !ok || got.ID != "ws-ext" || got.Name != "Readable workspace" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestResolveWorkspaceIDUsesTokenClaimWithoutClient(t *testing.T) {
	got, err := ResolveWorkspaceID(context.Background(), nil, jwtWithWorkspace(t, "ws-claim"), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "ws-claim" {
		t.Fatalf("got %+v", got)
	}
}

func TestWhoamiUsesSandboxProxyToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/whoami":
			if r.Header.Get("Authorization") != "Bearer sandbox-proxy-token" {
				t.Fatalf("Authorization=%q", r.Header.Get("Authorization"))
			}
			json.NewEncoder(w).Encode(map[string]any{
				"workspace_id":   "ws-whoami",
				"workspace_name": "Joined workspace",
			})
		case "/api/workspaces":
			t.Fatalf("/api/workspaces should not be called for agent whoami")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	got, err := New(srv.URL).Whoami(context.Background(), "sandbox-proxy-token")
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace.ID != "ws-whoami" || got.Workspace.Name != "Joined workspace" {
		t.Fatalf("got %+v", got)
	}
}
