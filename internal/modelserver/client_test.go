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
