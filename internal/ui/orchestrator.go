// Package ui exposes the onboarding web UI as an embedded SPA driven via
// HTTP JSON-RPC + Server-Sent Events.
package ui

import (
	"context"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

// Orchestrator is the side-effecting backend driven by the SPA.
// Each method is idempotent: calling twice after success is a no-op.
type Orchestrator interface {
	State(ctx context.Context) (SanitizedState, error)

	// LoginModelserver kicks off PKCE: reserves a callback port, opens browser
	// to the OAuth URL, starts the listener. Returns the URL so the front-end
	// can also render a "browser didn't open?" fallback link.
	LoginModelserver(ctx context.Context) (oauthURL string, err error)
	PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error)

	// LoginAgentserver kicks off device flow: requests a device_code, opens
	// browser to the verification URL, stores the in-flight challenge.
	// Returns the verification_uri_complete URL (already contains user_code).
	LoginAgentserver(ctx context.Context) (oauthURL string, err error)
	PollAgentserverLogin(ctx context.Context) (agentserver.WorkspaceAPIKey, error)

	EnsureVSCode(ctx context.Context, progress chan<- ProgressEvent) error
	ConfigureVSCode(ctx context.Context) error
	EnsureFrontend(ctx context.Context, progress chan<- ProgressEvent) error
	ConfigureFrontend(ctx context.Context) error

	Finalize(ctx context.Context) error
	Abort(ctx context.Context) error

	// LaunchAndShutdown spawns VS Code (via the configured Code executable)
	// and asks the launcher to gracefully shut down its onboarding HTTP
	// server. The shutdown is async (after a short delay so the HTTP
	// response can flush). Returns once VS Code is spawned, not when it
	// exits. If no shutdown hook is registered with the implementation,
	// this is just a VS Code launch. The ctx is reserved for interface
	// uniformity; the current implementation does not observe cancellation
	// because cmd.Start returns essentially instantly.
	LaunchAndShutdown(ctx context.Context) error
}

// SanitizedState is the read view sent to the browser — never contains secrets.
type SanitizedState struct {
	SchemaVersion            int      `json:"schema_version"`
	InstallID                string   `json:"install_id"`
	OnboardingStatus         string   `json:"onboarding_status"`
	CompletedSteps           []string `json:"completed_steps"`
	LastError                string   `json:"last_error,omitempty"`
	FrontendMode             string   `json:"frontend_mode"`
	FrontendName             string   `json:"frontend_name"`
	ModelserverProjectID     string   `json:"modelserver_project_id,omitempty"`
	AgentserverWorkspaceID   string   `json:"agentserver_workspace_id,omitempty"`
	AgentserverWorkspaceName string   `json:"agentserver_workspace_name,omitempty"`
	VSCodePath               string   `json:"vscode_path,omitempty"`
	VSCodeVersion            string   `json:"vscode_version,omitempty"`
	CodexDesktopInstalled    bool     `json:"codex_desktop_installed,omitempty"`
	CodexDesktopVersion      string   `json:"codex_desktop_version,omitempty"`
}

func SanitizeState(s *state.State) SanitizedState {
	mode := state.NormalizeFrontendMode(s.FrontendMode)
	return SanitizedState{
		SchemaVersion:            s.SchemaVersion,
		InstallID:                s.InstallID,
		OnboardingStatus:         string(s.Onboarding.Status),
		CompletedSteps:           append([]string(nil), s.Onboarding.CompletedSteps...),
		LastError:                s.Onboarding.LastError,
		FrontendMode:             string(mode),
		FrontendName:             frontendName(mode),
		ModelserverProjectID:     s.Modelserver.ProjectID,
		AgentserverWorkspaceID:   s.Agentserver.WorkspaceID,
		AgentserverWorkspaceName: s.Agentserver.WorkspaceName,
		VSCodePath:               s.VSCode.Path,
		VSCodeVersion:            s.VSCode.Version,
		CodexDesktopInstalled:    s.CodexDesktop.Installed,
		CodexDesktopVersion:      s.CodexDesktop.Version,
	}
}

func frontendName(mode state.FrontendMode) string {
	if state.NormalizeFrontendMode(mode) == state.FrontendModeMinimalVSCode {
		return "极简界面"
	}
	return "Codex Desktop"
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
	return SanitizedState{
		SchemaVersion:    1,
		InstallID:        "noop",
		OnboardingStatus: string(state.StatusPending),
		FrontendMode:     string(state.FrontendModeCodexDesktop),
		FrontendName:     "Codex Desktop",
	}, nil
}
func (noopOrchestrator) LoginModelserver(context.Context) (string, error) {
	return "https://example.invalid/oauth/test", nil
}
func (noopOrchestrator) PollModelserverLogin(context.Context) (modelserver.APIKey, error) {
	return modelserver.APIKey{}, nil
}
func (noopOrchestrator) LoginAgentserver(context.Context) (string, error) {
	return "https://example.invalid/oauth/test?user_code=TEST", nil
}
func (noopOrchestrator) PollAgentserverLogin(context.Context) (agentserver.WorkspaceAPIKey, error) {
	return agentserver.WorkspaceAPIKey{}, nil
}
func (noopOrchestrator) EnsureVSCode(context.Context, chan<- ProgressEvent) error { return nil }
func (noopOrchestrator) ConfigureVSCode(context.Context) error                    { return nil }
func (noopOrchestrator) EnsureFrontend(context.Context, chan<- ProgressEvent) error {
	return nil
}
func (noopOrchestrator) ConfigureFrontend(context.Context) error { return nil }
func (noopOrchestrator) Finalize(context.Context) error          { return nil }
func (noopOrchestrator) Abort(context.Context) error             { return nil }
func (noopOrchestrator) LaunchAndShutdown(context.Context) error { return nil }
