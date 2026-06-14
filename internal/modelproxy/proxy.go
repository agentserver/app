// Package modelproxy exposes a local Modelserver-compatible proxy for Codex.
package modelproxy

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

const (
	DefaultListenAddr      = "127.0.0.1:53452"
	DefaultBaseURL         = "http://127.0.0.1:53452/v1"
	DefaultUpstreamBaseURL = "https://code.ai.cs.ac.cn/v1"

	HealthPath = "/agentserver/model-proxy/health"

	MaxRequestBodyBytes = 32 << 20

	maxHeaderBytes    = 64 << 10
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 60 * time.Second
)

var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

type Options struct {
	Secrets          secrets.Store
	UpstreamBaseURL  string
	LocalBearerToken string
	Transport        http.RoundTripper
}

type ServerOptions struct {
	Addr             string
	Secrets          secrets.Store
	UpstreamBaseURL  string
	LocalBearerToken string
	Transport        http.RoundTripper
}

func NewHandler(opts Options) (http.Handler, error) {
	if opts.Secrets == nil {
		return nil, errors.New("modelproxy: secrets store required")
	}
	localBearerToken := strings.TrimSpace(opts.LocalBearerToken)
	if localBearerToken == "" {
		return nil, errors.New("modelproxy: local bearer token required")
	}
	upstreamRaw := opts.UpstreamBaseURL
	if upstreamRaw == "" {
		upstreamRaw = DefaultUpstreamBaseURL
	}
	upstream, err := url.Parse(upstreamRaw)
	if err != nil {
		return nil, fmt.Errorf("modelproxy: parse upstream base URL: %w", err)
	}
	if upstream.Scheme == "" || upstream.Host == "" {
		return nil, fmt.Errorf("modelproxy: upstream base URL must include scheme and host")
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.Host = upstream.Host
		},
	}
	if opts.Transport != nil {
		proxy.Transport = opts.Transport
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == HealthPath {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !validLocalBearer(r.Header.Get("Authorization"), localBearerToken) {
			http.Error(w, "local model proxy authorization required", http.StatusUnauthorized)
			return
		}
		if r.ContentLength > MaxRequestBodyBytes {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		token, err := opts.Secrets.Get(tokenrefresh.AccessTokenKey)
		if err != nil || token == "" {
			http.Error(w, "modelserver login required", http.StatusUnauthorized)
			return
		}
		r2 := r.Clone(r.Context())
		r2.Header = r.Header.Clone()
		stripHopByHopHeaders(r2.Header)
		r2.Header.Set("Authorization", "Bearer "+token)
		r2.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes)
		proxy.ServeHTTP(w, r2)
	}), nil
}

func stripHopByHopHeaders(h http.Header) {
	for _, value := range h.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				h.Del(name)
			}
		}
	}
	for _, name := range hopByHopHeaders {
		h.Del(name)
	}
}

func validLocalBearer(auth, token string) bool {
	parts := strings.Fields(auth)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) == 1
}

func ListenAndServe(ctx context.Context, opts ServerOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	addr := opts.Addr
	if addr == "" {
		addr = DefaultListenAddr
	}
	handler, err := NewHandler(Options{
		Secrets:          opts.Secrets,
		UpstreamBaseURL:  opts.UpstreamBaseURL,
		LocalBearerToken: opts.LocalBearerToken,
		Transport:        opts.Transport,
	})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
