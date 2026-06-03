package oauth

import "testing"

func TestConfigEndpoints(t *testing.T) {
	c := Config{
		Endpoint:  "https://x.example.com/",
		AuthPath:  "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token",
	}
	if got := c.AuthURL(); got != "https://x.example.com/api/oauth2/device/auth" {
		t.Errorf("auth url %q", got)
	}
	if got := c.TokenURL(); got != "https://x.example.com/api/oauth2/token" {
		t.Errorf("token url %q", got)
	}
}
