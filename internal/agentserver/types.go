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
