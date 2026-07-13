// Package ui exposes the onboarding web UI as an embedded SPA driven via
// HTTP JSON-RPC + Server-Sent Events.
package ui

import (
	"context"
	"errors"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

type userVisibleError struct {
	message string
	cause   error
}

func (e userVisibleError) Error() string { return e.message }

func (e userVisibleError) Unwrap() error { return e.cause }

// SafeFrontendLaunchError hides operational details while retaining cause
// identity for errors.Is/errors.As and internal control flow.
func SafeFrontendLaunchError(mode state.FrontendMode, cause error) error {
	if cause == nil {
		return nil
	}
	message := ""
	switch state.NormalizeFrontendMode(mode) {
	case state.FrontendModeOpenCodeDesktop:
		message = "OpenCode Desktop 启动失败。请确认应用已安装且可正常打开，然后重试。"
	case state.FrontendModeMinimalVSCode:
		message = "极简界面启动失败。请确认 VS Code 已安装且可正常打开，然后重试。"
	default:
		switch {
		case errors.Is(cause, codexdesktop.ErrNotFound):
			message = "未检测到 " + codexdesktop.LongDisplayName + "。请从 Microsoft Store 安装产品 ID " + codexdesktop.CodexStoreProductID + " 后重试。"
		case errors.Is(cause, codexdesktop.ErrSchemeMissing):
			message = "ChatGPT / Codex 的 codex:// 协议缺失。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。"
		case errors.Is(cause, codexdesktop.ErrSchemeTargetInvalid):
			message = "ChatGPT / Codex 的 codex:// 协议已注册，但处理程序无效或不受信任。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。"
		case errors.Is(cause, codexdesktop.ErrLaunchFailed):
			message = "ChatGPT / Codex 桌面应用本身无法启动。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。"
		default:
			message = "ChatGPT / Codex 启动失败。请重新运行安装向导检查配置后重试。"
		}
	}
	return userVisibleError{message: boundedUserVisibleError(message), cause: cause}
}

// SafeFrontendInstallError hides installer and detector output while retaining
// cause identity for errors.Is/errors.As and internal diagnostics.
func SafeFrontendInstallError(mode state.FrontendMode, cause error) error {
	if cause == nil {
		return nil
	}
	message := ""
	switch state.NormalizeFrontendMode(mode) {
	case state.FrontendModeOpenCodeDesktop:
		message = "OpenCode Desktop 安装或检查失败。请检查网络和安装程序后重试。"
	case state.FrontendModeMinimalVSCode:
		message = "VS Code 安装或检查失败。请检查网络、Microsoft Store 和 Windows App Installer 后重试。"
	default:
		switch {
		case errors.Is(cause, codexdesktop.ErrWingetNotFound):
			message = "未找到 winget；请安装或更新 Windows App Installer 后重试。"
		case errors.Is(cause, codexdesktop.ErrSchemeMissing):
			message = "ChatGPT / Codex 的 codex:// 协议缺失。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。"
		case errors.Is(cause, codexdesktop.ErrSchemeTargetInvalid):
			message = "ChatGPT / Codex 的 codex:// 协议已注册，但处理程序无效或不受信任。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。"
		case errors.Is(cause, codexdesktop.ErrNotFound):
			message = "未能安装或检测到 " + codexdesktop.LongDisplayName + "。请从 Microsoft Store 安装产品 ID " + codexdesktop.CodexStoreProductID + " 后重试。"
		default:
			message = codexdesktop.LongDisplayName + "安装或检查失败。请检查网络、Microsoft Store 和 Windows App Installer 后重试。"
		}
	}
	return userVisibleError{message: boundedUserVisibleError(message), cause: cause}
}

// SafeFrontendStateReadError hides the state-file path from API responses.
func SafeFrontendStateReadError(cause error) error {
	if cause == nil {
		return nil
	}
	return userVisibleError{message: "无法读取前端启动状态，请重试。", cause: cause}
}

// SafeFrontendStatePersistenceError hides the state-file path after launch.
func SafeFrontendStatePersistenceError(cause error) error {
	if cause == nil {
		return nil
	}
	return userVisibleError{message: "前端已启动，但无法保存启动状态。请重试。", cause: cause}
}

func boundedUserVisibleError(message string) string {
	const maxRunes = 256
	runes := []rune(strings.Join(strings.Fields(message), " "))
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}

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

	// LaunchAndShutdown starts the configured frontend and asks the launcher
	// to gracefully shut down its onboarding HTTP
	// server. The shutdown is async (after a short delay so the HTTP
	// response can flush). For ChatGPT/Codex, success includes bounded trusted
	// package-process confirmation. If no shutdown hook is registered, this
	// only launches the frontend.
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
	OpenCodeDesktopInstalled bool     `json:"opencode_desktop_installed,omitempty"`
	OpenCodeDesktopVersion   string   `json:"opencode_desktop_version,omitempty"`
	OpenCodeDesktopPath      string   `json:"opencode_desktop_path,omitempty"`
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
		OpenCodeDesktopInstalled: s.OpenCodeDesktop.Installed,
		OpenCodeDesktopVersion:   s.OpenCodeDesktop.Version,
		OpenCodeDesktopPath:      s.OpenCodeDesktop.Path,
	}
}

func frontendName(mode state.FrontendMode) string {
	switch state.NormalizeFrontendMode(mode) {
	case state.FrontendModeMinimalVSCode:
		return "极简界面"
	case state.FrontendModeOpenCodeDesktop:
		return "OpenCode Desktop"
	default:
		return codexdesktop.ShortDisplayName
	}
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
		FrontendName:     codexdesktop.ShortDisplayName,
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
