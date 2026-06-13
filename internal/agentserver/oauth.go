package agentserver

import (
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/oauth"
)

func OAuthConfig(endpoint string) oauth.Config {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "https://agent.cs.ac.cn"
	}
	return oauth.Config{
		Endpoint:  strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		AuthPath:  "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token",
		ClientID:  "agentserver-agent-cli",
		Scope:     "openid profile agent:register",
	}
}
