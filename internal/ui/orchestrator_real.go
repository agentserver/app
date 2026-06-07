package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/branding"
	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/download"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/shortcut"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
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
	BundledCodexPath  string
	// CodexDownloadURL overrides the default GitHub Releases URL when set.
	CodexDownloadURL string

	CodexDesktopEnsure func(context.Context) (codexdesktop.Detected, error)
	CodexDesktopOpen   func(string) error

	// OpenBrowser is invoked by the orchestrator after starting the PKCE
	// listener. Optional in tests.
	OpenBrowser func(string)

	// Shutdown is invoked by LaunchAndShutdown after VS Code is spawned.
	// The launcher uses this to gracefully close its HTTP server so the
	// process can exit cleanly. Optional in tests.
	Shutdown func()

	// Used by Finalize (set by launcher; see P9.3)
	LauncherExePath       string
	OpenFolderExePath     string
	TokenRefresherExePath string
	IconPath              string
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

func frontendName(mode state.FrontendMode) string {
	if state.NormalizeFrontendMode(mode) == state.FrontendModeMinimalVSCode {
		return "极简界面"
	}
	return "Codex Desktop"
}

func (r *realOrchestrator) frontendMode() (state.FrontendMode, error) {
	s, err := r.d.State.Load()
	if err != nil {
		return state.FrontendModeCodexDesktop, err
	}
	return state.NormalizeFrontendMode(s.FrontendMode), nil
}

func (r *realOrchestrator) State(ctx context.Context) (SanitizedState, error) {
	s, err := r.d.State.Load()
	if err != nil {
		return SanitizedState{}, err
	}
	mode := state.NormalizeFrontendMode(s.FrontendMode)
	return SanitizedState{
		SchemaVersion:          s.SchemaVersion,
		InstallID:              s.InstallID,
		OnboardingStatus:       string(s.Onboarding.Status),
		CompletedSteps:         append([]string(nil), s.Onboarding.CompletedSteps...),
		LastError:              s.Onboarding.LastError,
		FrontendMode:           string(mode),
		FrontendName:           frontendName(mode),
		ModelserverProjectID:   s.Modelserver.ProjectID,
		AgentserverWorkspaceID: s.Agentserver.WorkspaceID,
		VSCodePath:             s.VSCode.Path,
		VSCodeVersion:          s.VSCode.Version,
		CodexDesktopInstalled:  s.CodexDesktop.Installed,
		CodexDesktopVersion:    s.CodexDesktop.Version,
	}, nil
}

func (r *realOrchestrator) LoginModelserver(ctx context.Context) (string, error) {
	// If a previous login is still in-flight (e.g., user clicked retry),
	// release its port + listener before starting a fresh one.
	r.cleanupMS()

	port, ln, err := oauth.ReservePort(r.d.MSOAuth)
	if err != nil {
		if errors.Is(err, oauth.ErrAllPortsBusy) {
			return "", fmt.Errorf("OAuth 回调端口 %v 全部被占用, 请关闭其他 %s 进程后重试",
				r.d.MSOAuth.Ports, branding.DisplayName)
		}
		return "", err
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, r.d.MSOAuth.CallbackPath)
	sess, err := oauth.StartPKCE(r.d.MSOAuth, redirectURI)
	if err != nil {
		_ = ln.Close()
		return "", err
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
	return sess.AuthURL, nil
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

		// Use the PKCE access_token directly as the OPENAI_API_KEY for codex.
		// The proxy path /v1/* (internal/proxy/auth_middleware.go) accepts any
		// raw Hydra token via introspection fallback when it doesn't match the
		// "ms-" API-key prefix.
		key := modelserver.APIKey{
			Secret:    tok.AccessToken,
			KeySuffix: lastN(tok.AccessToken, 4),
		}
		if _, err := tokenrefresh.StoreToken(r.d.Secrets, tok, time.Now().UTC(), ""); err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		project, err := r.d.MS.PickOrCreateProject(ctx, tok.AccessToken, "default")
		if err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, fmt.Errorf("resolve modelserver project: %w", err)
		}
		if project.ID == "" {
			r.cleanupMS()
			return modelserver.APIKey{}, fmt.Errorf("resolve modelserver project: empty project id")
		}
		if r.d.TokenRefresherExePath != "" {
			_ = tokenrefresh.StartDaemon(r.d.TokenRefresherExePath)
		}
		if err := r.d.State.Update(func(s *state.State) error {
			s.Modelserver.ProjectID = project.ID
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

// lastN returns the last n chars of s (or all of s if shorter). Used to
// derive a short display suffix from an opaque token without leaking it.
func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func (r *realOrchestrator) cleanupMS() {
	if r.msShutdown != nil {
		r.msShutdown()
	}
	r.msSession = nil
	r.msCallback = nil
	r.msShutdown = nil
}

func (r *realOrchestrator) LoginAgentserver(ctx context.Context) (string, error) {
	ch, err := oauth.RequestDeviceCode(ctx, r.d.ASOAuth)
	if err != nil {
		return "", err
	}
	r.asChallenge = ch
	url := ch.VerificationURIComplete
	if url == "" {
		url = ch.VerificationURI
	}
	if url != "" && r.d.OpenBrowser != nil {
		go r.d.OpenBrowser(url)
	}
	return url, nil
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

	// Use the device-code access_token directly as the agentserver credential.
	// Resolve and persist the workspace ID separately, but keep the OAuth
	// token as the secret used by downstream loom components.
	key := agentserver.WorkspaceAPIKey{
		Secret:    tok.AccessToken,
		KeySuffix: lastN(tok.AccessToken, 4),
	}
	if err := r.d.Secrets.Set("agentserver_ws_api_key", key.Secret); err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	current, err := r.d.State.Load()
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	workspace, err := agentserver.ResolveWorkspaceID(ctx, r.d.AS, tok.AccessToken, current.Agentserver.WorkspaceID)
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, fmt.Errorf("resolve agentserver workspace: %w", err)
	}
	if workspace.ID == "" {
		return agentserver.WorkspaceAPIKey{}, fmt.Errorf("resolve agentserver workspace: empty workspace id")
	}
	if err := r.d.State.Update(func(s *state.State) error {
		s.Agentserver.WorkspaceID = workspace.ID
		s.Agentserver.WorkspaceAPIKeySuffix = key.KeySuffix
		s.Onboarding.AddCompleted("agentserver_login")
		return nil
	}); err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	return key, nil
}

func (r *realOrchestrator) EnsureFrontend(ctx context.Context, ch chan<- ProgressEvent) error {
	mode, err := r.frontendMode()
	if err != nil {
		return err
	}
	if mode == state.FrontendModeMinimalVSCode {
		return r.EnsureVSCode(ctx, ch)
	}
	return r.EnsureCodexDesktop(ctx, ch)
}

func (r *realOrchestrator) ConfigureFrontend(ctx context.Context) error {
	mode, err := r.frontendMode()
	if err != nil {
		return err
	}
	if mode == state.FrontendModeMinimalVSCode {
		return r.ConfigureVSCode(ctx)
	}
	return r.ConfigureCodexDesktop(ctx)
}

func (r *realOrchestrator) EnsureCodexDesktop(ctx context.Context, ch chan<- ProgressEvent) error {
	ensure := r.d.CodexDesktopEnsure
	if ensure == nil {
		ensure = func(ctx context.Context) (codexdesktop.Detected, error) {
			return codexdesktop.EnsureInstalled(ctx, codexdesktop.Options{})
		}
	}
	if ch != nil {
		ch <- ProgressEvent{Stage: "checking", Msg: "正在检查 Codex Desktop..."}
	}
	det, err := ensure(ctx)
	if err != nil {
		return err
	}
	if ch != nil {
		ch <- ProgressEvent{Stage: "verified", Msg: "已检测到 Codex Desktop"}
	}
	return r.d.State.Update(func(s *state.State) error {
		s.CodexDesktop.Installed = true
		s.CodexDesktop.Version = det.Version
		s.CodexDesktop.InstalledByUs = true
		s.Onboarding.AddCompleted("codex_desktop_installed")
		return nil
	})
}

func (r *realOrchestrator) EnsureVSCode(ctx context.Context, ch chan<- ProgressEvent) error {
	det, _ := vscode.Detect()
	if det.Installed {
		// Emit a progress event so the front-end SSE stream has something
		// to render even on the fast-path (skip download). Without this
		// the user sees nothing happen and assumes the button is broken.
		if ch != nil {
			select {
			case ch <- ProgressEvent{Stage: "detected", Msg: "已检测到 VS Code " + det.Version + ", 跳过下载"}:
			default:
			}
		}
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

func (r *realOrchestrator) configureSharedCodex(ctx context.Context) error {
	_ = ctx
	if err := codex.UpdateConfig(r.d.CodexConfigPath, codex.ModelserverSettings()); err != nil {
		return err
	}
	if r.d.Secrets != nil {
		apiKey, err := r.d.Secrets.Get("modelserver_api_key")
		if err == nil {
			_ = env.PersistUserEnv("OPENAI_API_KEY", apiKey)
			_ = os.Setenv("OPENAI_API_KEY", apiKey)
		}
	}
	if r.d.TokenRefresherExePath != "" {
		_ = tokenrefresh.StartDaemon(r.d.TokenRefresherExePath)
	}
	return nil
}

func (r *realOrchestrator) ConfigureCodexDesktop(ctx context.Context) error {
	if err := r.configureSharedCodex(ctx); err != nil {
		return err
	}
	return r.d.State.Update(func(s *state.State) error {
		s.Onboarding.AddCompleted("codex_desktop_configured")
		return nil
	})
}

func (r *realOrchestrator) ConfigureVSCode(ctx context.Context) error {
	s, err := r.d.State.Load()
	if err != nil {
		return err
	}
	if s.VSCode.Path == "" {
		det, detErr := vscode.Detect()
		if detErr != nil || !det.Installed || det.Path == "" {
			if detErr != nil {
				return fmt.Errorf("ConfigureVSCode: vscode.Path unknown — run EnsureVSCode first: %w", detErr)
			}
			return fmt.Errorf("ConfigureVSCode: vscode.Path unknown — run EnsureVSCode first")
		}
		if err := r.d.State.Update(func(s *state.State) error {
			s.VSCode.Path = det.Path
			s.VSCode.Version = det.Version
			s.VSCode.InstalledByUs = false
			s.Onboarding.AddCompleted("vscode_installed")
			return nil
		}); err != nil {
			return err
		}
		s.VSCode.Path = det.Path
		s.VSCode.Version = det.Version
	}
	// Copy or download codex.exe to r.d.CodexAbsPath if missing.
	if r.d.CodexAbsPath != "" {
		if _, statErr := os.Stat(r.d.CodexAbsPath); os.IsNotExist(statErr) {
			if err := os.MkdirAll(filepath.Dir(r.d.CodexAbsPath), 0o755); err != nil {
				return fmt.Errorf("mkdir codex bin dir: %w", err)
			}
			if r.d.BundledCodexPath != "" {
				if _, err := os.Stat(r.d.BundledCodexPath); err == nil {
					if err := copyFile(r.d.BundledCodexPath, r.d.CodexAbsPath); err != nil {
						return fmt.Errorf("copy bundled codex: %w", err)
					}
					_ = os.Remove(r.d.CodexAbsPath + ".part")
					_ = os.Remove(r.d.CodexAbsPath + ".meta")
				}
			}
		}
		if _, statErr := os.Stat(r.d.CodexAbsPath); os.IsNotExist(statErr) {
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
	if err := r.configureSharedCodex(ctx); err != nil {
		return err
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

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
func (r *realOrchestrator) Finalize(ctx context.Context) error {
	if r.d.LauncherExePath != "" {
		if err := shortcut.EnsureDesktopShortcut(shortcut.DesktopInput{
			Name:      branding.DisplayName,
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
			MenuLabel:         branding.ContextMenuLabel,
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
func (r *realOrchestrator) LaunchAndShutdown(ctx context.Context) error {
	s, err := r.d.State.Load()
	if err != nil {
		return err
	}
	mode := state.NormalizeFrontendMode(s.FrontendMode)
	if mode == state.FrontendModeCodexDesktop {
		open := r.d.CodexDesktopOpen
		if open == nil {
			open = func(u string) error { return codexdesktop.Launch(ctx, "", nil) }
		}
		if err := open(codexdesktop.ThreadURL("")); err != nil {
			return fmt.Errorf("launch Codex Desktop: %w", err)
		}
		if r.d.Shutdown != nil {
			r.d.Shutdown()
		}
		return nil
	}
	if s.VSCode.Path == "" {
		return fmt.Errorf("VS Code path unknown; was vscode_install completed?")
	}
	cmd := exec.Command(s.VSCode.Path, vscode.LaunchArgs(r.d.VSCodeUserDataDir, r.d.VSCodeExtDir)...)
	if r.d.Secrets != nil {
		if apiKey, err := r.d.Secrets.Get("modelserver_api_key"); err == nil {
			cmd.Env = vscode.UpsertEnv(os.Environ(), "OPENAI_API_KEY", apiKey)
		}
	}
	if r.d.TokenRefresherExePath != "" {
		_ = tokenrefresh.StartDaemon(r.d.TokenRefresherExePath)
	}
	// Don't inherit our stdio — we're about to shut down.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch VS Code: %w", err)
	}
	// VS Code is now running independently. Trigger launcher shutdown
	// (async — see launcher main.go: 500ms delay so the HTTP response
	// to /api/launch-vscode can flush before srv.Shutdown closes things).
	if r.d.Shutdown != nil {
		r.d.Shutdown()
	}
	return nil
}

func (r *realOrchestrator) Abort(ctx context.Context) error {
	r.cleanupMS()
	return nil
}
