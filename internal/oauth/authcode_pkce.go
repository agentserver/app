package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"time"
)

// AuthCodeConfig is OAuth 2.0 authorization_code with PKCE (RFC 7636).
// Used by the modelserver login path. Separate from Config (device code)
// because the two flows share only the Token type — mixing them in one
// struct invites silent misuse.
type AuthCodeConfig struct {
	Endpoint     string        // "https://codeapi.cs.ac.cn"
	AuthPath     string        // "/oauth2/auth"
	TokenPath    string        // "/oauth2/token"
	ClientID     string        // "5321f7e6-3d79-4ac9-a742-04809dbf9025"
	Scope        string        // "project:inference offline_access"
	CallbackPath string        // "/oauth/modelserver/callback"
	Ports        []int         // [53428..53435]
	LoginTimeout time.Duration // upper bound on a single login (default 10m, set by StartListening)
}

func (c AuthCodeConfig) AuthURL() string  { return joinURL(c.Endpoint, c.AuthPath) }
func (c AuthCodeConfig) TokenURL() string { return joinURL(c.Endpoint, c.TokenPath) }

// PKCESession is one in-flight login attempt.
// Constructed by StartPKCE with a known redirectURI; consumed by FinishPKCE.
// Never reuse.
type PKCESession struct {
	Verifier    string // 43-128 chars base64url (RFC 7636 §4.1)
	Challenge   string // base64url(sha256(verifier))
	State       string // CSRF nonce, ≥16 bytes base64url
	RedirectURI string // full http://127.0.0.1:<port>/<callbackPath>
	AuthURL     string // pre-built browser URL
}

// StartPKCE generates verifier/challenge/state and pre-builds the auth URL.
// The caller MUST have already reserved a callback port (via ReservePort)
// and passed the resulting redirectURI here; AuthURL embeds it.
func StartPKCE(cfg AuthCodeConfig, redirectURI string) (*PKCESession, error) {
	verifier, err := randomURLSafe(64)
	if err != nil {
		return nil, fmt.Errorf("pkce verifier: %w", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	state, err := randomURLSafe(16)
	if err != nil {
		return nil, fmt.Errorf("pkce state: %w", err)
	}
	sess := &PKCESession{
		Verifier:    verifier,
		Challenge:   challenge,
		State:       state,
		RedirectURI: redirectURI,
	}
	sess.AuthURL = buildAuthURL(cfg, sess)
	return sess, nil
}

// buildAuthURL composes the OAuth /oauth2/auth URL with all PKCE params.
func buildAuthURL(cfg AuthCodeConfig, sess *PKCESession) string {
	// Defined fully in Task 2; stubbed here so the first test compiles.
	return cfg.AuthURL()
}

// randomURLSafe returns n bytes of crypto/rand encoded as base64url-without-padding.
func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
