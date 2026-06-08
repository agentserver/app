// Package agentserver wraps the relevant HTTP endpoints of agent.cs.ac.cn.
package agentserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    http.DefaultClient,
	}
}

func (c *Client) ListWorkspaces(ctx context.Context, token string) ([]Workspace, error) {
	var wrap struct {
		Data []Workspace `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/workspaces", token, nil, &wrap); err != nil {
		return nil, err
	}
	return wrap.Data, nil
}

func (c *Client) CreateWorkspace(ctx context.Context, token, name string) (Workspace, error) {
	var wrap struct {
		Data Workspace `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/workspaces", token,
		map[string]string{"name": name}, &wrap); err != nil {
		return Workspace{}, err
	}
	return wrap.Data, nil
}

func (c *Client) GetOrCreateDefaultWorkspace(ctx context.Context, token, name string) (Workspace, error) {
	ws, err := c.ListWorkspaces(ctx, token)
	if err != nil {
		return Workspace{}, err
	}
	for _, w := range ws {
		if w.Name == name {
			return w, nil
		}
	}
	return c.CreateWorkspace(ctx, token, name)
}

func (c *Client) Whoami(ctx context.Context, token string) (Identity, error) {
	var raw map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/agent/whoami", token, nil, &raw); err != nil {
		return Identity{}, err
	}
	return identityFromWhoami(raw), nil
}

func (c *Client) RegisterAgent(ctx context.Context, oauthToken, name, typ string) (AgentRegistration, error) {
	var out AgentRegistration
	if err := c.do(ctx, http.MethodPost, "/api/agent/register", oauthToken,
		map[string]string{"name": name, "type": typ}, &out); err != nil {
		return AgentRegistration{}, err
	}
	return out, nil
}

func (c *Client) CreateWorkspaceAPIKey(ctx context.Context, token, workspaceID, name string) (WorkspaceAPIKey, error) {
	var wrap struct {
		Data WorkspaceAPIKey `json:"data"`
		Key  string          `json:"key"`
	}
	if err := c.do(ctx, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/api-keys", token,
		map[string]any{"name": name}, &wrap); err != nil {
		return WorkspaceAPIKey{}, err
	}
	wrap.Data.Secret = wrap.Key
	return wrap.Data, nil
}

func (c *Client) do(ctx context.Context, method, path, token string,
	body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, b)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func identityFromWhoami(raw map[string]any) Identity {
	userID := stringFromAny(raw["user_id"])
	if userID == "" {
		userID = stringFromAny(raw["sub"])
	}
	ws := workspaceFromWhoami(raw, 0)
	return Identity{UserID: userID, Workspace: ws}
}

func workspaceFromWhoami(raw map[string]any, depth int) Workspace {
	if raw == nil || depth > 3 {
		return Workspace{}
	}
	ws := Workspace{
		ID:   firstString(raw["workspace_id"], raw["workspaceID"], raw["id"]),
		Name: firstString(raw["workspace_name"], raw["workspaceName"], raw["name"]),
	}
	if ws.ID != "" {
		return ws
	}
	for _, key := range []string{"workspace", "current_workspace", "currentWorkspace", "data"} {
		if nested, ok := raw[key].(map[string]any); ok {
			if got := workspaceFromWhoami(nested, depth+1); got.ID != "" {
				return got
			}
		}
	}
	return Workspace{}
}

func firstString(values ...any) string {
	for _, v := range values {
		if s := stringFromAny(v); s != "" {
			return s
		}
	}
	return ""
}

func stringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
