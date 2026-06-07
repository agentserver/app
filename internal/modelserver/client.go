// Package modelserver wraps the relevant HTTP endpoints of code.cs.ac.cn.
package modelserver

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

func (c *Client) ListProjects(ctx context.Context, accessToken string) ([]Project, error) {
	var wrap struct {
		Data []Project `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/projects", accessToken, nil, &wrap); err != nil {
		return nil, err
	}
	return wrap.Data, nil
}

func (c *Client) SubscriptionUsage(ctx context.Context, token, projectID string) ([]SubscriptionUsageWindow, error) {
	var wrap struct {
		Data []SubscriptionUsageWindow `json:"data"`
	}
	if projectID == "" {
		return nil, fmt.Errorf("modelserver: projectID required")
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/subscription/usage", token, nil, &wrap); err != nil {
		return nil, err
	}
	return wrap.Data, nil
}

func (c *Client) CreateProject(ctx context.Context, token, name string) (Project, error) {
	var wrap struct {
		Data Project `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"name": name}, &wrap); err != nil {
		return Project{}, err
	}
	return wrap.Data, nil
}

func (c *Client) CreateAPIKey(ctx context.Context, token, projectID, name string) (APIKey, error) {
	var wrap struct {
		Data APIKey `json:"data"`
		Key  string `json:"key"`
	}
	if err := c.do(ctx, http.MethodPost,
		"/api/v1/projects/"+projectID+"/keys", token,
		map[string]any{"name": name}, &wrap); err != nil {
		return APIKey{}, err
	}
	wrap.Data.Secret = wrap.Key
	return wrap.Data, nil
}

// PickOrCreateProject finds a project named `name`; if none, creates it.
func (c *Client) PickOrCreateProject(ctx context.Context, token, name string) (Project, error) {
	ps, err := c.ListProjects(ctx, token)
	if err != nil {
		return Project{}, err
	}
	for _, p := range ps {
		if p.Name == name {
			return p, nil
		}
	}
	return c.CreateProject(ctx, token, name)
}

// do is the shared JSON request helper.
func (c *Client) do(ctx context.Context, method, path, token string,
	body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
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
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
