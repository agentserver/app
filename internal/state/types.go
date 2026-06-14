// Package state holds the persisted onboarding state.
// Sensitive secrets are NOT stored here; they live in keyring.
package state

import "time"

const CurrentSchemaVersion = 1

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusComplete   Status = "complete"
	StatusFailed     Status = "failed"
)

type FrontendMode string

const (
	FrontendModeCodexDesktop    FrontendMode = "codex_desktop"
	FrontendModeOpenCodeDesktop FrontendMode = "opencode_desktop"
	FrontendModeMinimalVSCode   FrontendMode = "minimal_vscode"
)

func NormalizeFrontendMode(mode FrontendMode) FrontendMode {
	switch mode {
	case FrontendModeOpenCodeDesktop:
		return FrontendModeOpenCodeDesktop
	case FrontendModeMinimalVSCode:
		return FrontendModeMinimalVSCode
	default:
		return FrontendModeCodexDesktop
	}
}

type State struct {
	SchemaVersion   int                  `json:"schema_version"`
	InstallID       string               `json:"install_id"`
	CreatedAt       time.Time            `json:"created_at"`
	FrontendMode    FrontendMode         `json:"frontend_mode,omitempty"`
	Onboarding      OnboardingState      `json:"onboarding"`
	Modelserver     ModelserverState     `json:"modelserver"`
	Agentserver     AgentserverState     `json:"agentserver"`
	VSCode          VSCodeState          `json:"vscode"`
	CodexDesktop    CodexDesktopState    `json:"codex_desktop"`
	OpenCodeDesktop OpenCodeDesktopState `json:"opencode_desktop"`
	Shortcuts       ShortcutsState       `json:"shortcuts"`
}

type OnboardingState struct {
	Status         Status   `json:"status"`
	CompletedSteps []string `json:"completed_steps"`
	LastError      string   `json:"last_error,omitempty"`
}

// AddCompleted idempotently appends step to CompletedSteps if not already present. Uses a pointer receiver because it mutates the slice.
func (o *OnboardingState) AddCompleted(step string) {
	for _, s := range o.CompletedSteps {
		if s == step {
			return
		}
	}
	o.CompletedSteps = append(o.CompletedSteps, step)
}

// HasCompleted reports whether step is in CompletedSteps. O(n) — acceptable for the small step count this state tracks.
func (o OnboardingState) HasCompleted(step string) bool {
	for _, s := range o.CompletedSteps {
		if s == step {
			return true
		}
	}
	return false
}

type ModelserverState struct {
	BaseURL         string    `json:"base_url"`
	UserID          string    `json:"user_id,omitempty"`
	ProjectID       string    `json:"project_id,omitempty"`
	APIKeySuffix    string    `json:"api_key_suffix,omitempty"`
	APIKeyCreatedAt time.Time `json:"api_key_created_at,omitempty"`
}

type AgentserverState struct {
	BaseURL               string `json:"base_url"`
	UserID                string `json:"user_id,omitempty"`
	SandboxID             string `json:"sandbox_id,omitempty"`
	ShortID               string `json:"short_id,omitempty"`
	WorkspaceID           string `json:"workspace_id,omitempty"`
	WorkspaceName         string `json:"workspace_name,omitempty"`
	WorkspaceAPIKeySuffix string `json:"workspace_api_key_suffix,omitempty"`
}

type VSCodeState struct {
	Path             string `json:"path,omitempty"`
	Version          string `json:"version,omitempty"`
	InstalledByUs    bool   `json:"installed_by_us"`
	UserDataDir      string `json:"user_data_dir,omitempty"`
	ExtensionsDir    string `json:"extensions_dir,omitempty"`
	ExtensionVersion string `json:"extension_version,omitempty"`
}

type CodexDesktopState struct {
	Installed     bool   `json:"installed"`
	Version       string `json:"version,omitempty"`
	InstalledByUs bool   `json:"installed_by_us"`
}

type OpenCodeDesktopState struct {
	Installed     bool   `json:"installed"`
	Version       string `json:"version,omitempty"`
	Path          string `json:"path,omitempty"`
	InstalledByUs bool   `json:"installed_by_us"`
}

type ShortcutsState struct {
	DesktopCreated       bool `json:"desktop_created"`
	ContextMenuInstalled bool `json:"context_menu_installed"`
}
