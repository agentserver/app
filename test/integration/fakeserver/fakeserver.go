//go:build integration

// Package fakeserver provides a single httptest.Server that emulates the
// minimal modelserver + agentserver endpoints needed for installer flows.
package fakeserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

type Server struct {
	srv *httptest.Server

	mu        sync.Mutex
	approved  bool // device-code approved?
	approveAt time.Time
	projects  []map[string]string
	keys      []map[string]any
	wsList    []map[string]string
	wsKeys    []map[string]any
}

// Start spins up the fake. Caller must Close. Approval is automatic
// after 50ms by default to keep tests fast.
func Start() *Server {
	s := &Server{}
	mux := http.NewServeMux()

	// ---- modelserver routes ----
	mux.HandleFunc("/api/oauth2/device/auth", s.handleDeviceAuth)
	mux.HandleFunc("/api/oauth2/token", s.handleToken)
	mux.HandleFunc("/api/v1/projects", s.handleProjects)
	mux.HandleFunc("/api/v1/projects/", s.handleProjectsSub) // .../keys

	// ---- agentserver routes ----
	mux.HandleFunc("/api/workspaces", s.handleWorkspaces)
	mux.HandleFunc("/api/workspaces/", s.handleWorkspacesSub) // .../api-keys

	s.srv = httptest.NewServer(mux)
	return s
}

func (s *Server) Close()        { s.srv.Close() }
func (s *Server) URL() string   { return s.srv.URL }
func (s *Server) MSURL() string { return s.srv.URL }
func (s *Server) ASURL() string { return s.srv.URL }

func (s *Server) Approve() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approved = true
	s.approveAt = time.Now()
}

func (s *Server) handleDeviceAuth(w http.ResponseWriter, r *http.Request) {
	// Auto-approve after 50ms to keep tests fast.
	go func() { time.Sleep(50 * time.Millisecond); s.Approve() }()
	writeJSON(w, 200, map[string]any{
		"device_code":               "dev-fake",
		"user_code":                 "ABCD-EFGH",
		"verification_uri":          s.srv.URL + "/verify",
		"verification_uri_complete": s.srv.URL + "/verify?u=ABCD-EFGH",
		"expires_in":                30,
		"interval":                  1,
	})
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	ok := s.approved
	s.mu.Unlock()
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "authorization_pending"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"access_token": "fake-access", "token_type": "Bearer", "expires_in": 3600,
	})
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"data": s.projects})
	case http.MethodPost:
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		defer s.mu.Unlock()
		p := map[string]string{"id": fmt.Sprintf("proj-%d", len(s.projects)+1), "name": body["name"]}
		s.projects = append(s.projects, p)
		writeJSON(w, 201, map[string]any{"data": p})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleProjectsSub(w http.ResponseWriter, r *http.Request) {
	// /api/v1/projects/{id}/keys
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/")
	if len(parts) < 2 || parts[1] != "keys" {
		http.NotFound(w, r)
		return
	}
	pid := parts[0]
	if r.Method == http.MethodPost {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		defer s.mu.Unlock()
		k := map[string]any{
			"id": fmt.Sprintf("key-%d", len(s.keys)+1), "project_id": pid,
			"name": body["name"], "key_suffix": "wxyz", "status": "active",
		}
		s.keys = append(s.keys, k)
		writeJSON(w, 201, map[string]any{"data": k, "key": "ms-fakeapikey-1234"})
		return
	}
	if r.Method == http.MethodGet {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"data": s.keys})
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"data": s.wsList})
	case http.MethodPost:
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		defer s.mu.Unlock()
		ws := map[string]string{"id": fmt.Sprintf("ws-%d", len(s.wsList)+1), "name": body["name"]}
		s.wsList = append(s.wsList, ws)
		writeJSON(w, 201, map[string]any{"data": ws})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleWorkspacesSub(w http.ResponseWriter, r *http.Request) {
	// /api/workspaces/{wid}/api-keys
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/workspaces/"), "/")
	if len(parts) < 2 || parts[1] != "api-keys" {
		http.NotFound(w, r)
		return
	}
	wid := parts[0]
	if r.Method == http.MethodPost {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		defer s.mu.Unlock()
		k := map[string]any{
			"id": fmt.Sprintf("wskey-%d", len(s.wsKeys)+1), "workspace_id": wid,
			"name": body["name"], "key_suffix": "ab12",
		}
		s.wsKeys = append(s.wsKeys, k)
		writeJSON(w, 201, map[string]any{"data": k, "key": "ws-sk-fakekey-1234"})
		return
	}
	http.NotFound(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
