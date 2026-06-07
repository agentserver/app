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
