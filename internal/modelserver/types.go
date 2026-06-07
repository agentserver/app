package modelserver

import "time"

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SubscriptionUsageWindow struct {
	Window     string  `json:"window"`
	Percentage float64 `json:"percentage"`
	ResetsAt   string  `json:"resets_at,omitempty"`
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
