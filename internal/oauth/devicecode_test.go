package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func fakeHydra(t *testing.T, tokenAfterPolls int32) *httptest.Server {
	t.Helper()
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth2/device/auth", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.FormValue("client_id") != "test-client" {
			t.Errorf("missing client_id form value")
		}
		json.NewEncoder(w).Encode(DeviceCodeChallenge{
			DeviceCode:              "dev-xyz",
			UserCode:                "ABCD-EFGH",
			VerificationURI:         "http://example/verify",
			VerificationURIComplete: "http://example/verify?u=ABCD-EFGH",
			ExpiresIn:               60,
			Interval:                1,
		})
	})
	mux.HandleFunc("/api/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&polls, 1)
		if n <= tokenAfterPolls {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		json.NewEncoder(w).Encode(Token{AccessToken: "AT", TokenType: "Bearer", ExpiresIn: 3600})
	})
	return httptest.NewServer(mux)
}

func TestRequestDeviceCode(t *testing.T) {
	srv := fakeHydra(t, 0)
	defer srv.Close()
	cfg := Config{Endpoint: srv.URL, AuthPath: "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token", ClientID: "test-client", Scope: "openid"}
	ch, err := RequestDeviceCode(context.Background(), cfg)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if ch.UserCode != "ABCD-EFGH" {
		t.Errorf("got user_code %q", ch.UserCode)
	}
	if ch.RetrievedAt.IsZero() {
		t.Errorf("RetrievedAt not set")
	}
}

func TestPollTokenSuccess(t *testing.T) {
	srv := fakeHydra(t, 2) // succeed on 3rd poll
	defer srv.Close()
	cfg := Config{Endpoint: srv.URL, AuthPath: "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token", ClientID: "test-client"}
	ch, err := RequestDeviceCode(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ch.Interval = 1 // ensure fast polling
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tok, err := PollToken(ctx, cfg, ch)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if tok.AccessToken != "AT" {
		t.Errorf("got %+v", tok)
	}
}

func TestPollTokenExpires(t *testing.T) {
	srv := fakeHydra(t, 100) // never succeeds
	defer srv.Close()
	cfg := Config{Endpoint: srv.URL, AuthPath: "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token", ClientID: "test-client"}
	ch, _ := RequestDeviceCode(context.Background(), cfg)
	ch.Interval = 1
	ch.ExpiresIn = 2 // expire fast
	ch.RetrievedAt = time.Now()
	_, err := PollToken(context.Background(), cfg, ch)
	if err == nil || err.Error() != "device code expired" {
		t.Errorf("want device code expired, got %v", err)
	}
}
