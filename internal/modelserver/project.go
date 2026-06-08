package modelserver

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

func ProjectIDFromToken(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", false
	}
	if v, ok := claims["project_id"].(string); ok && v != "" {
		return v, true
	}
	if ext, ok := claims["ext"].(map[string]any); ok {
		if v, ok := ext["project_id"].(string); ok && v != "" {
			return v, true
		}
	}
	return "", false
}
