package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestStartPKCE_S256(t *testing.T) {
	cfg := AuthCodeConfig{
		Endpoint:  "https://codeapi.cs.ac.cn",
		AuthPath:  "/oauth2/auth",
		TokenPath: "/oauth2/token",
		ClientID:  "5321f7e6-3d79-4ac9-a742-04809dbf9025",
		Scope:     "project:inference offline_access",
	}
	sess, err := StartPKCE(cfg, "http://127.0.0.1:53428/oauth/modelserver/callback")
	if err != nil {
		t.Fatalf("StartPKCE: %v", err)
	}
	if l := len(sess.Verifier); l < 43 || l > 128 {
		t.Errorf("Verifier length %d not in [43,128]", l)
	}
	sum := sha256.Sum256([]byte(sess.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if sess.Challenge != want {
		t.Errorf("Challenge = %q, want %q", sess.Challenge, want)
	}
	if len(sess.State) < 16 {
		t.Errorf("State too short: %d", len(sess.State))
	}
	if sess.RedirectURI != "http://127.0.0.1:53428/oauth/modelserver/callback" {
		t.Errorf("RedirectURI = %q", sess.RedirectURI)
	}
}

func TestStartPKCE_AuthURL(t *testing.T) {
	cfg := AuthCodeConfig{
		Endpoint: "https://codeapi.cs.ac.cn",
		AuthPath: "/oauth2/auth",
		ClientID: "5321f7e6-3d79-4ac9-a742-04809dbf9025",
		Scope:    "project:inference offline_access",
	}
	sess, err := StartPKCE(cfg, "http://127.0.0.1:53428/oauth/modelserver/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(sess.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "codeapi.cs.ac.cn" || u.Path != "/oauth2/auth" {
		t.Errorf("AuthURL base wrong: %q", sess.AuthURL)
	}
	q := u.Query()
	for _, k := range []string{"response_type", "client_id", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method"} {
		if q.Get(k) == "" {
			t.Errorf("AuthURL missing %s", k)
		}
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") != sess.Challenge {
		t.Errorf("code_challenge != session.Challenge")
	}
	if q.Get("state") != sess.State {
		t.Errorf("state != session.State")
	}
	if q.Get("redirect_uri") != sess.RedirectURI {
		t.Errorf("redirect_uri = %q, want %q", q.Get("redirect_uri"), sess.RedirectURI)
	}
	if !strings.Contains(q.Get("scope"), "project:inference") {
		t.Errorf("scope = %q", q.Get("scope"))
	}
}

func TestFinishPKCE_Success(t *testing.T) {
	var gotBody url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		_ = r.ParseForm()
		gotBody = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok-xyz","token_type":"Bearer","refresh_token":"rtok","expires_in":3600}`))
	}))
	defer srv.Close()

	cfg := AuthCodeConfig{
		Endpoint:  srv.URL,
		TokenPath: "/oauth2/token",
		ClientID:  "client-x",
	}
	sess := &PKCESession{
		Verifier:    "verifier-xyz",
		RedirectURI: "http://127.0.0.1:53428/oauth/modelserver/callback",
	}
	tok, err := FinishPKCE(context.Background(), cfg, sess, "code-abc")
	if err != nil {
		t.Fatalf("FinishPKCE: %v", err)
	}
	if tok.AccessToken != "tok-xyz" || tok.RefreshToken != "rtok" || tok.TokenType != "Bearer" || tok.ExpiresIn != 3600 {
		t.Errorf("token = %+v", tok)
	}
	if gotBody.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", gotBody.Get("grant_type"))
	}
	if gotBody.Get("code") != "code-abc" {
		t.Errorf("code = %q", gotBody.Get("code"))
	}
	if gotBody.Get("code_verifier") != "verifier-xyz" {
		t.Errorf("code_verifier = %q", gotBody.Get("code_verifier"))
	}
	if gotBody.Get("client_id") != "client-x" {
		t.Errorf("client_id = %q", gotBody.Get("client_id"))
	}
	if gotBody.Get("redirect_uri") != "http://127.0.0.1:53428/oauth/modelserver/callback" {
		t.Errorf("redirect_uri = %q", gotBody.Get("redirect_uri"))
	}
}

func TestFinishPKCE_InvalidGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"code used or expired"}`))
	}))
	defer srv.Close()

	cfg := AuthCodeConfig{Endpoint: srv.URL, TokenPath: "/oauth2/token", ClientID: "x"}
	sess := &PKCESession{Verifier: "v", RedirectURI: "http://127.0.0.1:1/cb"}

	_, err := FinishPKCE(context.Background(), cfg, sess, "stale-code")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %q, want to contain 'invalid_grant'", err.Error())
	}
}

func TestRefreshToken_Success(t *testing.T) {
	var gotBody url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		_ = r.ParseForm()
		gotBody = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok-refreshed","token_type":"Bearer","refresh_token":"rtok-2","expires_in":7200}`))
	}))
	defer srv.Close()

	cfg := AuthCodeConfig{
		Endpoint:  srv.URL,
		TokenPath: "/oauth2/token",
		ClientID:  "client-refresh",
	}
	tok, err := RefreshToken(context.Background(), cfg, "rtok-1")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if tok.AccessToken != "tok-refreshed" || tok.RefreshToken != "rtok-2" || tok.ExpiresIn != 7200 {
		t.Errorf("token = %+v", tok)
	}
	if gotBody.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q", gotBody.Get("grant_type"))
	}
	if gotBody.Get("refresh_token") != "rtok-1" {
		t.Errorf("refresh_token = %q", gotBody.Get("refresh_token"))
	}
	if gotBody.Get("client_id") != "client-refresh" {
		t.Errorf("client_id = %q", gotBody.Get("client_id"))
	}
	if gotBody.Get("redirect_uri") != "" || gotBody.Get("code_verifier") != "" {
		t.Errorf("refresh request should not include auth-code fields: %v", gotBody)
	}
}

func TestRefreshToken_InvalidGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token expired"}`))
	}))
	defer srv.Close()

	cfg := AuthCodeConfig{Endpoint: srv.URL, TokenPath: "/oauth2/token", ClientID: "x"}
	_, err := RefreshToken(context.Background(), cfg, "expired-refresh-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %q, want to contain 'invalid_grant'", err.Error())
	}
}
