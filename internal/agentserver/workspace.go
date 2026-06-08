package agentserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func WorkspaceIDFromToken(token string) (string, bool) {
	ws, ok := WorkspaceFromToken(token)
	return ws.ID, ok
}

func WorkspaceFromToken(token string) (Workspace, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return Workspace{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Workspace{}, false
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Workspace{}, false
	}
	ws := Workspace{
		ID:   stringFromAny(claims["workspace_id"]),
		Name: stringFromAny(claims["workspace_name"]),
	}
	if ext, ok := claims["ext"].(map[string]any); ok {
		if ws.ID == "" {
			ws.ID = stringFromAny(ext["workspace_id"])
		}
		if ws.Name == "" {
			ws.Name = stringFromAny(ext["workspace_name"])
		}
	}
	if ws.ID != "" {
		return ws, true
	}
	return Workspace{}, false
}

func ResolveWorkspaceID(ctx context.Context, c *Client, token, existingID string) (Workspace, error) {
	_ = ctx
	_ = c

	if ws, ok := WorkspaceFromToken(token); ok {
		return ws, nil
	}
	if existingID != "" {
		return Workspace{ID: existingID}, nil
	}
	return Workspace{}, fmt.Errorf("agentserver: token missing workspace_id claim")
}
