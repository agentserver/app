package ui

import (
	"context"
	"errors"
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
	MSOAuth           oauth.AuthCodeConfig // PKCE (modelserver path)
	ASOAuth           oauth.Config         // device code (agentserver path)
	CodexConfigPath   string
	VSCodeUserDataDir string
	VSCodeExtDir      string
	EmbeddedVSIXPath  string
	CodexAbsPath      string
	// CodexDownloadURL overrides the default GitHub Releases URL when set.
	CodexDownloadURL string

	// OpenBrowser is invoked by the orchestrator after starting the PKCE
	// listener. Optional in tests.
	OpenBrowser func(string)

	// Used by Finalize (set by launcher; see P9.3)
	LauncherExePath   string
	OpenFolderExePath string
	IconPath          string
}

type realOrchestrator struct {
	d Deps
	// modelserver PKCE in-flight session:
	msSession  *oauth.PKCESession
	msCallback <-chan oauth.CallbackResult
	msShutdown func()
	// agentserver device-code in-flight challenge (unchanged):
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

func (r *realOrchestrator) LoginModelserver(ctx context.Context) error {
	// If a previous login is still in-flight (e.g., user clicked retry),
	// release its port + listener before starting a fresh one.
	r.cleanupMS()

	port, ln, err := oauth.ReservePort(r.d.MSOAuth)
	if err != nil {
		if errors.Is(err, oauth.ErrAllPortsBusy) {
			return fmt.Errorf("OAuth 回调端口 %v 全部被占用, 请关闭其他 agentserver-vscode 进程后重试",
				r.d.MSOAuth.Ports)
		}
		return err
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, r.d.MSOAuth.CallbackPath)
	sess, err := oauth.StartPKCE(r.d.MSOAuth, redirectURI)
	if err != nil {
		_ = ln.Close()
		return err
	}
	// Use context.Background() so the PKCE listener outlives the POST
	// request that started the login. The listener shuts itself down via
	// LoginTimeout or an explicit cleanup call.
	ch, shutdown := oauth.StartListening(context.Background(), ln, r.d.MSOAuth, sess.State)
	r.msSession = sess
	r.msCallback = ch
	r.msShutdown = shutdown

	if r.d.OpenBrowser != nil {
		go r.d.OpenBrowser(sess.AuthURL)
	}
	return nil
}

func (r *realOrchestrator) PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error) {
	if r.msSession == nil {
		return modelserver.APIKey{}, fmt.Errorf("no in-flight modelserver login")
	}
	select {
	case res, ok := <-r.msCallback:
		if !ok {
			r.cleanupMS()
			return modelserver.APIKey{}, fmt.Errorf("登录会话已结束, 请重试")
		}
		if res.Error != "" {
			r.cleanupMS()
			return modelserver.APIKey{}, fmt.Errorf("登录被拒绝: %s", res.Error)
		}
		if res.State != r.msSession.State {
			r.cleanupMS()
			return modelserver.APIKey{}, fmt.Errorf("会话状态不匹配, 请重试")
		}
		tok, err := oauth.FinishPKCE(ctx, r.d.MSOAuth, r.msSession, res.Code)
		if err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		r.msToken = tok
		proj, err := r.d.MS.PickOrCreateProject(ctx, tok.AccessToken, "default")
		if err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		key, err := r.d.MS.CreateAPIKey(ctx, tok.AccessToken, proj.ID, "agentserver-vscode")
		if err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		if err := r.d.Secrets.Set("modelserver_api_key", key.Secret); err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		if err := r.d.State.Update(func(s *state.State) error {
			s.Modelserver.ProjectID = proj.ID
			s.Modelserver.APIKeySuffix = key.KeySuffix
			s.Onboarding.AddCompleted("modelserver_login")
			return nil
		}); err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		r.cleanupMS()
		return key, nil
	case <-ctx.Done():
		// Do NOT cleanup: caller (server.handleMSStatus) wraps Poll in a 30s
		// timeout and re-polls. Session stays armed until callback arrives or
		// the PKCE listener's own 10-minute timeout fires.
		return modelserver.APIKey{}, ctx.Err()
	}
}

func (r *realOrchestrator) cleanupMS() {
	if r.msShutdown != nil {
		r.msShutdown()
	}
	r.msSession = nil
	r.msCallback = nil
	r.msShutdown = nil
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
	det2, err := vscode.InstallAndDetect(ctx, cache, plan, vscode.SilentInstall, vscode.Detect)
	if err != nil {
		return fmt.Errorf("install VS Code: %w", err)
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
func (r *realOrchestrator) Abort(ctx context.Context) error {
	r.cleanupMS()
	return nil
}
