package modelserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// Profile hits /api/oauth/profile and returns the account + project bound to
// the access token (modelserver PR #63). Verifies both the active-subscription
// shape (subscription_created_at, project_type populated) and the
// free-tier shape (omit those fields).
func TestProfileReadsAccountAndProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/oauth/profile" {
			t.Fatalf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer AT" {
			t.Fatalf("auth %q", got)
		}
		_, _ = w.Write([]byte(`{
			"account": {
				"uuid": "user_01",
				"email": "alice@example.com",
				"display_name": "Alice",
				"created_at": "2024-03-15T10:00:00Z"
			},
			"project": {
				"uuid": "proj_01",
				"project_type": "max",
				"rate_limit_tier": "max_5x",
				"seat_tier": "max_5x",
				"has_extra_usage_enabled": true,
				"billing_type": "stripe",
				"cc_onboarding_flags": {},
				"subscription_created_at": "2025-01-01T00:00:00Z"
			}
		}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL).Profile(context.Background(), "AT")
	if err != nil {
		t.Fatal(err)
	}
	if got.Account.UUID != "user_01" || got.Account.Email != "alice@example.com" {
		t.Errorf("account = %+v", got.Account)
	}
	if got.Project.UUID != "proj_01" || got.Project.ProjectType != "max" {
		t.Errorf("project = %+v", got.Project)
	}
	if !got.Project.HasExtraUsageEnabled {
		t.Errorf("has_extra_usage_enabled should be true")
	}
	if got.Project.SubscriptionCreatedAt != "2025-01-01T00:00:00Z" {
		t.Errorf("subscription_created_at = %q", got.Project.SubscriptionCreatedAt)
	}
}

func TestProfileFreeTierOmitsOptionalFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"account": {"uuid": "u", "email": "", "display_name": "", "created_at": ""},
			"project": {
				"uuid": "p", "rate_limit_tier": null, "seat_tier": null,
				"has_extra_usage_enabled": false, "billing_type": null,
				"cc_onboarding_flags": {}
			}
		}`))
	}))
	defer srv.Close()
	got, err := New(srv.URL).Profile(context.Background(), "AT")
	if err != nil {
		t.Fatal(err)
	}
	if got.Project.UUID != "p" || got.Project.ProjectType != "" || got.Project.SubscriptionCreatedAt != "" {
		t.Errorf("free-tier project = %+v", got.Project)
	}
}

func TestProfilePropagatesNon2xxAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "oauth token required", http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Profile(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("err = %v, want status 401", err)
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
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Window != "5h" || got[0].Percentage != 58.2 {
		t.Fatalf("got[0]=%+v", got[0])
	}
	if got[1].Window != "7d" || got[1].Percentage != 22.0 {
		t.Fatalf("got[1]=%+v", got[1])
	}
	if got[0].ResetsAt != "2026-06-07T12:34:56Z" {
		t.Fatalf("resets_at=%q", got[0].ResetsAt)
	}
}

func TestProxyUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/usage" {
			t.Fatalf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer AT" {
			t.Fatalf("auth %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"credit_usage":[{"window":"5h","percentage":58.2,"window_end":"2026-06-08T17:00:00Z"},{"window":"7d","percentage":22.0}]}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL).ProxyUsage(context.Background(), "AT")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Window != "5h" || got[0].Percentage != 58.2 || got[0].ResetsAt != "2026-06-08T17:00:00Z" {
		t.Fatalf("got[0]=%+v", got[0])
	}
	if got[1].Window != "7d" || got[1].Percentage != 22.0 {
		t.Fatalf("got[1]=%+v", got[1])
	}
}

func TestNewClientUsesBoundedHTTPTimeout(t *testing.T) {
	got := New("https://code.cs.ac.cn").http.Timeout
	if got != 60*time.Second {
		t.Fatalf("timeout=%s, want 60s", got)
	}
}

func TestErrorResponseDoesNotEchoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Authorization: Bearer secret-token", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := New(srv.URL).ListProjects(context.Background(), "secret-token")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("error leaked response body: %q", err)
	}
	if !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("error=%q, want status", err)
	}
}

