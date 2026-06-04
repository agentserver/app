package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerStateEndpoint(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	var s SanitizedState
	json.NewDecoder(resp.Body).Decode(&s)
}

func TestServerStepEndpoint(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}))
	defer srv.Close()

	// MS login
	resp, err := http.Post(srv.URL+"/api/step/modelserver_login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("MS status %d", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["state"] != "started" {
		t.Errorf("MS got %+v, want state=started", body)
	}
	if body["oauth_url"] == nil || body["oauth_url"] == "" {
		t.Errorf("MS missing oauth_url: %+v", body)
	}

	// AS login (same response shape now)
	resp2, err := http.Post(srv.URL+"/api/step/agentserver_login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("AS status %d", resp2.StatusCode)
	}
	var body2 map[string]any
	json.NewDecoder(resp2.Body).Decode(&body2)
	if body2["state"] != "started" {
		t.Errorf("AS got %+v, want state=started", body2)
	}
	if body2["oauth_url"] == nil || body2["oauth_url"] == "" {
		t.Errorf("AS missing oauth_url: %+v", body2)
	}
}

func TestServerStaticAsset(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
}
