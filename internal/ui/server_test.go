package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerStateEndpoint(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}, nil))
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
	srv := httptest.NewServer(NewServer(noopOrchestrator{}, nil))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/step/modelserver_login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["user_code"] != "TEST" {
		t.Errorf("got %+v", body)
	}
}

func TestServerStaticAsset(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}, nil))
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
