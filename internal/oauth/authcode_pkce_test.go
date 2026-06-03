package oauth

import (
	"crypto/sha256"
	"encoding/base64"
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
