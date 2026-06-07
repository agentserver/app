// launcher is the user-facing entrypoint (desktop shortcut). It either:
//   - if first run: spawn onboarding-server + open browser
//   - else: exec VS Code with our user-data-dir
//
// Folder argument (right-click handler) is delegated to cmd/open-folder.
package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/agentserver/agentserver-pkg/internal/console"
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
	return runWithOptions(context.Background(), parseLauncherOptions(os.Args[1:]))
}

type launcherOptions struct {
	Background   bool
	OpenPage     bool
	OpenFrontend bool
}

func parseLauncherOptions(args []string) launcherOptions {
	opts := launcherOptions{OpenPage: true, OpenFrontend: true}
	for _, arg := range args {
		if arg == "--background" {
			opts.Background = true
			opts.OpenPage = false
			opts.OpenFrontend = false
		}
	}
	return opts
}

func runWithOptions(ctx context.Context, opts launcherOptions) error {
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
	if err := installmode.SyncStoreIfPresent(store, installmode.PathForExecutable(exe)); err != nil {
		return err
	}
	s, err := store.Load()
	if err != nil {
		return err
	}

	if s.Onboarding.Status == state.StatusComplete {
		err := runCompletedConsole(ctx, completedConsoleDeps{
			Options:     opts,
			PortFile:    p.ConsolePortFile,
			OpenBrowser: browser.Open,
			Post:        postConsole,
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, errNoRunningConsole) {
			return err
		}
		return serveCompletedConsole(ctx, completedServeInput{
			Paths:      p,
			State:      store,
			Secrets:    secrets.New(p.SecretsFile),
			InstallDir: installDir,
			Options:    opts,
		})
	}

	// Otherwise: serve onboarding UI.
	return serveOnboarding(p, store)
}

type completedConsoleDeps struct {
	Options     launcherOptions
	PortFile    string
	Discover    func(context.Context, string) (console.InstanceInfo, bool)
	OpenBrowser func(string) error
	Post        func(context.Context, string) error
}

var errNoRunningConsole = errors.New("no running console")

func runCompletedConsole(ctx context.Context, d completedConsoleDeps) error {
	discover := d.Discover
	if discover == nil {
		discover = console.DiscoverInstance
	}
	post := d.Post
	if post == nil {
		post = postConsole
	}
	if info, ok := discover(ctx, d.PortFile); ok {
		base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)
		var errs []error
		if d.Options.OpenPage && d.OpenBrowser != nil {
			if err := d.OpenBrowser(base + "/"); err != nil {
				errs = append(errs, fmt.Errorf("open console page: %w", err))
			}
		}
		if d.Options.OpenFrontend {
			if err := post(ctx, base+"/api/console/open-frontend"); err != nil {
				errs = append(errs, fmt.Errorf("open completed frontend: %w", err))
			}
		}
		return errors.Join(errs...)
	}
	return errNoRunningConsole
}

func postConsole(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("console POST %s: status %d", url, resp.StatusCode)
	}
	return nil
}

type completedServeInput struct {
	Paths       paths.Paths
	State       *state.Store
	Secrets     secrets.Store
	InstallDir  string
	Options     launcherOptions
	OpenBrowser func(string) error
}

func serveCompletedConsole(ctx context.Context, in completedServeInput) error {
	sec := in.Secrets
	if sec == nil {
		sec = secrets.New(in.Paths.SecretsFile)
	}
	openBrowser := in.OpenBrowser
	if openBrowser == nil {
		openBrowser = browser.Open
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{}
	ctrl := console.NewController(console.Deps{
		State:                 in.State,
		Secrets:               sec,
		MS:                    modelserver.New("https://codeapi.cs.ac.cn"),
		AS:                    agentserver.New("https://agent.cs.ac.cn"),
		ModelserverWebBaseURL: "https://code.cs.ac.cn",
		OpenURL:               openBrowser,
		OpenFrontend: func(ctx context.Context) error {
			current, err := in.State.Load()
			if err != nil {
				return err
			}
			return launchCompletedFrontend(ctx, current, in.Paths, sec,
				joinExe(in.InstallDir, "token-refresher.exe"),
				joinExe(in.InstallDir, "agentserver-vscode.vsix"),
				nil)
		},
		Quit: func() {
			go srv.Shutdown(context.Background())
		},
	})
	srv.Handler = ui.NewServerWithConsole(newCompletedStateOrchestrator(in.State), ctrl)

	port := ln.Addr().(*net.TCPAddr).Port
	info := console.InstanceInfo{Port: port, PID: os.Getpid()}
	if err := console.WriteInstanceInfo(in.Paths.ConsolePortFile, info); err != nil {
		ln.Close()
		return err
	}
	defer func() {
		if err := removeConsolePortFileIfMatches(in.Paths.ConsolePortFile, info); err != nil {
			log.Printf("launcher: cleanup console port file: %v", err)
		}
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if in.Options.OpenPage {
		runAsyncLauncherAction("open console page", func() error {
			return openBrowser(base + "/")
		})
	}
	if in.Options.OpenFrontend {
		runAsyncLauncherAction("open completed frontend", func() error {
			return ctrl.OpenFrontend(ctx)
		})
	}

	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func runAsyncLauncherAction(name string, fn func() error) {
	go func() {
		if err := fn(); err != nil {
			log.Printf("launcher: %s: %v", name, err)
		}
	}()
}

func removeConsolePortFileIfMatches(path string, expected console.InstanceInfo) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var current console.InstanceInfo
	if err := json.Unmarshal(b, &current); err != nil {
		return nil
	}
	if current.Port != expected.Port || current.PID != expected.PID {
		return nil
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else {
		return err
	}
}

type completedStateOrchestrator struct {
	ui.Orchestrator
	store *state.Store
}

func newCompletedStateOrchestrator(store *state.Store) ui.Orchestrator {
	return completedStateOrchestrator{
		Orchestrator: ui.NewNoopOrchestrator(),
		store:        store,
	}
}

func (o completedStateOrchestrator) State(ctx context.Context) (ui.SanitizedState, error) {
	if o.store == nil {
		return o.Orchestrator.State(ctx)
	}
	s, err := o.store.Load()
	if err != nil {
		return ui.SanitizedState{}, err
	}
	return ui.SanitizeState(s), nil
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
