package oauth

import (
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// ErrAllPortsBusy is returned by ReservePort when none of cfg.Ports could be bound.
var ErrAllPortsBusy = errors.New("all configured callback ports are busy")

// ReservePort tries cfg.Ports in order, returns the first that net.Listen accepts.
// The returned listener is held for StartListening; caller MUST hand it off or .Close() it.
func ReservePort(cfg AuthCodeConfig) (port int, ln net.Listener, err error) {
	for _, p := range cfg.Ports {
		l, lerr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if lerr == nil {
			return p, l, nil
		}
	}
	return 0, nil, ErrAllPortsBusy
}

//go:embed templates/success.html templates/denied.html templates/state_mismatch.html templates/missing_code.html
var callbackTemplates embed.FS

// callbackPage returns the embedded HTML for the named outcome.
// Panics on unknown name (programmer error).
func callbackPage(name string) []byte {
	b, err := callbackTemplates.ReadFile("templates/" + name + ".html")
	if err != nil {
		panic("oauth: missing embedded template " + name + ": " + err.Error())
	}
	return b
}

// CallbackResult is what arrives at the redirect_uri.
// Sent on the channel returned by StartListening.
type CallbackResult struct {
	Code  string
	State string
	Error string // OAuth error code if present (e.g. "access_denied")
}

// StartListening serves cfg.CallbackPath on ln. The handler:
//   - On valid code+state match → send {Code, State} on channel, serve success.html
//   - On error= → send {Error} on channel, serve denied.html
//   - On state mismatch → DO NOT send, serve state_mismatch.html (caller times out)
//   - On missing code & no error → DO NOT send, serve missing_code.html
//
// The channel receives at most one value. It is closed either when shutdown() is
// called or when cfg.LoginTimeout (default 10m) elapses.
// Caller MUST call shutdown(); idempotent.
func StartListening(ctx context.Context, ln net.Listener, cfg AuthCodeConfig, expectedState string) (
	ch <-chan CallbackResult, shutdown func(),
) {
	timeout := cfg.LoginTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)

	out := make(chan CallbackResult, 1)
	srv := &http.Server{}

	var once sync.Once
	sendOnce := func(r CallbackResult) {
		select {
		case out <- r:
		default:
		}
	}
	closeOnce := func() { once.Do(func() { close(out) }) }

	mux := http.NewServeMux()
	mux.HandleFunc(cfg.CallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(callbackPage("denied"))
			sendOnce(CallbackResult{Error: e})
			return
		}
		state := q.Get("state")
		if !stateMatches(state, expectedState) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(callbackPage("state_mismatch"))
			return // no send → caller will time out
		}
		code := q.Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(callbackPage("missing_code"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(callbackPage("success"))
		sendOnce(CallbackResult{Code: code, State: state})
	})
	srv.Handler = mux

	go func() { _ = srv.Serve(ln) }()

	// Watcher: close channel + shut server when ctx expires.
	go func() {
		<-ctx.Done()
		// srv.Shutdown blocks until all in-flight ServeHTTP calls return,
		// so handler's sendOnce has finished before we run closeOnce below.
		// Without that ordering, sendOnce's `select { case out <- r: default: }`
		// would panic if `out` were closed (default does NOT protect a closed channel).
		_ = srv.Shutdown(context.Background())
		closeOnce()
	}()

	shutdown = func() {
		cancel()
		// srv.Shutdown blocks until all in-flight ServeHTTP calls return,
		// so handler's sendOnce has finished before we run closeOnce below.
		// Without that ordering, sendOnce's `select { case out <- r: default: }`
		// would panic if `out` were closed (default does NOT protect a closed channel).
		_ = srv.Shutdown(context.Background())
		closeOnce()
	}
	return out, shutdown
}

func stateMatches(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
