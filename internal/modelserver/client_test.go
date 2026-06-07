package modelserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListProjects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects" {
			t.Errorf("path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer AT" {
			t.Errorf("auth %q", got)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []Project{{ID: "p1", Name: "default"}, {ID: "p2", Name: "other"}},
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	ps, err := c.ListProjects(context.Background(), "AT")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != "p1" {
		t.Errorf("got %+v", ps)
	}
}

func TestCreateProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/projects" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "default" {
			t.Errorf("name %q", body["name"])
		}
		json.NewEncoder(w).Encode(map[string]Project{
			"data": {ID: "new-1", Name: "default"},
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	p, err := c.CreateProject(context.Background(), "AT", "default")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "new-1" {
		t.Errorf("got %+v", p)
	}
}

func TestCreateAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects/p1/keys" {
			t.Errorf("path %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": APIKey{ID: "k1", ProjectID: "p1", Name: "x", KeySuffix: "wxyz", Status: "active"},
			"key":  "ms-1234567890abcdef",
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	k, err := c.CreateAPIKey(context.Background(), "AT", "p1", "x")
	if err != nil {
		t.Fatal(err)
	}
	if k.Secret != "ms-1234567890abcdef" || k.ID != "k1" {
		t.Errorf("got %+v", k)
	}
}

func TestPickOrCreateProject_FoundDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": []Project{{ID: "p1", Name: "default"}},
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	p, err := c.PickOrCreateProject(context.Background(), "AT", "default")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "p1" {
		t.Errorf("expected existing, got %+v", p)
	}
}

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
