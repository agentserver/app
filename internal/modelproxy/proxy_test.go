package modelproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/codex"
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
		Secrets:         sec,
		UpstreamBaseURL: upstream.URL + "/v1",
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
	req.Header.Set("Authorization", "Bearer "+codex.LocalProxyAPIKeyValue)
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
	req.Header.Set("Authorization", "Bearer "+codex.LocalProxyAPIKeyValue)
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
		Secrets:         sec,
		UpstreamBaseURL: upstream.URL + "/v1",
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
		{name: "wrong scheme", auth: "Basic " + codex.LocalProxyAPIKeyValue},
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
		Secrets:         newTestSecrets(),
		UpstreamBaseURL: "https://upstream.test/v1",
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

func TestProxyReturnsUnauthorizedWhenAccessTokenMissing(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	}))
	defer upstream.Close()

	handler, err := NewHandler(Options{
		Secrets:         newTestSecrets(),
		UpstreamBaseURL: upstream.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+codex.LocalProxyAPIKeyValue)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if upstreamCalled {
		t.Fatal("upstream should not be called without an access token")
	}
}
