// Package oauth implements the OAuth 2.0 Device Authorization Grant (RFC 8628).
package oauth

import (
	"strings"
	"time"
)

type Config struct {
	Endpoint  string // e.g. "https://code.cs.ac.cn"
	AuthPath  string // e.g. "/api/oauth2/device/auth"
	TokenPath string // e.g. "/api/oauth2/token"
	ClientID  string
	Scope     string
}

func (c Config) AuthURL() string  { return joinURL(c.Endpoint, c.AuthPath) }
func (c Config) TokenURL() string { return joinURL(c.Endpoint, c.TokenPath) }

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// DeviceCodeChallenge is what the server returns to /device/auth.
type DeviceCodeChallenge struct {
	DeviceCode              string        `json:"device_code"`
	UserCode                string        `json:"user_code"`
	VerificationURI         string        `json:"verification_uri"`
	VerificationURIComplete string        `json:"verification_uri_complete"`
	ExpiresIn               int           `json:"expires_in"`
	Interval                int           `json:"interval"`
	RetrievedAt             time.Time     `json:"-"`
}

func (c DeviceCodeChallenge) ExpiresAt() time.Time {
	if c.RetrievedAt.IsZero() {
		return time.Now().Add(time.Duration(c.ExpiresIn) * time.Second)
	}
	return c.RetrievedAt.Add(time.Duration(c.ExpiresIn) * time.Second)
}

func (c DeviceCodeChallenge) PollInterval() time.Duration {
	iv := c.Interval
	if iv <= 0 {
		iv = 5
	}
	return time.Duration(iv) * time.Second
}

type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
}
