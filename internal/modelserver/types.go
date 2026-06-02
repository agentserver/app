package modelserver

import "time"

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type APIKey struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	KeySuffix string    `json:"key_suffix"`
	Status    string    `json:"status"`
	Secret    string    `json:"-"` // populated from create response wrapper
	CreatedAt time.Time `json:"created_at"`
}
