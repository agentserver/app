package modelserver

import (
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/oauth"
)

func DeviceConfig(endpoint string) oauth.Config {
	cfg := OAuthConfig()
	if strings.TrimSpace(endpoint) == "" {
		endpoint = cfg.Endpoint
	}
	return oauth.Config{
		Endpoint:  strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		AuthPath:  "/oauth2/device/auth",
		TokenPath: cfg.TokenPath,
		ClientID:  cfg.ClientID,
		Scope:     cfg.Scope,
	}
}
