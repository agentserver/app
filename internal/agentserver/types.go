package agentserver

import "time"

type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkspaceAPIKey struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	KeySuffix   string    `json:"key_suffix"`
	Status      string    `json:"status"`
	Secret      string    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
}

type Identity struct {
	UserID    string    `json:"user_id,omitempty"`
	Workspace Workspace `json:"workspace"`
}

type AgentRegistration struct {
	SandboxID   string `json:"sandbox_id"`
	TunnelToken string `json:"tunnel_token"`
	ProxyToken  string `json:"proxy_token"`
	WorkspaceID string `json:"workspace_id"`
	ShortID     string `json:"short_id"`
}
