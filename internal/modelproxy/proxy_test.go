package modelproxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

type testSecrets struct {
	values map[string]string
}

func newTestSecrets() *testSecrets {
	return &testSecrets{values: map[string]string{}}
}

func (s *testSecrets) Get(key string) (string, error) {
	v, ok := s.values[key]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}

func (s *testSecrets) Set(key, value string) error {
	s.values[key] = value
	return nil
}

func (s *testSecrets) Delete(key string) error {
	delete(s.values, key)
	return nil
}

func TestProxyLoadsLatestAccessTokenForEveryRequest(t *testing.T) {
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access-1"); err != nil {
		t.Fatal(err)
	}

	var gotAuth []string
	var gotPath []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		gotPath = append(gotPath, r.URL.String())
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.Copy(w, r.Body)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodPost, proxy.URL+"/v1/responses?trace=1", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer random-local-token")
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
	if got := resp.Header.Get("X-Upstream"); got != "ok" {
		t.Fatalf("X-Upstream = %q, want ok", got)
	}

	if err := sec.Set(tokenrefresh.AccessTokenKey, "access-2"); err != nil {
		t.Fatal(err)
	}
	req, err = http.NewRequest(http.MethodGet, proxy.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer random-local-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	_ = resp.Body.Close()

	wantAuth := []string{"Bearer access-1", "Bearer access-2"}
	if len(gotAuth) != len(wantAuth) {
		t.Fatalf("upstream auth calls = %v, want %v", gotAuth, wantAuth)
	}
	for i := range wantAuth {
		if gotAuth[i] != wantAuth[i] {
			t.Fatalf("auth[%d] = %q, want %q", i, gotAuth[i], wantAuth[i])
		}
	}
	wantPath := []string{"/v1/responses?trace=1", "/v1/models"}
	for i := range wantPath {
		if gotPath[i] != wantPath[i] {
			t.Fatalf("path[%d] = %q, want %q", i, gotPath[i], wantPath[i])
		}
	}
}

func TestProxyAddsResponsesInstructionsFromInputMessages(t *testing.T) {
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access"); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{
		"model": "gpt-5.5",
		"input": [
			{"role": "developer", "content": [{"type": "input_text", "text": "Answer in the user's language."}]},
			{"role": "system", "content": "Keep answers concise."},
			{"role": "user", "content": [{"type": "input_text", "text": "你好"}]}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer random-local-token")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	if got["instructions"] != "Answer in the user's language.\n\nKeep answers concise." {
		t.Fatalf("instructions=%q", got["instructions"])
	}
	input, ok := got["input"].([]any)
	if !ok {
		t.Fatalf("input=%#v", got["input"])
	}
	if len(input) != 1 {
		t.Fatalf("input should only contain user messages after system/developer promotion: %#v", got["input"])
	}
	user, _ := input[0].(map[string]any)
	if user["role"] != "user" {
		t.Fatalf("remaining input role=%v", user["role"])
	}
}

func TestProxyAddsDefaultResponsesInstructionsWhenMissing(t *testing.T) {
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access"); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"model":"gpt-5.5","input":[{"role":"user","content":[{"type":"input_text","text":"你是谁？"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer random-local-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	want := "You are a helpful coding assistant. Follow the user's instructions."
	if got["instructions"] != want {
		t.Fatalf("instructions=%q, want %q", got["instructions"], want)
	}
}

func TestProxyPreservesResponsesMaxOutputTokensByDefault(t *testing.T) {
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access"); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"model":"gpt-5.5","max_output_tokens":4096,"input":[{"role":"user","content":"你好"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer random-local-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	if got["max_output_tokens"] != float64(4096) {
		t.Fatalf("max_output_tokens = %#v, want preserved", got["max_output_tokens"])
	}
	if got["model"] != "gpt-5.5" {
		t.Fatalf("model = %v", got["model"])
	}
	if got["instructions"] == "" {
		t.Fatalf("instructions should still be normalized: %#v", got)
	}
}

func TestProxyRemovesUnsupportedResponsesMaxOutputTokensForOpenCode(t *testing.T) {
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access"); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		if r.Header.Get("X-AgentServer-Client") != "" {
			t.Fatalf("X-AgentServer-Client should not be forwarded upstream")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"model":"gpt-5.5","max_output_tokens":4096,"input":[{"role":"user","content":"你好"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer random-local-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentServer-Client", "opencode")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	if _, ok := got["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens should be removed before upstream: %#v", got)
	}
}

func TestProxyPreservesExistingResponsesInstructions(t *testing.T) {
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access"); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{
		"model": "gpt-5.5",
		"instructions": "Already present.",
		"input": [{"role": "developer", "content": "Do not use this."}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer random-local-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	if got["instructions"] != "Already present." {
		t.Fatalf("instructions=%q, want existing value", got["instructions"])
	}
}

func TestProxyLeavesNonResponsesJSONBodyUnchanged(t *testing.T) {
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access"); err != nil {
		t.Fatal(err)
	}

	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		got = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := `{"model":"gpt-5.5","messages":[{"role":"user","content":"你好"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer random-local-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	if got != body {
		t.Fatalf("body changed:\ngot  %s\nwant %s", got, body)
	}
}

func TestProxyRequiresLocalBearerToken(t *testing.T) {
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access"); err != nil {
		t.Fatal(err)
	}
	var upstreamCalled bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	for _, tt := range []struct {
		name string
		auth string
	}{
		{name: "missing"},
		{name: "wrong token", auth: "Bearer wrong"},
		{name: "wrong scheme", auth: "Basic random-local-token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstreamCalled = false
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if upstreamCalled {
				t.Fatal("upstream should not be called without valid local bearer")
			}
		})
	}
}

func TestProxyHealthDoesNotRequireLocalBearerToken(t *testing.T) {
	handler, err := NewHandler(Options{
		Secrets:          newTestSecrets(),
		UpstreamBaseURL:  "https://upstream.test/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, HealthPath, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestProxyRejectsOversizedRequestBody(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	}))
	defer upstream.Close()

	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access"); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(make([]byte, MaxRequestBodyBytes+1)))
	req.Header.Set("Authorization", "Bearer random-local-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if upstreamCalled {
		t.Fatal("upstream should not receive oversized request")
	}
}

func TestProxyStripsHopByHopAndProxyAuthorizationHeaders(t *testing.T) {
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access"); err != nil {
		t.Fatal(err)
	}

	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer random-local-token")
	req.Header.Set("Connection", "keep-alive, X-Drop-Me")
	req.Header.Set("X-Drop-Me", "drop")
	req.Header.Set("Proxy-Authorization", "Basic secret")
	req.Header.Set("TE", "trailers")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	for _, name := range []string{"Connection", "X-Drop-Me", "Proxy-Authorization", "TE"} {
		if got.Get(name) != "" {
			t.Fatalf("upstream header %s=%q, want stripped", name, got.Get(name))
		}
	}
	if got.Get("Authorization") != "Bearer access" {
		t.Fatalf("upstream Authorization=%q, want modelserver access token", got.Get("Authorization"))
	}
}

func TestProxyAcceptsLocalTokenFromAnthropicAPIKeyHeader(t *testing.T) {
	sec := newTestSecrets()
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access"); err != nil {
		t.Fatal(err)
	}

	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          sec,
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"glm-5.1","messages":[]}`))
	req.Header.Set("X-Api-Key", "random-local-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	if got.Get("Authorization") != "Bearer access" {
		t.Fatalf("upstream Authorization=%q, want modelserver access token", got.Get("Authorization"))
	}
	if got.Get("X-Api-Key") != "" {
		t.Fatalf("upstream X-Api-Key=%q, want stripped", got.Get("X-Api-Key"))
	}
}

func TestProxyReturnsUnauthorizedWhenAccessTokenMissing(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:          newTestSecrets(),
		UpstreamBaseURL:  upstream.URL + "/v1",
		LocalBearerToken: "random-local-token",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer random-local-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if upstreamCalled {
		t.Fatal("upstream should not be called without an access token")
	}
}

func TestProxyRequiresConfiguredLocalBearerToken(t *testing.T) {
	_, err := NewHandler(Options{
		Secrets:         newTestSecrets(),
		UpstreamBaseURL: "https://upstream.test/v1",
	})
	if err == nil {
		t.Fatal("NewHandler returned nil error without LocalBearerToken")
	}
}
