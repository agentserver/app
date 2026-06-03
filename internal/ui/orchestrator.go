// Package ui exposes the onboarding web UI as an embedded SPA driven via
// HTTP JSON-RPC + Server-Sent Events.
package ui

import (
	"context"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
)

// Orchestrator is the side-effecting backend driven by the SPA.
// Each method is idempotent: calling twice after success is a no-op.
type Orchestrator interface {
	State(ctx context.Context) (SanitizedState, error)

	LoginModelserver(ctx context.Context) (oauth.DeviceCodeChallenge, error)
	PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error)

	LoginAgentserver(ctx context.Context) (oauth.DeviceCodeChallenge, error)
	PollAgentserverLogin(ctx context.Context) (agentserver.WorkspaceAPIKey, error)

	EnsureVSCode(ctx context.Context, progress chan<- ProgressEvent) error
	ConfigureVSCode(ctx context.Context) error

	Finalize(ctx context.Context) error
	Abort(ctx context.Context) error
}

// SanitizedState is the read view sent to the browser — never contains secrets.
type SanitizedState struct {
	SchemaVersion          int      `json:"schema_version"`
	InstallID              string   `json:"install_id"`
	OnboardingStatus       string   `json:"onboarding_status"`
	CompletedSteps         []string `json:"completed_steps"`
	LastError              string   `json:"last_error,omitempty"`
	ModelserverProjectID   string   `json:"modelserver_project_id,omitempty"`
	AgentserverWorkspaceID string   `json:"agentserver_workspace_id,omitempty"`
	VSCodePath             string   `json:"vscode_path,omitempty"`
	VSCodeVersion          string   `json:"vscode_version,omitempty"`
}

type ProgressEvent struct {
	Stage      string `json:"stage"`
	Downloaded int64  `json:"downloaded,omitempty"`
	Total      int64  `json:"total,omitempty"`
	SpeedBps   int64  `json:"speed_bps,omitempty"`
	Msg        string `json:"msg,omitempty"`
}

// noopOrchestrator is used in tests + smoke runs (no state mutation).
type noopOrchestrator struct{}

// NewNoopOrchestrator returns an Orchestrator that does nothing.
// Useful for UI debugging or smoke tests where you don't want real side effects.
func NewNoopOrchestrator() Orchestrator { return noopOrchestrator{} }

func (noopOrchestrator) State(context.Context) (SanitizedState, error) {
	return SanitizedState{}, nil
}
func (noopOrchestrator) LoginModelserver(context.Context) (oauth.DeviceCodeChallenge, error) {
	return oauth.DeviceCodeChallenge{UserCode: "TEST"}, nil
}
func (noopOrchestrator) PollModelserverLogin(context.Context) (modelserver.APIKey, error) {
	return modelserver.APIKey{}, nil
}
func (noopOrchestrator) LoginAgentserver(context.Context) (oauth.DeviceCodeChallenge, error) {
	return oauth.DeviceCodeChallenge{UserCode: "TEST"}, nil
}
func (noopOrchestrator) PollAgentserverLogin(context.Context) (agentserver.WorkspaceAPIKey, error) {
	return agentserver.WorkspaceAPIKey{}, nil
}
func (noopOrchestrator) EnsureVSCode(context.Context, chan<- ProgressEvent) error { return nil }
func (noopOrchestrator) ConfigureVSCode(context.Context) error                    { return nil }
func (noopOrchestrator) Finalize(context.Context) error                           { return nil }
func (noopOrchestrator) Abort(context.Context) error                              { return nil }
