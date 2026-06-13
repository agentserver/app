package agentserver

import "testing"

func TestOAuthConfigUsesPublicAgentCLIClient(t *testing.T) {
	got := OAuthConfig("")
	if got.Endpoint != "https://agent.cs.ac.cn" {
		t.Fatalf("Endpoint=%q", got.Endpoint)
	}
	if got.AuthPath != "/api/oauth2/device/auth" {
		t.Fatalf("AuthPath=%q", got.AuthPath)
	}
	if got.TokenPath != "/api/oauth2/token" {
		t.Fatalf("TokenPath=%q", got.TokenPath)
	}
	if got.ClientID != "agentserver-agent-cli" {
		t.Fatalf("ClientID=%q", got.ClientID)
	}
	if got.Scope != "openid profile agent:register" {
		t.Fatalf("Scope=%q", got.Scope)
	}
}

func TestOAuthConfigTrimsEndpoint(t *testing.T) {
	got := OAuthConfig("https://agent.test/")
	if got.AuthURL() != "https://agent.test/api/oauth2/device/auth" {
		t.Fatalf("AuthURL=%q", got.AuthURL())
	}
}
