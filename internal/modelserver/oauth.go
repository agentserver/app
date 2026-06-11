package modelserver

import "github.com/agentserver/agentserver-pkg/internal/oauth"

func OAuthConfig() oauth.AuthCodeConfig {
	return oauth.AuthCodeConfig{
		Endpoint:     "https://codeapi.cs.ac.cn",
		AuthPath:     "/oauth2/auth",
		TokenPath:    "/oauth2/token",
		ClientID:     "5321f7e6-3d79-4ac9-a742-04809dbf9025",
		Scope:        "project:inference offline_access",
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{53428, 53429, 53430, 53431, 53432, 53433, 53434, 53435},
		ExtraAuthParams: map[string]string{
			"prompt": "consent",
		},
	}
}
