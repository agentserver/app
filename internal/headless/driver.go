package headless

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/loom"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/terminalauth"
)

const (
	defaultAgentserverEndpoint = "https://agent.cs.ac.cn"
	driverProxySecretKey       = "agentserver_ws_api_key"
	driverTunnelSecretKey      = "agentserver_tunnel_token"
)

type AgentRegistrar interface {
	RegisterAgent(context.Context, string, string, string) (agentserver.AgentRegistration, error)
	Whoami(context.Context, string) (agentserver.Identity, error)
}

type DriverOptions struct {
	Paths             paths.Paths
	Package           Package
	Secrets           secrets.Store
	ComputerName      string
	AS                AgentRegistrar
	ASOAuth           oauth.Config
	RequestDeviceCode func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error)
	PollToken         func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error)
	Stdout            io.Writer
	QR                terminalauth.QRWriter
}

type DriverMCPOptions struct {
	Paths   paths.Paths
	Package Package
	Secrets secrets.Store
	WorkDir string
	Exec    func(context.Context, string, []string) error
}

func InstallDriver(ctx context.Context, opts DriverOptions) error {
	return ensureDriver(ctx, opts, false)
}

func SwitchWorkspace(ctx context.Context, opts DriverOptions) error {
	return ensureDriver(ctx, opts, true)
}

func ServeDriverMCP(ctx context.Context, opts DriverMCPOptions) error {
	if opts.Secrets == nil {
		return errors.New("driver MCP: secrets store required")
	}
	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current workdir: %w", err)
		}
		workDir = wd
	}
	st, err := state.NewStore(opts.Paths.StateFile).Load()
	if err != nil {
		return err
	}
	proxyToken, tunnelToken, err := driverSecrets(opts.Secrets)
	if err != nil {
		return err
	}
	sessionConfig := filepath.Join(opts.Paths.InstallRoot, "driver-mcp", fmt.Sprintf("driver-%d.yaml", os.Getpid()))
	if err := writeDriverConfig(sessionConfig, st, proxyToken, tunnelToken, workDir); err != nil {
		return err
	}
	run := opts.Exec
	if run == nil {
		run = execDriverMCP
	}
	return run(ctx, opts.Package.DriverAgent, []string{"serve-mcp", "--config", sessionConfig})
}

func ensureDriver(ctx context.Context, opts DriverOptions, forceLogin bool) error {
	if opts.Secrets == nil {
		return errors.New("install driver: secrets store required")
	}
	opts = defaultDriverOptions(opts)

	if forceLogin {
		if err := registerDriver(ctx, opts); err != nil {
			return err
		}
		return refreshDriverFiles(opts.Paths, opts.Package, opts.Secrets)
	}

	registered, err := driverRegistered(opts.Paths, opts.Secrets)
	if err != nil {
		return err
	}
	if !registered {
		if err := registerDriver(ctx, opts); err != nil {
			return err
		}
	} else if err := repairRegisteredDriverState(ctx, opts); err != nil {
		return err
	}
	return refreshDriverFiles(opts.Paths, opts.Package, opts.Secrets)
}

func defaultDriverOptions(opts DriverOptions) DriverOptions {
	opts.ASOAuth = normalizeASOAuth(opts.ASOAuth)
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.RequestDeviceCode == nil {
		opts.RequestDeviceCode = oauth.RequestDeviceCode
	}
	if opts.PollToken == nil {
		opts.PollToken = oauth.PollToken
	}
	if opts.AS == nil {
		opts.AS = agentserver.New(defaultASBaseURL(opts.ASOAuth))
	}
	return opts
}

func normalizeASOAuth(cfg oauth.Config) oauth.Config {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultAgentserverEndpoint
	}
	base := agentserver.OAuthConfig(endpoint)
	if cfg.AuthPath != "" {
		base.AuthPath = cfg.AuthPath
	}
	if cfg.TokenPath != "" {
		base.TokenPath = cfg.TokenPath
	}
	if cfg.ClientID != "" {
		base.ClientID = cfg.ClientID
	}
	if cfg.Scope != "" {
		base.Scope = cfg.Scope
	}
	return base
}

func defaultASBaseURL(cfg oauth.Config) string {
	if endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/"); endpoint != "" {
		return endpoint
	}
	return defaultAgentserverEndpoint
}

func registerDriver(ctx context.Context, opts DriverOptions) error {
	computerName := strings.TrimSpace(opts.ComputerName)
	if computerName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("hostname: %w", err)
		}
		computerName = hostname
	}
	ch, err := opts.RequestDeviceCode(ctx, opts.ASOAuth)
	if err != nil {
		return fmt.Errorf("request agentserver device code: %w", err)
	}
	terminalauth.PrintChallenge(opts.Stdout, "Agentserver 登录", ch, opts.QR)
	tok, err := opts.PollToken(ctx, opts.ASOAuth, ch)
	if err != nil {
		return fmt.Errorf("poll agentserver token: %w", err)
	}
	reg, err := opts.AS.RegisterAgent(ctx, tok.AccessToken, computerName+"-星池指挥官", "custom")
	if err != nil {
		return fmt.Errorf("register agentserver agent: %w", err)
	}
	if reg.ProxyToken == "" || reg.TunnelToken == "" || reg.SandboxID == "" {
		return errors.New("register agentserver agent: incomplete registration")
	}

	workspace := resolveDriverWorkspace(ctx, opts.AS, reg, tok.AccessToken)
	if workspace.ID == "" {
		return errors.New("resolve agentserver workspace: empty workspace id")
	}
	if err := opts.Secrets.Set(driverProxySecretKey, reg.ProxyToken); err != nil {
		return err
	}
	if err := opts.Secrets.Set(driverTunnelSecretKey, reg.TunnelToken); err != nil {
		return err
	}
	return state.NewStore(opts.Paths.StateFile).Update(func(s *state.State) error {
		s.Agentserver.BaseURL = defaultASBaseURL(opts.ASOAuth)
		s.Agentserver.SandboxID = reg.SandboxID
		s.Agentserver.ShortID = defaultString(reg.ShortID, reg.SandboxID)
		s.Agentserver.WorkspaceID = workspace.ID
		s.Agentserver.WorkspaceName = workspace.Name
		s.Agentserver.WorkspaceAPIKeySuffix = lastN(reg.ProxyToken, 4)
		return nil
	})
}

func resolveDriverWorkspace(ctx context.Context, as AgentRegistrar, reg agentserver.AgentRegistration, accessToken string) agentserver.Workspace {
	if as != nil {
		if identity, err := as.Whoami(ctx, reg.ProxyToken); err == nil && identity.Workspace.ID != "" {
			return identity.Workspace
		}
	}
	if ws, ok := agentserver.WorkspaceFromToken(accessToken); ok {
		return ws
	}
	return agentserver.Workspace{ID: reg.WorkspaceID}
}

func repairRegisteredDriverState(ctx context.Context, opts DriverOptions) error {
	proxyToken, _, err := driverSecrets(opts.Secrets)
	if err != nil {
		return err
	}
	var workspace agentserver.Workspace
	if opts.AS != nil {
		if identity, err := opts.AS.Whoami(ctx, proxyToken); err == nil {
			workspace = identity.Workspace
		}
	}
	return state.NewStore(opts.Paths.StateFile).Update(func(s *state.State) error {
		s.Agentserver.BaseURL = defaultASBaseURL(opts.ASOAuth)
		if s.Agentserver.ShortID == "" {
			s.Agentserver.ShortID = s.Agentserver.SandboxID
		}
		if workspace.ID != "" {
			s.Agentserver.WorkspaceID = workspace.ID
		}
		if workspace.Name != "" && workspace.ID == s.Agentserver.WorkspaceID {
			s.Agentserver.WorkspaceName = workspace.Name
		}
		s.Agentserver.WorkspaceAPIKeySuffix = lastN(proxyToken, 4)
		return nil
	})
}

func driverRegistered(p paths.Paths, sec secrets.Store) (bool, error) {
	st, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(st.Agentserver.SandboxID) == "" {
		return false, nil
	}
	if ok, err := secretPresent(sec, driverProxySecretKey); err != nil || !ok {
		return false, err
	}
	if ok, err := secretPresent(sec, driverTunnelSecretKey); err != nil || !ok {
		return false, err
	}
	return true, nil
}

func secretPresent(sec secrets.Store, key string) (bool, error) {
	value, err := sec.Get(key)
	if errors.Is(err, secrets.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(value) != "", nil
}

func refreshDriverFiles(p paths.Paths, pkg Package, sec secrets.Store) error {
	st, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		return err
	}
	proxyToken, tunnelToken, err := driverSecrets(sec)
	if err != nil {
		return err
	}
	if err := writeDriverConfig(persistentDriverConfigPath(p), st, proxyToken, tunnelToken, driverUserHome(p)); err != nil {
		return err
	}
	if err := loom.InstallDriverSupport(loom.DriverSupportInput{
		UserHome:                    driverUserHome(p),
		SkillsArchivePath:           filepath.Join(driverPackageDir(pkg), "driver-skills.tar.gz"),
		SuperpowerSkillsArchivePath: filepath.Join(driverPackageDir(pkg), "driver-superpower-skills.tar.gz"),
		CodexPromptsArchivePath:     filepath.Join(driverPackageDir(pkg), "driver-codex-prompts.tar.gz"),
	}); err != nil {
		return fmt.Errorf("install loom driver support: %w", err)
	}
	enabled := true
	if err := codex.UpdateMCPServer(p.CodexConfigFile, "driver", codex.MCPServer{
		Command:           pkg.AgentserverExe,
		Args:              []string{"serve-driver-mcp"},
		StartupTimeoutSec: 30,
		ToolTimeoutSec:    120,
		Enabled:           &enabled,
	}); err != nil {
		return fmt.Errorf("configure codex mcp driver: %w", err)
	}
	return nil
}

func driverSecrets(sec secrets.Store) (string, string, error) {
	proxyToken, err := sec.Get(driverProxySecretKey)
	if err != nil {
		return "", "", fmt.Errorf("agentserver registration missing proxy token: %w", err)
	}
	tunnelToken, err := sec.Get(driverTunnelSecretKey)
	if err != nil {
		return "", "", fmt.Errorf("agentserver registration missing tunnel token: %w", err)
	}
	return proxyToken, tunnelToken, nil
}

func writeDriverConfig(path string, st *state.State, proxyToken, tunnelToken, workDir string) error {
	if st == nil {
		return errors.New("driver state required")
	}
	if err := loom.WriteDriverConfig(path, loom.DriverConfig{
		ServerURL:     defaultString(st.Agentserver.BaseURL, defaultAgentserverEndpoint),
		ServerName:    driverServerName(st.Agentserver),
		SandboxID:     st.Agentserver.SandboxID,
		TunnelToken:   tunnelToken,
		ProxyToken:    proxyToken,
		WorkspaceID:   st.Agentserver.WorkspaceID,
		WorkspaceName: st.Agentserver.WorkspaceName,
		ShortID:       defaultString(st.Agentserver.ShortID, st.Agentserver.SandboxID),
		DisplayName:   "星池指挥官",
		Description:   "星池指挥官本地协作驱动。",
		CodexBin:      "codex",
		CodexWorkDir:  workDir,
	}); err != nil {
		return fmt.Errorf("configure loom driver: %w", err)
	}
	return nil
}

func execDriverMCP(ctx context.Context, exe string, args []string) error {
	cmd := osexec.CommandContext(ctx, exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("driver-agent exited: %w", err)
	}
	return nil
}

func persistentDriverConfigPath(p paths.Paths) string {
	return filepath.Join(driverUserHome(p), ".config", "multi-agent", "driver.yaml")
}

func driverUserHome(p paths.Paths) string {
	if strings.TrimSpace(p.UserHome) != "" {
		return p.UserHome
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

func driverPackageDir(pkg Package) string {
	if strings.TrimSpace(pkg.PackageDir) != "" {
		return pkg.PackageDir
	}
	if strings.TrimSpace(pkg.DriverAgent) != "" {
		return filepath.Dir(pkg.DriverAgent)
	}
	return filepath.Dir(pkg.AgentserverExe)
}

func driverServerName(st state.AgentserverState) string {
	if st.ShortID != "" {
		return "driver-" + st.ShortID
	}
	if st.SandboxID != "" {
		return "driver-" + st.SandboxID
	}
	return "driver-local"
}

func defaultString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func lastN(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
