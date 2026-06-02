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
