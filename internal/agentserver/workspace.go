package agentserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func WorkspaceIDFromToken(token string) (string, bool) {
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
	v, ok := claims["workspace_id"].(string)
	return v, ok && v != ""
}

func ResolveWorkspaceID(ctx context.Context, c *Client, token, existingID string) (Workspace, error) {
	if id, ok := WorkspaceIDFromToken(token); ok {
		return Workspace{ID: id}, nil
	}
	if c == nil {
		return Workspace{}, fmt.Errorf("agentserver: client required")
	}
	ws, err := c.ListWorkspaces(ctx, token)
	if err != nil {
		return Workspace{}, err
	}
	if existingID != "" {
		for _, w := range ws {
			if w.ID == existingID {
				return w, nil
			}
		}
	}
	if len(ws) == 0 {
		return Workspace{}, fmt.Errorf("agentserver: no workspaces available")
	}
	return ws[0], nil
}
