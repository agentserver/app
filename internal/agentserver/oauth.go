package agentserver

import (
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/oauth"
)

const DefaultEndpoint = "https://agent.cs.ac.cn"

func OAuthConfig(endpoint string) oauth.Config {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	return oauth.Config{
		Endpoint:  strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		AuthPath:  "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token",
		ClientID:  "agentserver-agent-cli",
		Scope:     "openid profile agent:register",
	}
}
