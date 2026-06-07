// launcher is the user-facing entrypoint (desktop shortcut). It either:
//   - if first run: spawn onboarding-server + open browser
//   - else: exec VS Code with our user-data-dir
//
// Folder argument (right-click handler) is delegated to cmd/open-folder.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/browser"
	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/launchprep"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
	"github.com/agentserver/agentserver-pkg/internal/ui"
	"github.com/agentserver/agentserver-pkg/internal/vscode"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("launcher: %v", err)
	}
}

func run() error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.InstallRoot, 0o755); err != nil {
		return err
	}
	exe, _ := os.Executable()
	installDir := osDir(exe)
	store := state.NewStore(p.StateFile)
	if err := installmode.SyncStore(store, joinExe(installDir, "install-mode.json")); err != nil {
		return err
	}
	s, err := store.Load()
	if err != nil {
		return err
	}

	if s.Onboarding.Status == state.StatusComplete {
		return launchCompletedFrontend(context.Background(), s, p, secrets.New(p.SecretsFile),
			joinExe(installDir, "token-refresher.exe"), joinExe(installDir, "agentserver-vscode.vsix"), nil)
	}

	// Otherwise: serve onboarding UI.
	return serveOnboarding(p, store)
}

func serveOnboarding(p paths.Paths, store *state.Store) error {
	sec := secrets.New(p.SecretsFile)

	// modelserver: authorization_code + PKCE, public client registered by
	// ops on 2026-06-03 (see docs/ops/modelserver-oauth-client-registration.md).
	// 8 fixed callback ports because ops registered explicit redirect_uris
	// rather than wildcard 127.0.0.1.
	msOAuth := modelserver.OAuthConfig()
	// agentserver: device-code flow at /api/oauth2/device/auth, proxied
	// to Hydra. The CLI client `agentserver-agent-cli` is pre-registered
	// by the Helm chart with grant=device_code, public (no secret),
	// scopes=openid profile agent:register.
	asOAuth := oauth.Config{
		Endpoint:  "https://agent.cs.ac.cn",
		AuthPath:  "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token",
		ClientID:  "agentserver-agent-cli",
		Scope:     "openid profile agent:register",
	}

	installDir, err := os.Executable()
	if err != nil {
		return err
	}
	installDir = osDir(installDir)

	deps := ui.Deps{
		State:   store,
		Secrets: sec,
		// codeapi.cs.ac.cn is the admin API host (returns JSON). code.cs.ac.cn
		// is the dashboard SPA — any path there returns the SPA index HTML,
		// which causes the modelserver client's JSON decoder to fail with
		// "invalid character '<' looking for beginning of value". This is the
		// SAME host PKCE uses (msOAuth.Endpoint above).
		MS:                    modelserver.New("https://codeapi.cs.ac.cn"),
		AS:                    agentserver.New("https://agent.cs.ac.cn"),
		MSOAuth:               msOAuth,
		ASOAuth:               asOAuth,
		OpenBrowser:           func(url string) { _ = browser.Open(url) },
		CodexConfigPath:       p.CodexConfigFile,
		VSCodeUserDataDir:     p.VSCodeUserDataDir,
		VSCodeExtDir:          p.VSCodeExtDir,
		EmbeddedVSIXPath:      joinExe(installDir, "agentserver-vscode.vsix"),
		CodexAbsPath:          p.CodexExePath,
		BundledCodexPath:      joinExe(installDir, "codex.exe"),
		LauncherExePath:       joinExe(installDir, "launcher.exe"),
		OpenFolderExePath:     joinExe(installDir, "open-folder.exe"),
		TokenRefresherExePath: joinExe(installDir, "token-refresher.exe"),
		IconPath:              preferredIconPath(installDir),
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{}

	// Inject the shutdown callback into Deps so LaunchAndShutdown can
	// trigger graceful server close after VS Code is spawned. Delayed
	// 500ms so the in-flight POST /api/launch-vscode response can flush.
	deps.Shutdown = func() {
		go func() {
			time.Sleep(500 * time.Millisecond)
			_ = srv.Shutdown(context.Background())
		}()
	}

	orch := ui.NewRealOrchestrator(deps)
	srv.Handler = ui.NewServer(orch)

	url := fmt.Sprintf("http://%s/", ln.Addr())
	fmt.Println("onboarding URL:", url)
	go func() { _ = browser.Open(url) }()

	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil // clean shutdown via LaunchAndShutdown
	}
	return err
}

func launchCompletedInstall(ctx context.Context, codeExe string, p paths.Paths, sec secrets.Store, tokenRefresherExe string, embeddedVSIXPath string) error {
	if err := launchprep.PrepareVSCode(ctx, launchprep.Input{
		CodeExe:          codeExe,
		Paths:            p,
		EmbeddedVSIXPath: embeddedVSIXPath,
	}); err != nil {
		return err
	}
	return execVSCode(codeExe, p, "", sec, tokenRefresherExe)
}

func launchCompletedFrontend(ctx context.Context, s *state.State, p paths.Paths, sec secrets.Store, tokenRefresherExe string, embeddedVSIXPath string, codexOpen codexdesktop.Opener) error {
	if state.NormalizeFrontendMode(s.FrontendMode) == state.FrontendModeMinimalVSCode {
		if s.VSCode.Path == "" {
			return fmt.Errorf("VS Code path unknown; rerun onboarding")
		}
		return launchCompletedInstall(ctx, s.VSCode.Path, p, sec, tokenRefresherExe, embeddedVSIXPath)
	}
	return launchCompletedCodexDesktop(ctx, p, sec, tokenRefresherExe, codexOpen)
}

func launchCompletedCodexDesktop(ctx context.Context, p paths.Paths, sec secrets.Store, tokenRefresherExe string, opener codexdesktop.Opener) error {
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverSettings()); err != nil {
		return err
	}
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}
	return codexdesktop.Launch(ctx, "", opener)
}

func execVSCode(codeExe string, p paths.Paths, folder string, sec secrets.Store, tokenRefresherExe string) error {
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverSettings()); err != nil {
		return err
	}
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}
	args := vscode.LaunchArgs(p.VSCodeUserDataDir, p.VSCodeExtDir, folder)
	cmd := exec.Command(codeExe, args...)
	if sec != nil {
		if apiKey, err := sec.Get("modelserver_api_key"); err == nil {
			cmd.Env = vscode.UpsertEnv(os.Environ(), "OPENAI_API_KEY", apiKey)
		}
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

// osDir returns the directory of an executable path.
func osDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}

func joinExe(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + string(os.PathSeparator) + name
}

func preferredIconPath(installDir string) string {
	matches, err := filepath.Glob(filepath.Join(installDir, "icon-*.ico"))
	if err == nil && len(matches) > 0 {
		sort.Strings(matches)
		return matches[len(matches)-1]
	}
	return joinExe(installDir, "icon.ico")
}
