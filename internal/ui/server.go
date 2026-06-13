package ui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/updater"
)

//go:embed all:assets/dist
var assetsFS embed.FS

// NewServer constructs the onboarding HTTP handler. Browser-opening for
// OAuth flows is done by the orchestrator (see LoginModelserver /
// LoginAgentserver in orchestrator_real.go), not the server, so there's
// no openBrowser parameter here.
func NewServer(o Orchestrator) http.Handler {
	return NewServerWithConsole(o, noopConsoleController{})
}

func NewServerWithConsole(o Orchestrator, c ConsoleController) http.Handler {
	s := &server{o: o, c: c, sse: newSSEHub()}
	mux := http.NewServeMux()
	// Static
	staticFS, _ := fs.Sub(assetsFS, "assets/dist")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	// JSON
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/step/modelserver_login", s.handleMSLogin)
	mux.HandleFunc("/api/step/modelserver_login/status", s.handleMSStatus)
	mux.HandleFunc("/api/step/agentserver_login", s.handleASLogin)
	mux.HandleFunc("/api/step/agentserver_login/status", s.handleASStatus)
	mux.HandleFunc("/api/step/frontend_install", s.handleFrontendInstall)
	mux.HandleFunc("/api/step/frontend_configure", s.handleFrontendConfigure)
	mux.HandleFunc("/api/step/vscode_install", s.handleFrontendInstall)
	mux.HandleFunc("/api/step/vscode_configure", s.handleFrontendConfigure)
	mux.HandleFunc("/api/finalize", s.handleFinalize)
	mux.HandleFunc("/api/abort", s.handleAbort)
	mux.HandleFunc("/api/launch", s.handleLaunch)
	mux.HandleFunc("/api/launch-vscode", s.handleLaunch)

	mux.HandleFunc("/api/console/health", s.handleConsoleHealth)
	mux.HandleFunc("/api/console/state", s.handleConsoleState)
	mux.HandleFunc("/api/console/refresh", s.handleConsoleRefresh)
	mux.HandleFunc("/api/console/update", s.handleConsoleUpdate)
	mux.HandleFunc("/api/console/update/check", s.handleConsoleUpdateCheck)
	mux.HandleFunc("/api/console/update/install", s.handleConsoleUpdateInstall)
	mux.HandleFunc("/api/console/open-frontend", s.handleConsoleOpenFrontend)
	mux.HandleFunc("/api/console/open-subscription", s.handleConsoleOpenSubscription)
	mux.HandleFunc("/api/console/logout-modelserver", s.handleConsoleLogoutModelserver)
	mux.HandleFunc("/api/console/quit", s.handleConsoleQuit)
	mux.HandleFunc("/api/console/select-folder", s.handleConsoleSelectFolder)
	mux.HandleFunc("/api/console/slaves", s.handleConsoleSlaves)
	mux.HandleFunc("/api/console/slaves/", s.handleConsoleSlave)

	// SSE
	mux.HandleFunc("/api/events", s.sse.handle)
	return mux
}

type server struct {
	o   Orchestrator
	c   ConsoleController
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

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	return false
}

func requireMethods(w http.ResponseWriter, r *http.Request, allow string, methods ...string) bool {
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", allow)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	return false
}

func requireTrustedConsoleMutation(w http.ResponseWriter, r *http.Request) bool {
	if trustedConsoleMutationRequest(r) {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
	return false
}

func requirePostTrustedMutation(w http.ResponseWriter, r *http.Request) bool {
	if !requireMethod(w, r, http.MethodPost) {
		return false
	}
	return requireTrustedConsoleMutation(w, r)
}

func trustedConsoleMutationRequest(r *http.Request) bool {
	fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	switch fetchSite {
	case "", "same-origin", "same-site", "none":
	case "cross-site":
		return false
	default:
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !sameRequestOrigin(r, origin) {
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Referer")); origin != "" && !sameRequestOrigin(r, origin) {
		return false
	}
	return true
}

func sameRequestOrigin(r *http.Request, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, requestScheme(r)) && strings.EqualFold(u.Host, r.Host)
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func writeConsoleErr(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

func writeConsoleCreateErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, slave.ErrSlaveConflict):
		writeErr(w, http.StatusConflict, err)
	case errors.Is(err, slave.ErrInvalidCreateInput):
		writeErr(w, http.StatusBadRequest, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
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
	if !requirePostTrustedMutation(w, r) {
		return
	}
	oauthURL, err := s.o.LoginModelserver(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "started", "oauth_url": oauthURL})
}

func (s *server) handleMSStatus(w http.ResponseWriter, r *http.Request) {
	if !requirePostTrustedMutation(w, r) {
		return
	}
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
	if !requirePostTrustedMutation(w, r) {
		return
	}
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
	if !requirePostTrustedMutation(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	key, err := s.o.PollAgentserverLogin(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]string{"state": "waiting", "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"state": "success", "key_suffix": key.KeySuffix})
}

func (s *server) handleFrontendInstall(w http.ResponseWriter, r *http.Request) {
	if !requirePostTrustedMutation(w, r) {
		return
	}
	streamID := s.sse.newStream()
	go func() {
		defer s.sse.close(streamID)
		progress := make(chan ProgressEvent, 128)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for ev := range progress {
				_ = s.sse.send(streamID, ev)
			}
		}()
		if err := s.o.EnsureFrontend(context.Background(), progress); err != nil {
			s.sse.send(streamID, ProgressEvent{Stage: "error", Msg: err.Error()})
		}
		close(progress)
		<-done
	}()
	writeJSON(w, 200, map[string]string{"stream_id": streamID})
}

func (s *server) handleFrontendConfigure(w http.ResponseWriter, r *http.Request) {
	if !requirePostTrustedMutation(w, r) {
		return
	}
	if err := s.o.ConfigureFrontend(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "success"})
}

func (s *server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	if !requirePostTrustedMutation(w, r) {
		return
	}
	if err := s.o.Finalize(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "complete"})
}

func (s *server) handleAbort(w http.ResponseWriter, r *http.Request) {
	if !requirePostTrustedMutation(w, r) {
		return
	}
	_ = s.o.Abort(r.Context())
	writeJSON(w, 200, map[string]string{"state": "aborted"})
}

func (s *server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	if !requirePostTrustedMutation(w, r) {
		return
	}
	if err := s.o.LaunchAndShutdown(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "launching"})
}

func (s *server) handleConsoleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.c.Healthy(r.Context()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"state": "unavailable"})
		return
	}
	writeJSON(w, 200, map[string]string{"state": "ok"})
}

func (s *server) handleConsoleState(w http.ResponseWriter, r *http.Request) {
	st, err := s.c.State(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *server) handleConsoleRefresh(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !requireTrustedConsoleMutation(w, r) {
		return
	}
	st, err := s.c.Refresh(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *server) handleConsoleUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	st, err := s.c.UpdateState(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *server) handleConsoleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !requirePostTrustedMutation(w, r) {
		return
	}
	st, err := s.c.CheckUpdate(r.Context(), false)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *server) handleConsoleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if !requirePostTrustedMutation(w, r) {
		return
	}
	confirmed, err := s.c.UpdateState(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if !stateHasInstallableCachedUpdate(confirmed) {
		writeErr(w, http.StatusConflict, errors.New("console: no update available"))
		return
	}
	manifest := updater.Manifest{
		Version: confirmed.Update.Version,
		URL:     confirmed.Update.URL,
		SHA256:  confirmed.Update.SHA256,
		Size:    confirmed.Update.Size,
		Notes:   confirmed.Update.Notes,
	}
	if err := manifest.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	fresh, err := s.c.CheckUpdate(r.Context(), false)
	if err == nil {
		if fresh.Status != updater.StatusAvailable || fresh.Update == nil {
			writeErr(w, http.StatusConflict, errors.New("console: update is no longer available"))
			return
		}
		if !sameAvailableUpdate(*confirmed.Update, *fresh.Update) {
			writeErr(w, http.StatusConflict, errors.New("console: update changed; check again before installing"))
			return
		}
	}
	st, err := s.c.InstallUpdate(r.Context(), manifest)
	if err != nil {
		if errors.Is(err, console.ErrUpdateInstallInProgress) {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

func stateHasInstallableCachedUpdate(state updater.State) bool {
	if state.Update == nil {
		return false
	}
	return state.Status == updater.StatusAvailable || state.Status == updater.StatusError
}

func sameAvailableUpdate(a, b updater.AvailableUpdate) bool {
	return a.Version == b.Version &&
		a.URL == b.URL &&
		a.SHA256 == b.SHA256 &&
		a.Size == b.Size &&
		a.Notes == b.Notes
}

func (s *server) handleConsoleSlaves(w http.ResponseWriter, r *http.Request) {
	if !requireMethods(w, r, "GET, POST", http.MethodGet, http.MethodPost) {
		return
	}
	if r.Method == http.MethodGet {
		machine, slaves, err := s.c.Slaves(r.Context())
		if err != nil {
			writeConsoleErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"machine": machine, "slaves": slaves})
		return
	}
	if !requireTrustedConsoleMutation(w, r) {
		return
	}

	var in slave.CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sl, err := s.c.CreateSlave(r.Context(), in)
	if err != nil {
		writeConsoleCreateErr(w, err)
		return
	}
	writeJSON(w, 200, sl)
}

func (s *server) handleConsoleSelectFolder(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !requireTrustedConsoleMutation(w, r) {
		return
	}
	folder, err := s.c.SelectFolder(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"folder": folder})
}

func (s *server) handleConsoleSlave(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/console/slaves/"
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(rest, "/")
	if rest == "" || parts[0] == "" || len(parts) > 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if !requireMethod(w, r, http.MethodDelete) {
			return
		}
		if !requireTrustedConsoleMutation(w, r) {
			return
		}
		if err := s.c.DeleteSlave(r.Context(), id); err != nil {
			writeConsoleErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"state": "deleted"})
		return
	}

	action := parts[1]
	if action == "" {
		http.NotFound(w, r)
		return
	}
	if action != "restart" && action != "pause" && action != "open-remote" {
		http.NotFound(w, r)
		return
	}
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !requireTrustedConsoleMutation(w, r) {
		return
	}
	if action == "open-remote" {
		result, err := s.c.OpenSlaveRemote(r.Context(), id)
		if err != nil {
			writeConsoleErr(w, err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	var (
		sl  slave.Slave
		err error
	)
	if action == "restart" {
		sl, err = s.c.RestartSlave(r.Context(), id)
	} else {
		sl, err = s.c.PauseSlave(r.Context(), id)
	}
	if err != nil {
		writeConsoleErr(w, err)
		return
	}
	writeJSON(w, 200, sl)
}

func (s *server) handleConsoleOpenFrontend(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !requireTrustedConsoleMutation(w, r) {
		return
	}
	if err := s.c.OpenFrontend(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "opened"})
}

func (s *server) handleConsoleOpenSubscription(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !requireTrustedConsoleMutation(w, r) {
		return
	}
	if err := s.c.OpenSubscription(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "opened"})
}

func (s *server) handleConsoleLogoutModelserver(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !requireTrustedConsoleMutation(w, r) {
		return
	}
	if err := s.c.LogoutModelserver(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "logged_out"})
}

func (s *server) handleConsoleQuit(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !requireTrustedConsoleMutation(w, r) {
		return
	}
	if err := s.c.Quit(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "quitting"})
}

// ----------- SSE hub -----------

type sseHub struct {
	mu      sync.Mutex
	streams map[string]*sseStream
}

type sseStream struct {
	ch     chan ProgressEvent
	closed bool
}

func newSSEHub() *sseHub {
	return &sseHub{streams: map[string]*sseStream{}}
}

func (h *sseHub) newStream() string {
	id := randomStreamID()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.streams[id] = &sseStream{ch: make(chan ProgressEvent, 128)}
	return id
}

func randomStreamID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return time.Now().Format("20060102-150405.000000000")
}

func (h *sseHub) channel(id string) chan<- ProgressEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	if st := h.streams[id]; st != nil {
		return st.ch
	}
	return nil
}

func (h *sseHub) send(id string, ev ProgressEvent) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.streams[id]
	if st == nil || st.closed {
		return false
	}
	select {
	case st.ch <- ev:
	default:
	}
	return true
}

func (h *sseHub) close(id string) {
	h.mu.Lock()
	st := h.streams[id]
	if st != nil && !st.closed {
		st.closed = true
		close(st.ch)
		time.AfterFunc(5*time.Minute, func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.streams[id] == st {
				delete(h.streams, id)
			}
		})
	}
	h.mu.Unlock()
}

func (h *sseHub) handle(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("stream")
	h.mu.Lock()
	st := h.streams[id]
	h.mu.Unlock()
	if st == nil {
		http.Error(w, "no such stream", 404)
		return
	}
	ch := st.ch
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				h.deleteStream(id, ch)
				return
			}
			w.Write([]byte("data: "))
			enc.Encode(ev)
			w.Write([]byte("\n"))
			if flusher != nil {
				flusher.Flush()
			}
		case <-r.Context().Done():
			h.deleteStream(id, ch)
			return
		}
	}
}

func (h *sseHub) deleteStream(id string, ch chan ProgressEvent) {
	h.mu.Lock()
	if st := h.streams[id]; st != nil && st.ch == ch {
		delete(h.streams, id)
	}
	h.mu.Unlock()
}
