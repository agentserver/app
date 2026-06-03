package oauth

import (
	"crypto/sha256"
	"encoding/base64"
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
