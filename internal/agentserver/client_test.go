package agentserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetOrCreateDefaultWorkspace_Existing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/workspaces" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []Workspace{{ID: "ws-1", Name: "default"}},
			})
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	c := New(srv.URL)
	ws, err := c.GetOrCreateDefaultWorkspace(context.Background(), "AT", "default")
	if err != nil {
		t.Fatal(err)
	}
	if ws.ID != "ws-1" {
		t.Errorf("got %+v", ws)
	}
}

func TestGetOrCreateDefaultWorkspace_Creates(t *testing.T) {
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/workspaces":
			json.NewEncoder(w).Encode(map[string]any{"data": []Workspace{}})
		case r.Method == "POST" && r.URL.Path == "/api/workspaces":
			created = true
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["name"] != "default" {
				t.Errorf("name %q", body["name"])
			}
			json.NewEncoder(w).Encode(map[string]Workspace{"data": {ID: "ws-new", Name: "default"}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := New(srv.URL)
	ws, err := c.GetOrCreateDefaultWorkspace(context.Background(), "AT", "default")
	if err != nil {
		t.Fatal(err)
	}
	if !created || ws.ID != "ws-new" {
		t.Errorf("got %+v, created=%v", ws, created)
	}
}

func TestCreateWorkspaceAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/workspaces/ws-1/api-keys" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": WorkspaceAPIKey{ID: "k1", WorkspaceID: "ws-1", Name: "x", KeySuffix: "abcd"},
			"key":  "ws-sk-aaaaaa",
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	k, err := c.CreateWorkspaceAPIKey(context.Background(), "AT", "ws-1", "x")
	if err != nil {
		t.Fatal(err)
	}
	if k.Secret != "ws-sk-aaaaaa" {
		t.Errorf("got %+v", k)
	}
}

func TestRegisterAgentUsesOAuthToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/agent/register" {
			t.Fatalf("got %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer oauth-token" {
			t.Fatalf("Authorization=%q", r.Header.Get("Authorization"))
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["name"] != "星池指挥官" || body["type"] != "custom" {
			t.Fatalf("body=%+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"sandbox_id":   "sb-1",
			"tunnel_token": "tunnel-token",
			"proxy_token":  "sandbox-proxy-token",
			"workspace_id": "ws-1",
			"short_id":     "abc123",
		})
	}))
	defer srv.Close()

	got, err := New(srv.URL).RegisterAgent(context.Background(), "oauth-token", "星池指挥官", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProxyToken != "sandbox-proxy-token" || got.WorkspaceID != "ws-1" {
		t.Fatalf("got %+v", got)
	}
}
