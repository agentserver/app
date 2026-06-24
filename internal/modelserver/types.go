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

// Profile is what GET /api/oauth/profile returns to an OAuth-authenticated
// caller (modelserver PR #63). It exposes the account + project bound to the
// access token without requiring the caller to decode an opaque or JWT token
// locally.
type Profile struct {
	Account ProfileAccount `json:"account"`
	Project ProfileProject `json:"project"`
}

type ProfileAccount struct {
	UUID        string `json:"uuid"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type ProfileProject struct {
	UUID                   string         `json:"uuid"`
	ProjectType            string         `json:"project_type,omitempty"`
	RateLimitTier          string         `json:"rate_limit_tier"`
	SeatTier               string         `json:"seat_tier"`
	HasExtraUsageEnabled   bool           `json:"has_extra_usage_enabled"`
	BillingType            string         `json:"billing_type"`
	CCOnboardingFlags      map[string]any `json:"cc_onboarding_flags"`
	SubscriptionCreatedAt  string         `json:"subscription_created_at,omitempty"`
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
