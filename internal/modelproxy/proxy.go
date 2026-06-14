// Package modelproxy exposes a local Modelserver-compatible proxy for Codex.
package modelproxy

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
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

	defaultResponsesInstructions = "You are a helpful coding assistant. Follow the user's instructions."

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
		if !validLocalRequestToken(r.Header, localBearerToken) {
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
		r2.Header.Del("X-Api-Key")
		r2.Header.Set("Authorization", "Bearer "+token)
		if err := normalizeResponsesInstructions(r2, http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes)); err != nil {
			if errors.Is(err, errRequestBodyTooLarge) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "request body unavailable", http.StatusBadRequest)
			return
		}
		r2.Header.Del("X-AgentServer-Client")
		proxy.ServeHTTP(w, r2)
	}), nil
}

var errRequestBodyTooLarge = errors.New("modelproxy: request body too large")

func normalizeResponsesInstructions(r *http.Request, body io.ReadCloser) error {
	r.Body = body
	if !shouldNormalizeResponsesInstructions(r) {
		return nil
	}
	raw, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errRequestBodyTooLarge
		}
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		setRequestBody(r, raw)
		return nil
	}

	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		setRequestBody(r, raw)
		return nil
	}
	if instructions, _ := root["instructions"].(string); strings.TrimSpace(instructions) == "" {
		var filtered any
		var changed bool
		instructions, filtered, changed = extractResponsesInstructions(root["input"])
		if instructions == "" {
			instructions = defaultResponsesInstructions
		} else if changed {
			root["input"] = filtered
		}
		root["instructions"] = instructions
	}
	if isOpenCodeRequest(r) {
		delete(root, "max_output_tokens")
	}
	rewritten, err := json.Marshal(root)
	if err != nil {
		setRequestBody(r, raw)
		return nil
	}
	setRequestBody(r, rewritten)
	return nil
}

func shouldNormalizeResponsesInstructions(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := strings.TrimRight(r.URL.Path, "/")
	if path != "/v1/responses" && path != "/responses" {
		return false
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
}

func isOpenCodeRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-AgentServer-Client")), "opencode")
}

func extractResponsesInstructions(input any) (string, any, bool) {
	messages, ok := input.([]any)
	if !ok {
		return "", input, false
	}
	var parts []string
	filtered := make([]any, 0, len(messages))
	changed := false
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		role, _ := message["role"].(string)
		switch strings.ToLower(strings.TrimSpace(role)) {
		case "developer", "system":
		default:
			filtered = append(filtered, item)
			continue
		}
		if text := strings.TrimSpace(extractTextContent(message["content"])); text != "" {
			parts = append(parts, text)
			changed = true
			continue
		}
		filtered = append(filtered, item)
	}
	return strings.Join(parts, "\n\n"), filtered, changed
}

func extractTextContent(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, item := range value {
			if text := strings.TrimSpace(extractContentPartText(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}

func extractContentPartText(part any) string {
	switch value := part.(type) {
	case string:
		return value
	case map[string]any:
		if text, _ := value["text"].(string); text != "" {
			return text
		}
		if text, _ := value["content"].(string); text != "" {
			return text
		}
		return ""
	default:
		return ""
	}
}

func setRequestBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
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

func validLocalRequestToken(h http.Header, token string) bool {
	if validLocalBearer(h.Get("Authorization"), token) {
		return true
	}
	apiKey := strings.TrimSpace(h.Get("X-Api-Key"))
	return apiKey != "" && subtle.ConstantTimeCompare([]byte(apiKey), []byte(token)) == 1
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
