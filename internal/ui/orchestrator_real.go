package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/download"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/shortcut"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/vscode"
)

// codexDownloadURL is the URL we fetch codex.exe from. Pinned to a
// specific release tag so the install is reproducible. Bumping the
// version requires updating the tag below; openai/codex does NOT
// publish a SHA256 for the standalone .exe (only for -package-.tar.gz),
// so verification relies on HTTPS + GitHub trust.
const codexReleaseTag = "rust-v0.136.0"

func codexDownloadURL() string {
	return "https://github.com/openai/codex/releases/download/" +
		codexReleaseTag + "/codex-x86_64-pc-windows-msvc.exe"
}

type Deps struct {
	State             *state.Store
	Secrets           secrets.Store
	MS                *modelserver.Client
	AS                *agentserver.Client
	MSOAuth           oauth.Config
	ASOAuth           oauth.Config
	CodexConfigPath   string
	VSCodeUserDataDir string
	VSCodeExtDir      string
	EmbeddedVSIXPath  string
	CodexAbsPath      string
	// CodexDownloadURL overrides the default GitHub Releases URL when set.
	// Empty = use the pinned default (codexDownloadURL()). Tests inject a
	// local httptest URL here to avoid a 246MB real download.
	CodexDownloadURL  string

	// Used by Finalize (set by launcher; see P9.3)
	LauncherExePath   string
	OpenFolderExePath string
	IconPath          string
}

type realOrchestrator struct {
	d Deps
	// transient: in-flight device-code challenges per step
	msChallenge oauth.DeviceCodeChallenge
	asChallenge oauth.DeviceCodeChallenge
	msToken     oauth.Token
	asToken     oauth.Token
}

func NewRealOrchestrator(d Deps) Orchestrator {
	return &realOrchestrator{d: d}
}

func (r *realOrchestrator) State(ctx context.Context) (SanitizedState, error) {
	s, err := r.d.State.Load()
	if err != nil {
		return SanitizedState{}, err
	}
	return SanitizedState{
		SchemaVersion:          s.SchemaVersion,
		InstallID:              s.InstallID,
		OnboardingStatus:       string(s.Onboarding.Status),
		CompletedSteps:         append([]string(nil), s.Onboarding.CompletedSteps...),
		LastError:              s.Onboarding.LastError,
		ModelserverProjectID:   s.Modelserver.ProjectID,
		AgentserverWorkspaceID: s.Agentserver.WorkspaceID,
		VSCodePath:             s.VSCode.Path,
		VSCodeVersion:          s.VSCode.Version,
	}, nil
}

func (r *realOrchestrator) LoginModelserver(ctx context.Context) (oauth.DeviceCodeChallenge, error) {
	ch, err := oauth.RequestDeviceCode(ctx, r.d.MSOAuth)
	if err != nil {
		return oauth.DeviceCodeChallenge{}, err
	}
	r.msChallenge = ch
	return ch, nil
}

func (r *realOrchestrator) PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error) {
	if r.msChallenge.DeviceCode == "" {
		return modelserver.APIKey{}, fmt.Errorf("no in-flight modelserver login")
	}
	tok, err := oauth.PollToken(ctx, r.d.MSOAuth, r.msChallenge)
	if err != nil {
		return modelserver.APIKey{}, err
	}
	r.msToken = tok
	proj, err := r.d.MS.PickOrCreateProject(ctx, tok.AccessToken, "default")
	if err != nil {
		return modelserver.APIKey{}, err
	}
	key, err := r.d.MS.CreateAPIKey(ctx, tok.AccessToken, proj.ID, "agentserver-vscode")
	if err != nil {
		return modelserver.APIKey{}, err
	}
	if err := r.d.Secrets.Set("modelserver_api_key", key.Secret); err != nil {
		return modelserver.APIKey{}, err
	}
	if err := r.d.State.Update(func(s *state.State) error {
		s.Modelserver.ProjectID = proj.ID
		s.Modelserver.APIKeySuffix = key.KeySuffix
		s.Onboarding.AddCompleted("modelserver_login")
		return nil
	}); err != nil {
		return modelserver.APIKey{}, err
	}
	return key, nil
}

func (r *realOrchestrator) LoginAgentserver(ctx context.Context) (oauth.DeviceCodeChallenge, error) {
	ch, err := oauth.RequestDeviceCode(ctx, r.d.ASOAuth)
	if err != nil {
		return oauth.DeviceCodeChallenge{}, err
	}
	r.asChallenge = ch
	return ch, nil
}

func (r *realOrchestrator) PollAgentserverLogin(ctx context.Context) (agentserver.WorkspaceAPIKey, error) {
	if r.asChallenge.DeviceCode == "" {
		return agentserver.WorkspaceAPIKey{}, fmt.Errorf("no in-flight agentserver login")
	}
	tok, err := oauth.PollToken(ctx, r.d.ASOAuth, r.asChallenge)
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	r.asToken = tok
	ws, err := r.d.AS.GetOrCreateDefaultWorkspace(ctx, tok.AccessToken, "default")
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	key, err := r.d.AS.CreateWorkspaceAPIKey(ctx, tok.AccessToken, ws.ID, "agentserver-vscode")
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	if err := r.d.Secrets.Set("agentserver_ws_api_key", key.Secret); err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	if err := r.d.State.Update(func(s *state.State) error {
		s.Agentserver.WorkspaceID = ws.ID
		s.Agentserver.WorkspaceAPIKeySuffix = key.KeySuffix
		s.Onboarding.AddCompleted("agentserver_login")
		return nil
	}); err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	return key, nil
}

func (r *realOrchestrator) EnsureVSCode(ctx context.Context, ch chan<- ProgressEvent) error {
	det, _ := vscode.Detect()
	if det.Installed {
		if err := r.d.State.Update(func(s *state.State) error {
			s.VSCode.Path = det.Path
			s.VSCode.Version = det.Version
			s.VSCode.InstalledByUs = false
			s.Onboarding.AddCompleted("vscode_installed")
			return nil
		}); err != nil {
			return err
		}
		return nil
	}
	plan := vscode.PlanInstall()
	cache := filepath.Join(r.d.VSCodeUserDataDir, "..", "cache",
		"vscode-"+vscode.LockedVersion+plan.FileExt)
	if err := download.DownloadResumable(ctx, plan.URL, cache, plan.SHA256,
		downloadAdapter(ch)); err != nil {
		return fmt.Errorf("download VS Code: %w", err)
	}
	if err := vscode.SilentInstall(ctx, cache, plan); err != nil {
		return fmt.Errorf("install VS Code: %w", err)
	}
	det2, err := vscode.Detect()
	if err != nil {
		return fmt.Errorf("post-install detect: %w", err)
	}
	return r.d.State.Update(func(s *state.State) error {
		s.VSCode.Path = det2.Path
		s.VSCode.Version = det2.Version
		s.VSCode.InstalledByUs = true
		s.Onboarding.AddCompleted("vscode_installed")
		return nil
	})
}

func downloadAdapter(ui chan<- ProgressEvent) chan<- download.ProgressEvent {
	if ui == nil {
		return nil
	}
	out := make(chan download.ProgressEvent, 16)
	go func() {
		for ev := range out {
			ui <- ProgressEvent{
				Stage: ev.Stage, Downloaded: ev.Downloaded, Total: ev.Total,
				SpeedBps: ev.SpeedBps, Msg: ev.Msg,
			}
		}
	}()
	return out
}

func (r *realOrchestrator) ConfigureVSCode(ctx context.Context) error {
	s, err := r.d.State.Load()
	if err != nil {
		return err
	}
	if s.VSCode.Path == "" {
		return fmt.Errorf("ConfigureVSCode: vscode.Path unknown — run EnsureVSCode first")
	}
	// Download codex.exe to r.d.CodexAbsPath if missing.
	if r.d.CodexAbsPath != "" {
		if _, statErr := os.Stat(r.d.CodexAbsPath); os.IsNotExist(statErr) {
			if err := os.MkdirAll(filepath.Dir(r.d.CodexAbsPath), 0o755); err != nil {
				return fmt.Errorf("mkdir codex bin dir: %w", err)
			}
			url := r.d.CodexDownloadURL
			if url == "" {
				url = codexDownloadURL()
			}
			// SHA256 left empty: openai/codex publishes SHA256SUMS only for
			// the -package- .tar.gz variants, not the standalone .exe.
			// We trust HTTPS + GitHub Releases. Upgrade path: pin a sha
			// once OpenAI publishes one for the .exe.
			if err := download.DownloadResumable(ctx, url,
				r.d.CodexAbsPath, "", nil); err != nil {
				return fmt.Errorf("download codex: %w", err)
			}
		}
	}
	// Write settings.json
	settingsPath := filepath.Join(r.d.VSCodeUserDataDir, "User", "settings.json")
	if err := vscode.WriteSettings(settingsPath, vscode.SettingsInput{
		CodexAbsPath: r.d.CodexAbsPath,
	}); err != nil {
		return err
	}
	// Write/merge ~/.codex/config.toml
	if err := codex.UpdateConfig(r.d.CodexConfigPath, codex.Settings{
		Provider: "modelserver", Model: "gpt-5.5",
		BaseURL: "https://code.ai.cs.ac.cn/v1",
		EnvKey:  "OPENAI_API_KEY", WireAPI: "responses",
	}); err != nil {
		return err
	}
	// Setx OPENAI_API_KEY (no-op on non-Windows)
	if r.d.Secrets != nil {
		apiKey, err := r.d.Secrets.Get("modelserver_api_key")
		if err == nil {
			_ = env.PersistUserEnv("OPENAI_API_KEY", apiKey)
		}
	}
	// Install zh-hans language pack + our embedded .vsix
	if err := vscode.InstallExtensions(ctx, vscode.Installer{
		CodeExe:       s.VSCode.Path,
		UserDataDir:   r.d.VSCodeUserDataDir,
		ExtensionsDir: r.d.VSCodeExtDir,
		Extensions: []string{
			"MS-CEINTL.vscode-language-pack-zh-hans",
			r.d.EmbeddedVSIXPath,
		},
	}); err != nil {
		return err
	}
	return r.d.State.Update(func(s *state.State) error {
		s.Onboarding.AddCompleted("vscode_configured")
		return nil
	})
}
func (r *realOrchestrator) Finalize(ctx context.Context) error {
	if r.d.LauncherExePath != "" {
		if err := shortcut.EnsureDesktopShortcut(shortcut.DesktopInput{
			Name:      "agentserver-vscode",
			TargetExe: r.d.LauncherExePath,
			IconPath:  r.d.IconPath,
		}); err != nil {
			return err
		}
		if err := r.d.State.Update(func(s *state.State) error {
			s.Shortcuts.DesktopCreated = true
			return nil
		}); err != nil {
			return err
		}
	}
	if r.d.OpenFolderExePath != "" {
		if err := shortcut.InstallContextMenu(shortcut.ContextMenuInput{
			MenuLabel:         "用 agentserver-vscode 打开",
			HandlerExe:        r.d.OpenFolderExePath,
			IconPath:          r.d.IconPath,
			RegistryKeySuffix: "AgentserverVscode",
		}); err != nil {
			return err
		}
		if err := r.d.State.Update(func(s *state.State) error {
			s.Shortcuts.ContextMenuInstalled = true
			return nil
		}); err != nil {
			return err
		}
	}
	return r.d.State.Update(func(s *state.State) error {
		s.Onboarding.AddCompleted("shortcuts_created")
		s.Onboarding.Status = state.StatusComplete
		return nil
	})
}
func (r *realOrchestrator) Abort(ctx context.Context) error { return nil }
