package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/branding"
	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/codexruntime"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/loom"
	"github.com/agentserver/agentserver-pkg/internal/modelaccess"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/opencode"
	"github.com/agentserver/agentserver-pkg/internal/opencodedesktop"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/shortcut"
	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
	"github.com/agentserver/agentserver-pkg/internal/vscode"
)

type Deps struct {
	State   *state.Store
	Secrets secrets.Store
	MS      *modelserver.Client
	// MSProxy is the modelserver client pointed at the proxy gateway
	// (code.ai.cs.ac.cn). The OAuth profile endpoint (PR #63) lives there,
	// not on the admin API host MS targets (codeapi.cs.ac.cn).
	MSProxy                           *modelserver.Client
	AS                                *agentserver.Client
	MSOAuth                           oauth.AuthCodeConfig // PKCE (modelserver path)
	ASOAuth                           oauth.Config         // device code (agentserver path)
	CodexConfigPath                   string
	LocalProxyTokenPath               string
	CodexDesktopGlobalStatePath       string
	CodexDesktopComputerUseConfigPath string
	VSCodeUserDataDir                 string
	VSCodeExtDir                      string
	EmbeddedVSIXPath                  string
	CodexAbsPath                      string
	BundledCodexPath                  string
	CodexManifestPath                 string
	CodexRuntimeEnsure                func(context.Context, string, string, string) error
	LoomDriverPath                    string
	LoomConfigPath                    string
	MachineFile                       string
	// CodexDesktopCodexPath is the codex CLI used by loom's internal planner.
	// Minimal VS Code mode uses CodexAbsPath when this is empty.
	CodexDesktopCodexPath string

	CodexDesktopEnsure func(context.Context) (codexdesktop.Detected, error)
	CodexDesktopOpen   func(string) error
	OpenCodeConfigPath string

	OpenCodeDesktopEnsure func(context.Context) (opencodedesktop.Detected, error)
	OpenCodeDesktopLaunch func(context.Context, opencodedesktop.LaunchOptions) error

	// OpenBrowser is invoked by the orchestrator after starting the PKCE
	// listener. Optional in tests.
	OpenBrowser func(string)

	// Shutdown is invoked by LaunchAndShutdown after VS Code is spawned.
	// The launcher uses this to gracefully close its HTTP server so the
	// process can exit cleanly. Optional in tests.
	Shutdown func()

	// StartCompletedConsole starts the persistent post-onboarding console.
	// The launcher wires this to launch launcher.exe after state is complete,
	// so tray and dashboard behavior use the same path as a later double-click.
	StartCompletedConsole func(context.Context) error

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

var ensureOpenCodeDesktopInstalled = opencodedesktop.EnsureInstalled

func NewRealOrchestrator(d Deps) Orchestrator {
	return &realOrchestrator{d: d}
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
	return SanitizeState(s), nil
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
		if strings.TrimSpace(tok.RefreshToken) == "" {
			r.cleanupMS()
			return modelserver.APIKey{}, fmt.Errorf("modelserver login did not return refresh_token; please reconnect and allow offline access")
		}
		r.msToken = tok

		// Keep the PKCE access_token in local secrets. Codex uses a stable local
		// proxy credential; the proxy injects the latest access token per request.
		key := modelserver.APIKey{
			Secret:    tok.AccessToken,
			KeySuffix: lastN(tok.AccessToken, 4),
		}
		if _, err := tokenrefresh.StoreToken(r.d.Secrets, tok, time.Now().UTC(), ""); err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		// Fetch project_id from the OAuth profile endpoint (modelserver PR #63).
		// Works for both opaque and JWT access tokens — no local decoding.
		// Best-effort: if the gateway is briefly unreachable or the endpoint
		// hasn't been deployed yet, persist an empty project_id rather than
		// failing the entire login (callers tolerate "" — the proxy uses the
		// token directly, not project_id; quota fetching surfaces its own
		// error message in the UI when project_id is empty).
		var projectID string
		if r.d.MSProxy != nil {
			profile, perr := r.d.MSProxy.Profile(ctx, tok.AccessToken)
			if perr != nil {
				log.Printf("modelserver: profile fetch failed (continuing without project_id): %v", perr)
			} else {
				projectID = profile.Project.UUID
			}
		}
		if r.d.TokenRefresherExePath != "" {
			_ = tokenrefresh.StartDaemon(r.d.TokenRefresherExePath)
		}
		if err := r.d.State.Update(func(s *state.State) error {
			s.Modelserver.ProjectID = projectID
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

	if r.d.AS == nil {
		return agentserver.WorkspaceAPIKey{}, fmt.Errorf("agentserver client required")
	}
	reg, err := r.d.AS.RegisterAgent(ctx, tok.AccessToken, "星池指挥官", "custom")
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, fmt.Errorf("register agentserver agent: %w", err)
	}
	if reg.ProxyToken == "" {
		return agentserver.WorkspaceAPIKey{}, fmt.Errorf("register agentserver agent: missing sandbox proxy token")
	}
	if reg.SandboxID == "" || reg.TunnelToken == "" || reg.ShortID == "" {
		return agentserver.WorkspaceAPIKey{}, fmt.Errorf("register agentserver agent: incomplete registration")
	}

	workspace := agentserver.Workspace{ID: reg.WorkspaceID}
	if ws, ok := agentserver.WorkspaceFromToken(tok.AccessToken); ok {
		if workspace.ID == "" {
			workspace.ID = ws.ID
		}
		if workspace.Name == "" {
			workspace.Name = ws.Name
		}
	}
	if identity, err := r.d.AS.Whoami(ctx, reg.ProxyToken); err == nil && identity.Workspace.ID != "" {
		workspace = identity.Workspace
	}
	if workspace.ID == "" {
		return agentserver.WorkspaceAPIKey{}, fmt.Errorf("resolve agentserver workspace: empty workspace id")
	}

	key := agentserver.WorkspaceAPIKey{
		Secret:      reg.ProxyToken,
		KeySuffix:   lastN(reg.ProxyToken, 4),
		WorkspaceID: workspace.ID,
		Name:        "星池指挥官",
	}
	if err := r.d.Secrets.Set("agentserver_ws_api_key", key.Secret); err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	if reg.TunnelToken != "" {
		if err := r.d.Secrets.Set("agentserver_tunnel_token", reg.TunnelToken); err != nil {
			return agentserver.WorkspaceAPIKey{}, err
		}
	}
	if err := r.d.State.Update(func(s *state.State) error {
		s.Agentserver.SandboxID = reg.SandboxID
		s.Agentserver.ShortID = reg.ShortID
		s.Agentserver.WorkspaceID = workspace.ID
		s.Agentserver.WorkspaceName = workspace.Name
		s.Agentserver.WorkspaceAPIKeySuffix = key.KeySuffix
		s.Onboarding.AddCompleted("agentserver_login")
		return nil
	}); err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	if err := r.configureLoomDriver(); err != nil {
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
	if mode == state.FrontendModeOpenCodeDesktop {
		return r.EnsureOpenCodeDesktop(ctx, ch)
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
	if mode == state.FrontendModeOpenCodeDesktop {
		return r.ConfigureOpenCodeDesktop(ctx)
	}
	return r.ConfigureCodexDesktop(ctx)
}

func (r *realOrchestrator) EnsureOpenCodeDesktop(ctx context.Context, ch chan<- ProgressEvent) error {
	ensure := r.d.OpenCodeDesktopEnsure
	if ensure == nil {
		ensure = func(ctx context.Context) (opencodedesktop.Detected, error) {
			return ensureOpenCodeDesktopInstalled(ctx, opencodedesktop.Options{})
		}
	}
	if ch != nil {
		ch <- ProgressEvent{Stage: "checking", Msg: "正在检查 OpenCode Desktop..."}
	}
	det, err := ensure(ctx)
	if err != nil {
		return err
	}
	if ch != nil {
		ch <- ProgressEvent{Stage: "verified", Msg: "已检测到 OpenCode Desktop"}
	}
	return r.d.State.Update(func(s *state.State) error {
		s.OpenCodeDesktop.Installed = true
		s.OpenCodeDesktop.Path = det.Path
		s.OpenCodeDesktop.Version = det.Version
		s.OpenCodeDesktop.InstalledByUs = true
		s.Onboarding.AddCompleted("opencode_desktop_installed")
		return nil
	})
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
		"vscode-store-bootstrapper"+plan.FileExt)
	if ch != nil {
		ch <- ProgressEvent{Stage: "download", Msg: "正在下载 VS Code 微软商店引导器..."}
	}
	if err := vscode.DownloadBootstrapper(ctx, plan.BootstrapperURL, cache, nil); err != nil {
		return fmt.Errorf("download VS Code Microsoft Store bootstrapper: %w", err)
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

func (r *realOrchestrator) configureSharedCodex(ctx context.Context) error {
	_ = ctx
	localProxyToken, err := r.localProxyBearerToken()
	if err != nil {
		return err
	}
	if err := codex.UpdateConfig(r.d.CodexConfigPath, codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, localProxyToken)); err != nil {
		return err
	}
	_ = env.PersistUserEnv(codex.LocalProxyAPIKeyEnv, localProxyToken)
	_ = os.Setenv(codex.LocalProxyAPIKeyEnv, localProxyToken)
	if r.d.TokenRefresherExePath != "" {
		_ = tokenrefresh.StartDaemon(r.d.TokenRefresherExePath)
	}
	if err := r.configureLoomDriver(); err != nil {
		return err
	}
	return nil
}

func (r *realOrchestrator) localProxyBearerToken() (string, error) {
	if r.d.LocalProxyTokenPath == "" {
		return codex.LegacyLocalProxyAPIKeyValue, nil
	}
	return modelaccess.EnsureLocalProxyToken(r.d.LocalProxyTokenPath)
}

func (r *realOrchestrator) configureLoomDriver() error {
	if r.d.LoomDriverPath == "" || r.d.LoomConfigPath == "" {
		return nil
	}
	if _, err := os.Stat(r.d.LoomDriverPath); err != nil {
		return fmt.Errorf("configure loom driver: missing driver-agent.exe at %s: %w", r.d.LoomDriverPath, err)
	}
	st, err := r.d.State.Load()
	if err != nil {
		return err
	}
	if r.d.Secrets == nil {
		return fmt.Errorf("configure loom driver: secrets store required")
	}
	proxyToken, err := r.d.Secrets.Get("agentserver_ws_api_key")
	if err != nil {
		return fmt.Errorf("configure loom driver: agentserver registration missing proxy token: %w", err)
	}
	tunnelToken, err := r.d.Secrets.Get("agentserver_tunnel_token")
	if err != nil {
		return fmt.Errorf("configure loom driver: agentserver registration missing tunnel token: %w", err)
	}
	serverURL := st.Agentserver.BaseURL
	if serverURL == "" {
		serverURL = "https://agent.cs.ac.cn"
	}
	codexBin, codexExtraArgs, err := r.loomDriverCodexInvocation(st)
	if err != nil {
		return err
	}
	serverName := "driver-" + lastN(st.InstallID, 8)
	if st.Agentserver.ShortID != "" {
		serverName = "driver-" + st.Agentserver.ShortID
	}
	displayName := r.loomDriverDisplayName(st)
	if err := loom.WriteDriverConfig(r.d.LoomConfigPath, loom.DriverConfig{
		ServerURL:      serverURL,
		ServerName:     serverName,
		SandboxID:      st.Agentserver.SandboxID,
		TunnelToken:    tunnelToken,
		ProxyToken:     proxyToken,
		WorkspaceID:    st.Agentserver.WorkspaceID,
		WorkspaceName:  st.Agentserver.WorkspaceName,
		ShortID:        st.Agentserver.ShortID,
		DisplayName:    displayName,
		Description:    displayName + " 本地协作驱动。",
		CodexBin:       codexBin,
		CodexExtraArgs: codexExtraArgs,
		CodexHome:      filepath.Dir(r.d.CodexConfigPath),
		CodexWorkDir: func() string {
			if home, err := os.UserHomeDir(); err == nil {
				return home
			}
			return ""
		}(),
	}); err != nil {
		return fmt.Errorf("configure loom driver: %w", err)
	}
	if err := loom.InstallDriverSupport(loom.DriverSupportInput{
		UserHome:                    codexUserHome(r.d.CodexConfigPath),
		SkillsArchivePath:           filepath.Join(filepath.Dir(r.d.LoomDriverPath), "driver-skills.tar.gz"),
		SuperpowerSkillsArchivePath: filepath.Join(filepath.Dir(r.d.LoomDriverPath), "driver-superpower-skills.tar.gz"),
		CodexPromptsArchivePath:     filepath.Join(filepath.Dir(r.d.LoomDriverPath), "driver-codex-prompts.tar.gz"),
	}); err != nil {
		return fmt.Errorf("install loom driver support: %w", err)
	}
	enabled := true
	if err := codex.UpdateMCPServer(r.d.CodexConfigPath, "driver", codex.MCPServer{
		Command:           r.d.LoomDriverPath,
		Args:              []string{"serve-mcp", "--config", r.d.LoomConfigPath},
		StartupTimeoutSec: 30,
		ToolTimeoutSec:    120,
		Enabled:           &enabled,
	}); err != nil {
		return fmt.Errorf("configure codex mcp driver: %w", err)
	}
	return nil
}

func codexUserHome(configPath string) string {
	codexDir := filepath.Dir(configPath)
	if filepath.Base(codexDir) == ".codex" {
		return filepath.Dir(codexDir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

func (r *realOrchestrator) loomDriverDisplayName(st *state.State) string {
	if strings.TrimSpace(r.d.MachineFile) != "" {
		if m, err := slave.NewMachineStore(r.d.MachineFile).Ensure(driverComputerNameFallback(st)); err == nil {
			return m.ComputerName
		}
	}
	return driverComputerNameFallback(st)
}

func driverComputerNameFallback(st *state.State) string {
	if st != nil && strings.TrimSpace(st.InstallID) != "" {
		return "local-computer-" + lastN(st.InstallID, 10)
	}
	if name := strings.TrimSpace(os.Getenv("COMPUTERNAME")); name != "" {
		return name
	}
	if hostname, err := os.Hostname(); err == nil {
		if name := strings.TrimSpace(hostname); name != "" {
			return name
		}
	}
	return "local-computer"
}

func (r *realOrchestrator) loomDriverCodexBin(st *state.State) string {
	if st != nil && state.NormalizeFrontendMode(st.FrontendMode) == state.FrontendModeCodexDesktop && r.d.CodexDesktopCodexPath != "" {
		return r.d.CodexDesktopCodexPath
	}
	if r.d.CodexAbsPath != "" {
		return r.d.CodexAbsPath
	}
	return "codex"
}

func (r *realOrchestrator) loomDriverCodexInvocation(st *state.State) (string, []string, error) {
	wrapperPath := ""
	if r.d.LoomDriverPath != "" {
		wrapperPath = filepath.Join(filepath.Dir(r.d.LoomDriverPath), "codex-debug-wrapper.exe")
	}
	realCodexBin := r.loomDriverCodexBin(st)
	bin, extraArgs := loom.CodexDebugWrapperInvocation(wrapperPath, realCodexBin)
	if bin == wrapperPath {
		if err := loom.WriteCodexDebugWrapperConfig(wrapperPath, realCodexBin); err != nil {
			return "", nil, err
		}
	}
	return bin, extraArgs, nil
}

func (r *realOrchestrator) ConfigureCodexDesktop(ctx context.Context) error {
	if err := r.configureSharedCodex(ctx); err != nil {
		return err
	}
	if err := r.configureCodexDesktopLocale(); err != nil {
		return err
	}
	return r.d.State.Update(func(s *state.State) error {
		s.Onboarding.AddCompleted("codex_desktop_configured")
		return nil
	})
}

func (r *realOrchestrator) ConfigureOpenCodeDesktop(ctx context.Context) error {
	if err := r.configureSharedCodex(ctx); err != nil {
		return err
	}
	configPath := r.d.OpenCodeConfigPath
	if configPath == "" {
		return fmt.Errorf("ConfigureOpenCodeDesktop: OpenCodeConfigPath required")
	}
	localProxyToken, err := r.localProxyBearerToken()
	if err != nil {
		return err
	}
	if err := opencode.UpdateConfig(configPath, opencode.Settings{
		BaseURL: modelproxy.DefaultBaseURL,
		Model:   "gpt-5.5",
	}); err != nil {
		return err
	}
	_ = env.PersistUserEnv(codex.LocalProxyAPIKeyEnv, localProxyToken)
	_ = env.PersistUserEnv(opencode.LocalProxyAPIKeyEnv, localProxyToken)
	_ = os.Setenv(codex.LocalProxyAPIKeyEnv, localProxyToken)
	_ = os.Setenv(opencode.LocalProxyAPIKeyEnv, localProxyToken)
	return r.d.State.Update(func(s *state.State) error {
		s.Onboarding.AddCompleted("opencode_desktop_configured")
		return nil
	})
}

func (r *realOrchestrator) configureCodexDesktopLocale() error {
	globalPath := r.d.CodexDesktopGlobalStatePath
	computerUsePath := r.d.CodexDesktopComputerUseConfigPath
	if globalPath == "" || computerUsePath == "" {
		if r.d.CodexConfigPath == "" {
			return nil
		}
		codexDir := filepath.Dir(r.d.CodexConfigPath)
		if globalPath == "" {
			globalPath = codexdesktop.LocaleGlobalStatePath(codexDir)
		}
		if computerUsePath == "" {
			computerUsePath = codexdesktop.LocaleComputerUsePath(codexDir)
		}
	}
	return codexdesktop.ConfigureLocale(globalPath, computerUsePath, codexdesktop.DefaultLocale)
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
			ensure := r.d.CodexRuntimeEnsure
			if ensure == nil {
				ensure = func(ctx context.Context, manifestPath, destRoot, cacheDir string) error {
					_, err := codexruntime.Ensure(ctx, codexruntime.Options{
						ManifestPath: manifestPath,
						DestRoot:     destRoot,
						CacheDir:     cacheDir,
					})
					return err
				}
			}
			destRoot := filepath.Dir(filepath.Dir(r.d.CodexAbsPath))
			cacheDir := filepath.Join(destRoot, "cache", "codex")
			if err := ensure(ctx, r.d.CodexManifestPath, destRoot, cacheDir); err != nil {
				return fmt.Errorf("ensure codex runtime: %w", err)
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
			RegistryKeySuffix: "AgentserverApp",
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
	if err := r.d.State.Update(func(s *state.State) error {
		s.Onboarding.AddCompleted("shortcuts_created")
		s.Onboarding.Status = state.StatusComplete
		return nil
	}); err != nil {
		return err
	}
	if r.d.StartCompletedConsole != nil {
		if err := r.d.StartCompletedConsole(ctx); err != nil {
			return err
		}
	}
	if r.d.Shutdown != nil {
		r.d.Shutdown()
	}
	return nil
}
func (r *realOrchestrator) LaunchAndShutdown(ctx context.Context) error {
	s, err := r.d.State.Load()
	if err != nil {
		return err
	}
	mode := state.NormalizeFrontendMode(s.FrontendMode)
	if mode == state.FrontendModeOpenCodeDesktop {
		if r.d.OpenCodeConfigPath == "" {
			return fmt.Errorf("LaunchAndShutdown: OpenCodeConfigPath required")
		}
		if err := r.configureSharedCodex(ctx); err != nil {
			return err
		}
		if err := opencode.UpdateConfig(r.d.OpenCodeConfigPath, opencode.Settings{
			BaseURL: modelproxy.DefaultBaseURL,
			Model:   "gpt-5.5",
		}); err != nil {
			return err
		}
		localProxyToken, err := r.localProxyBearerToken()
		if err != nil {
			return err
		}
		launch := r.d.OpenCodeDesktopLaunch
		if launch == nil {
			launch = func(ctx context.Context, opts opencodedesktop.LaunchOptions) error {
				return opencodedesktop.Launch(ctx, opts)
			}
		}
		if err := launch(ctx, opencodedesktop.LaunchOptions{Detected: opencodedesktop.Detected{
			Installed: s.OpenCodeDesktop.Installed,
			Path:      s.OpenCodeDesktop.Path,
			Version:   s.OpenCodeDesktop.Version,
		}, Config: opencodedesktop.ConfigEnv{
			Path:      r.d.OpenCodeConfigPath,
			APIKeyEnv: opencode.LocalProxyAPIKeyEnv,
			APIKey:    localProxyToken,
		}}); err != nil {
			return fmt.Errorf("launch OpenCode Desktop: %w", err)
		}
		if r.d.Shutdown != nil {
			r.d.Shutdown()
		}
		return nil
	}
	if mode == state.FrontendModeCodexDesktop {
		if err := r.configureCodexDesktopLocale(); err != nil {
			return err
		}
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
	localProxyToken, err := r.localProxyBearerToken()
	if err != nil {
		return err
	}
	cmd := exec.Command(s.VSCode.Path, vscode.LaunchArgs(r.d.VSCodeUserDataDir, r.d.VSCodeExtDir)...)
	cmd.Env = vscode.UpsertEnv(os.Environ(), codex.LocalProxyAPIKeyEnv, localProxyToken)
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
