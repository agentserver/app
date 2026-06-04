package ui

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"sync"
	"time"
)

//go:embed assets/*
var assetsFS embed.FS

// NewServer constructs the onboarding HTTP handler. Browser-opening for
// OAuth flows is done by the orchestrator (see LoginModelserver /
// LoginAgentserver in orchestrator_real.go), not the server, so there's
// no openBrowser parameter here.
func NewServer(o Orchestrator) http.Handler {
	s := &server{o: o, sse: newSSEHub()}
	mux := http.NewServeMux()
	// Static
	staticFS, _ := fs.Sub(assetsFS, "assets")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	// JSON
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/step/modelserver_login", s.handleMSLogin)
	mux.HandleFunc("/api/step/modelserver_login/status", s.handleMSStatus)
	mux.HandleFunc("/api/step/agentserver_login", s.handleASLogin)
	mux.HandleFunc("/api/step/agentserver_login/status", s.handleASStatus)
	mux.HandleFunc("/api/step/vscode_install", s.handleVSCodeInstall)
	mux.HandleFunc("/api/step/vscode_configure", s.handleVSCodeConfigure)
	mux.HandleFunc("/api/finalize", s.handleFinalize)
	mux.HandleFunc("/api/abort", s.handleAbort)
	mux.HandleFunc("/api/launch-vscode", s.handleLaunchVSCode)

	// SSE
	mux.HandleFunc("/api/events", s.sse.handle)
	return mux
}

type server struct {
	o   Orchestrator
	sse *sseHub
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	st, err := s.o.State(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *server) handleMSLogin(w http.ResponseWriter, r *http.Request) {
	oauthURL, err := s.o.LoginModelserver(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "started", "oauth_url": oauthURL})
}

func (s *server) handleMSStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	key, err := s.o.PollModelserverLogin(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]string{"state": "waiting", "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"state": "success", "key_suffix": key.KeySuffix})
}

func (s *server) handleASLogin(w http.ResponseWriter, r *http.Request) {
	oauthURL, err := s.o.LoginAgentserver(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// Note: browser opening is now the orchestrator's responsibility
	// (LoginAgentserver does `go r.d.OpenBrowser(url)`). The server no
	// longer opens it here. This unifies behavior with handleMSLogin.
	writeJSON(w, 200, map[string]string{"state": "started", "oauth_url": oauthURL})
}

func (s *server) handleASStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	key, err := s.o.PollAgentserverLogin(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]string{"state": "waiting", "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"state": "success", "key_suffix": key.KeySuffix})
}

func (s *server) handleVSCodeInstall(w http.ResponseWriter, r *http.Request) {
	streamID := s.sse.newStream()
	go func() {
		defer s.sse.close(streamID)
		ch := s.sse.channel(streamID)
		s.o.EnsureVSCode(context.Background(), ch)
	}()
	writeJSON(w, 200, map[string]string{"stream_id": streamID})
}

func (s *server) handleVSCodeConfigure(w http.ResponseWriter, r *http.Request) {
	if err := s.o.ConfigureVSCode(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "success"})
}

func (s *server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	if err := s.o.Finalize(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "complete"})
}

func (s *server) handleAbort(w http.ResponseWriter, r *http.Request) {
	_ = s.o.Abort(r.Context())
	writeJSON(w, 200, map[string]string{"state": "aborted"})
}

func (s *server) handleLaunchVSCode(w http.ResponseWriter, r *http.Request) {
	if err := s.o.LaunchAndShutdown(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "launching"})
}

// ----------- SSE hub -----------

type sseHub struct {
	mu      sync.Mutex
	streams map[string]chan ProgressEvent
}

func newSSEHub() *sseHub {
	return &sseHub{streams: map[string]chan ProgressEvent{}}
}

func (h *sseHub) newStream() string {
	id := time.Now().Format("20060102-150405.000000000")
	h.mu.Lock()
	defer h.mu.Unlock()
	h.streams[id] = make(chan ProgressEvent, 128)
	return id
}

func (h *sseHub) channel(id string) chan<- ProgressEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.streams[id]
}

func (h *sseHub) close(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.streams[id]; ok {
		close(ch)
		delete(h.streams, id)
	}
}

func (h *sseHub) handle(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("stream")
	h.mu.Lock()
	ch, ok := h.streams[id]
	h.mu.Unlock()
	if !ok {
		http.Error(w, "no such stream", 404)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	for ev := range ch {
		w.Write([]byte("data: "))
		enc.Encode(ev)
		w.Write([]byte("\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
}
