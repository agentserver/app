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
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/appversion"
	"github.com/agentserver/agentserver-pkg/internal/browser"
	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/folderpicker"
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/launchprep"
	"github.com/agentserver/agentserver-pkg/internal/loom"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/process"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
	"github.com/agentserver/agentserver-pkg/internal/tray"
	"github.com/agentserver/agentserver-pkg/internal/ui"
	"github.com/agentserver/agentserver-pkg/internal/updater"
	"github.com/agentserver/agentserver-pkg/internal/vscode"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("launcher: %v", err)
	}
}

func run() error {
	opts, err := parseLauncherOptions(os.Args[1:])
	if err != nil {
		return err
	}
	return runWithOptions(context.Background(), opts)
}

type launcherOptions struct {
	Background   bool
	OpenPage     bool
	OpenFrontend bool
}

func parseLauncherOptions(args []string) (launcherOptions, error) {
	opts := launcherOptions{OpenPage: true, OpenFrontend: true}
	for _, arg := range args {
		switch arg {
		case "--background":
			opts.Background = true
			opts.OpenPage = false
			opts.OpenFrontend = false
		default:
			log.Printf("launcher: ignoring unknown option %s", arg)
		}
	}
	return opts, nil
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
	installModePath, err := installmode.Path()
	if err != nil {
		return err
	}
	if err := installmode.SyncStoreIfPresent(store, installModePath); err != nil {
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
	Post        func(context.Context, string, string) error
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
			if err := post(ctx, base+"/api/console/open-frontend", info.Token); err != nil {
				errs = append(errs, fmt.Errorf("open completed frontend: %w", err))
			}
		}
		return errors.Join(errs...)
	}
	return errNoRunningConsole
}

func postConsole(ctx context.Context, url, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set(ui.ConsoleInstanceTokenHeader, token)
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
	slaveManager, err := newCompletedSlaveManager(in)
	if err != nil {
		return err
	}
	updates := newCompletedUpdater(in.Paths)
	if err := restorePendingSlaveRestarts(ctx, in.Paths.PendingSlaveRestartsFile, appversion.Version, func(ctx context.Context, id string) error {
		_, err := slaveManager.Restart(ctx, id)
		return err
	}); err != nil {
		log.Printf("launcher: restore pending slave restarts: %v", err)
	}
	consoleCtx, stopConsole := context.WithCancel(ctx)
	defer stopConsole()
	scheduleAutomaticUpdateCheck(consoleCtx, updates, 30*time.Second)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{}
	ctrl := console.NewController(console.Deps{
		State:                    in.State,
		Secrets:                  sec,
		MS:                       modelserver.New("https://codeapi.cs.ac.cn"),
		MSProxy:                  modelserver.New("https://code.ai.cs.ac.cn"),
		AS:                       agentserver.New("https://agent.cs.ac.cn"),
		Slaves:                   slaveManager,
		Updates:                  updates,
		PendingSlaveRestartsPath: in.Paths.PendingSlaveRestartsFile,
		ModelserverWebBaseURL:    "https://code.cs.ac.cn",
		RefreshModelserverToken: func(ctx context.Context) error {
			_, err := tokenrefresh.RefreshOnce(ctx, tokenrefresh.Options{
				Secrets: sec,
				OAuth:   modelserver.OAuthConfig(),
			})
			return err
		},
		OpenURL:      openBrowser,
		SelectFolder: folderpicker.Select,
		OpenFrontend: func(ctx context.Context) error {
			current, err := in.State.Load()
			if err != nil {
				return err
			}
			return launchCompletedFrontend(ctx, current, in.Paths, sec,
				in.InstallDir,
				joinExe(in.InstallDir, process.ExeName("token-refresher")),
				joinExe(in.InstallDir, "agentserver-app.vsix"),
				nil)
		},
		Quit: func() {
			go srv.Shutdown(context.Background())
		},
	})
	token, err := console.NewInstanceToken()
	if err != nil {
		ln.Close()
		return err
	}
	srv.Handler = ui.NewServerWithConsoleToken(newCompletedConsoleOrchestrator(completedOrchestratorInput{
		State:                 in.State,
		Secrets:               sec,
		Paths:                 in.Paths,
		InstallDir:            in.InstallDir,
		MSOAuth:               modelserver.OAuthConfig(),
		OpenBrowser:           func(url string) { _ = openBrowser(url) },
		TokenRefresherExePath: joinExe(in.InstallDir, process.ExeName("token-refresher")),
	}), ctrl, token)

	port := ln.Addr().(*net.TCPAddr).Port
	info := console.InstanceInfo{Port: port, PID: os.Getpid(), Token: token}
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
	trayCtx, stopTray := context.WithCancel(ctx)
	trayApp := tray.New(preferredIconPath(in.InstallDir))
	trayActions := tray.Actions{
		OpenDashboard: func() {
			runAsyncLauncherAction("open console page", func() error {
				return openBrowser(base + "/")
			})
		},
		OpenFrontend: func() {
			runAsyncLauncherAction("open completed frontend", func() error {
				return ctrl.OpenFrontend(context.Background())
			})
		},
		OpenSubscription: func() {
			runAsyncLauncherAction("open subscription page", func() error {
				return ctrl.OpenSubscription(context.Background())
			})
		},
		Quit: func() {
			runAsyncLauncherAction("quit completed console", func() error {
				return ctrl.Quit(context.Background())
			})
		},
	}
	trayDone := runTrayApp(trayCtx, trayApp, trayActions)
	defer func() {
		if !stopTrayAndWait(stopTray, trayDone, trayShutdownTimeout) {
			log.Printf("launcher: tray cleanup did not finish within %s", trayShutdownTimeout)
		}
	}()
	go runTrayStatusLoop(trayCtx, trayApp, ctrl, console.ReminderEngine{
		Store: console.NewFileReminderStore(in.Paths.ConsoleNotificationsFile),
	})

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

func newCompletedUpdater(p paths.Paths) *updater.Service {
	return &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    updater.DefaultManifestURL,
		CacheDir:       p.UpdatesCacheDir,
		State:          updater.NewStateStore(p.UpdateStateFile),
	}
}

func restorePendingSlaveRestarts(ctx context.Context, path, currentVersion string, restart func(context.Context, string) error) error {
	pending, err := slave.ReadPendingRestarts(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cmp, err := updater.CompareVersions(currentVersion, pending.Version)
	if err != nil {
		return fmt.Errorf("compare pending slave restart version: %w", err)
	}
	if cmp < 0 {
		return nil
	}
	// Equality means the target version has just come back up after the update;
	// newer versions should also retry any still-pending slave restarts.
	return slave.RestorePendingRestarts(ctx, path, restart)
}

func scheduleAutomaticUpdateCheck(ctx context.Context, svc *updater.Service, delay time.Duration) <-chan struct{} {
	return scheduleAutomaticUpdateCheckWithTiming(ctx, svc, delay, 24*time.Hour, 30*time.Second)
}

func scheduleAutomaticUpdateCheckWithTiming(ctx context.Context, svc *updater.Service, delay, interval, timeout time.Duration) <-chan struct{} {
	return scheduleAutomaticUpdateCheckWithRetry(ctx, svc, delay, interval, time.Hour, timeout, jitterAutomaticUpdateInterval)
}

func scheduleAutomaticUpdateCheckWithRetry(ctx context.Context, svc *updater.Service, delay, interval, retryInterval, timeout time.Duration, jitter func(time.Duration) time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if svc == nil {
		close(done)
		return done
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if retryInterval <= 0 {
		retryInterval = time.Hour
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if jitter == nil {
		jitter = func(d time.Duration) time.Duration { return d }
	}
	go func() {
		defer close(done)
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}

			checkCtx, cancelCheck := context.WithTimeout(ctx, timeout)
			_, err := svc.Check(checkCtx, true)
			cancelCheck()
			nextDelay := jitter(interval)
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("launcher: automatic update check: %v", err)
				nextDelay = retryInterval
			}

			timer.Reset(nextDelay)
		}
	}()
	return done
}

func jitterAutomaticUpdateInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return interval
	}
	span := int64(float64(interval) * 0.1)
	if span <= 0 {
		return interval
	}
	return interval + time.Duration(rand.Int63n(2*span+1)-span)
}

func newCompletedSlaveManager(in completedServeInput) (*slave.Manager, error) {
	deps, err := completedSlaveManagerDeps(in)
	if err != nil {
		return nil, err
	}
	return slave.NewManager(deps), nil
}

func completedSlaveManagerDeps(in completedServeInput) (slave.ManagerDeps, error) {
	machines := slave.NewMachineStore(in.Paths.MachineFile)
	if _, err := machines.Ensure(completedComputerName()); err != nil {
		return slave.ManagerDeps{}, fmt.Errorf("ensure machine identity: %w", err)
	}
	return slave.ManagerDeps{
		Machines:  machines,
		Registry:  slave.NewRegistry(in.Paths.SlavesFile, in.Paths.SlavesDir),
		SlaveExe:  joinExe(in.InstallDir, process.ExeName("slave-agent")),
		ServerURL: "https://agent.cs.ac.cn",
		CodexBin:  in.Paths.CodexExePath,
		OpenAuthURL: func(url string) {
			_ = browser.Open(url)
		},
	}, nil
}

func completedComputerName() string {
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

const trayShutdownTimeout = 2 * time.Second

func runTrayApp(ctx context.Context, app tray.App, actions tray.Actions) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := app.Run(ctx, actions); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("launcher: tray run: %v", err)
		}
	}()
	return done
}

func stopTrayAndWait(cancel context.CancelFunc, done <-chan struct{}, timeout time.Duration) bool {
	cancel()
	if done == nil {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func runTrayStatusLoop(ctx context.Context, app tray.App, ctrl trayConsoleController, reminders console.ReminderEngine) {
	refresh := func() {
		if err := updateTrayOnce(ctx, app, ctrl, reminders); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("launcher: update tray: %v", err)
		}
	}
	refresh()

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
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

type completedOrchestratorInput struct {
	State                 *state.Store
	Secrets               secrets.Store
	Paths                 paths.Paths
	InstallDir            string
	MSOAuth               oauth.AuthCodeConfig
	ASOAuth               oauth.Config
	OpenBrowser           func(string)
	TokenRefresherExePath string
}

func newCompletedConsoleOrchestrator(in completedOrchestratorInput) ui.Orchestrator {
	asBaseURL := completedAgentserverBaseURL(in.State)
	asOAuth := in.ASOAuth
	if asOAuth.Endpoint == "" {
		asOAuth = agentserver.OAuthConfig(asBaseURL)
	}
	loomDriverPath := ""
	if in.InstallDir != "" {
		loomDriverPath = joinExe(in.InstallDir, process.ExeName("driver-agent"))
	}
	loomConfigPath := ""
	if in.Paths.UserHome != "" {
		loomConfigPath = filepath.Join(in.Paths.UserHome, ".config", "multi-agent", "driver.yaml")
	}
	return ui.NewRealOrchestrator(ui.Deps{
		State:                             in.State,
		Secrets:                           in.Secrets,
		MS:                                modelserver.New("https://codeapi.cs.ac.cn"),
		AS:                                agentserver.New(asBaseURL),
		MSOAuth:                           in.MSOAuth,
		ASOAuth:                           asOAuth,
		CodexConfigPath:                   in.Paths.CodexConfigFile,
		CodexDesktopGlobalStatePath:       in.Paths.CodexDesktopGlobalStateFile,
		CodexDesktopComputerUseConfigPath: in.Paths.CodexDesktopComputerUseConfigFile,
		CodexAbsPath:                      in.Paths.CodexExePath,
		LoomDriverPath:                    loomDriverPath,
		LoomConfigPath:                    loomConfigPath,
		OpenBrowser:                       in.OpenBrowser,
		TokenRefresherExePath:             in.TokenRefresherExePath,
	})
}

func completedAgentserverBaseURL(store *state.Store) string {
	const fallback = "https://agent.cs.ac.cn"
	if store == nil {
		return fallback
	}
	st, err := store.Load()
	if err != nil || strings.TrimSpace(st.Agentserver.BaseURL) == "" {
		return fallback
	}
	return strings.TrimSpace(st.Agentserver.BaseURL)
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

type trayConsoleController interface {
	State(context.Context) (console.State, error)
}

func updateTrayOnce(ctx context.Context, app tray.App, ctrl trayConsoleController, reminders console.ReminderEngine) error {
	st, err := ctrl.State(ctx)
	if err != nil {
		return err
	}
	app.Update(trayStateFromConsole(st))

	var errs []error
	for _, reminder := range reminders.Evaluate(st.Quotas) {
		title := "星池指挥官额度提醒"
		message := fmt.Sprintf("%s额度已用 %d%%", quotaLabel(reminder.Window), reminder.Threshold)
		if err := app.Notify(title, message); err != nil {
			errs = append(errs, fmt.Errorf("tray notify %s %d%%: %w", reminder.Window, reminder.Threshold, err))
		}
	}
	return errors.Join(errs...)
}

func trayStateFromConsole(st console.State) tray.State {
	state := tray.State{
		Tooltip:  "星池指挥官\n额度暂不可用",
		FiveHour: "5小时额度：暂不可用",
		SevenDay: "7天额度：暂不可用",
	}
	for _, q := range st.Quotas {
		line := fmt.Sprintf("%s额度：已用 %.0f%%，剩余约 %.0f%%", quotaLabel(q.Window), q.Percentage, q.RemainingPercentage)
		if q.Window == "5h" {
			state.FiveHour = line
		}
		if q.Window == "7d" {
			state.SevenDay = line
		}
	}
	state.Tooltip = "星池指挥官\n" + state.FiveHour + "\n" + state.SevenDay
	return state
}

func quotaLabel(window string) string {
	switch window {
	case "5h":
		return "5小时"
	case "7d":
		return "7天"
	default:
		return window
	}
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
	asOAuth := agentserver.OAuthConfig("https://agent.cs.ac.cn")

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
		MS:                                modelserver.New("https://codeapi.cs.ac.cn"),
		AS:                                agentserver.New("https://agent.cs.ac.cn"),
		MSOAuth:                           msOAuth,
		ASOAuth:                           asOAuth,
		OpenBrowser:                       func(url string) { _ = browser.Open(url) },
		CodexConfigPath:                   p.CodexConfigFile,
		CodexDesktopGlobalStatePath:       p.CodexDesktopGlobalStateFile,
		CodexDesktopComputerUseConfigPath: p.CodexDesktopComputerUseConfigFile,
		VSCodeUserDataDir:                 p.VSCodeUserDataDir,
		VSCodeExtDir:                      p.VSCodeExtDir,
		EmbeddedVSIXPath:                  joinExe(installDir, "agentserver-app.vsix"),
		CodexAbsPath:                      p.CodexExePath,
		BundledCodexPath:                  joinExe(installDir, process.ExeName("codex")),
		CodexManifestPath:                 joinExe(installDir, "codex-manifest.json"),
		LoomDriverPath:                    joinExe(installDir, process.ExeName("driver-agent")),
		LoomConfigPath:                    filepath.Join(p.UserHome, ".config", "multi-agent", "driver.yaml"),
		LauncherExePath:                   joinExe(installDir, process.ExeName("launcher")),
		OpenFolderExePath:                 joinExe(installDir, process.ExeName("open-folder")),
		TokenRefresherExePath:             joinExe(installDir, process.ExeName("token-refresher")),
		IconPath:                          preferredIconPath(installDir),
		StartCompletedConsole: func(ctx context.Context) error {
			return startCompletedConsole(ctx, joinExe(installDir, process.ExeName("launcher")))
		},
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

func startCompletedConsole(ctx context.Context, launcherExe string) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	cmd := exec.Command(launcherExe)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	process.HideWindow(cmd)
	return cmd.Start()
}

func launchCompletedFrontend(ctx context.Context, s *state.State, p paths.Paths, sec secrets.Store, installDir string, tokenRefresherExe string, embeddedVSIXPath string, codexOpen codexdesktop.Opener) error {
	if state.NormalizeFrontendMode(s.FrontendMode) == state.FrontendModeMinimalVSCode {
		if s.VSCode.Path == "" {
			return fmt.Errorf("VS Code path unknown; rerun onboarding")
		}
		if err := configureCompletedLoomDriver(p, s, sec, installDir); err != nil {
			return err
		}
		return launchCompletedInstall(ctx, s.VSCode.Path, p, sec, tokenRefresherExe, embeddedVSIXPath)
	}
	return launchCompletedCodexDesktop(ctx, s, p, sec, installDir, tokenRefresherExe, codexOpen)
}

func launchCompletedCodexDesktop(ctx context.Context, s *state.State, p paths.Paths, sec secrets.Store, installDir string, tokenRefresherExe string, opener codexdesktop.Opener) error {
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, codex.LegacyLocalProxyAPIKeyValue)); err != nil {
		return err
	}
	_ = env.PersistUserEnv(codex.LocalProxyAPIKeyEnv, codex.LegacyLocalProxyAPIKeyValue)
	_ = os.Setenv(codex.LocalProxyAPIKeyEnv, codex.LegacyLocalProxyAPIKeyValue)
	if err := codexdesktop.ConfigureLocale(
		p.CodexDesktopGlobalStateFile,
		p.CodexDesktopComputerUseConfigFile,
		codexdesktop.DefaultLocale,
	); err != nil {
		return err
	}
	if err := configureCompletedLoomDriver(p, s, sec, installDir); err != nil {
		return err
	}
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}
	return codexdesktop.Launch(ctx, "", opener)
}

func configureCompletedLoomDriver(p paths.Paths, s *state.State, sec secrets.Store, installDir string) error {
	if p.UserHome == "" || s == nil || sec == nil || installDir == "" {
		return nil
	}
	driverPath := joinExe(installDir, process.ExeName("driver-agent"))
	if _, err := os.Stat(driverPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat loom driver: %w", err)
	}
	loomConfigPath := filepath.Join(p.UserHome, ".config", "multi-agent", "driver.yaml")
	proxyToken := getSecretIfPresent(sec, "agentserver_ws_api_key")
	tunnelToken := getSecretIfPresent(sec, "agentserver_tunnel_token")
	if proxyToken == "" || tunnelToken == "" {
		fallbackProxyToken, fallbackTunnelToken := readExistingLoomTokens(loomConfigPath)
		if proxyToken == "" {
			proxyToken = fallbackProxyToken
		}
		if tunnelToken == "" {
			tunnelToken = fallbackTunnelToken
		}
	}
	if proxyToken == "" || tunnelToken == "" {
		return nil
	}
	serverURL := s.Agentserver.BaseURL
	if serverURL == "" {
		serverURL = "https://agent.cs.ac.cn"
	}
	serverName := "driver-local"
	if s.InstallID != "" {
		serverName = "driver-" + lastN(s.InstallID, 8)
	}
	if s.Agentserver.ShortID != "" {
		serverName = "driver-" + s.Agentserver.ShortID
	}
	codexBin := completedDriverCodexBin(p, s)
	if err := loom.WriteDriverConfig(loomConfigPath, loom.DriverConfig{
		ServerURL:     serverURL,
		ServerName:    serverName,
		SandboxID:     s.Agentserver.SandboxID,
		TunnelToken:   tunnelToken,
		ProxyToken:    proxyToken,
		WorkspaceID:   s.Agentserver.WorkspaceID,
		WorkspaceName: s.Agentserver.WorkspaceName,
		ShortID:       s.Agentserver.ShortID,
		DisplayName:   "星池指挥官",
		Description:   "星池指挥官本地协作驱动。",
		CodexBin:      codexBin,
		CodexWorkDir:  p.UserHome,
	}); err != nil {
		return fmt.Errorf("configure loom driver: %w", err)
	}
	if err := loom.InstallDriverSupport(loom.DriverSupportInput{
		UserHome:                    p.UserHome,
		SkillsArchivePath:           joinExe(installDir, "driver-skills.tar.gz"),
		SuperpowerSkillsArchivePath: joinExe(installDir, "driver-superpower-skills.tar.gz"),
		CodexPromptsArchivePath:     joinExe(installDir, "driver-codex-prompts.tar.gz"),
	}); err != nil {
		return fmt.Errorf("install loom driver support: %w", err)
	}
	if p.CodexConfigFile != "" {
		enabled := true
		if err := codex.UpdateMCPServer(p.CodexConfigFile, "driver", codex.MCPServer{
			Command:           driverPath,
			Args:              []string{"serve-mcp", "--config", loomConfigPath},
			StartupTimeoutSec: 30,
			ToolTimeoutSec:    120,
			Enabled:           &enabled,
		}); err != nil {
			return fmt.Errorf("configure codex mcp driver: %w", err)
		}
	}
	return nil
}

func completedDriverCodexBin(p paths.Paths, s *state.State) string {
	if state.NormalizeFrontendMode(s.FrontendMode) == state.FrontendModeMinimalVSCode {
		if p.CodexExePath != "" {
			return p.CodexExePath
		}
		return "codex"
	}
	// Codex Desktop owns its CLI runtime; do not point the driver at the
	// VS Code helper codex.exe staged under agentserver-app.
	return "codex"
}

func getSecretIfPresent(sec secrets.Store, key string) string {
	value, err := sec.Get(key)
	if err != nil {
		return ""
	}
	return value
}

func readExistingLoomTokens(path string) (proxyToken, tunnelToken string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := parseSimpleYAMLScalar(line)
		if !ok {
			continue
		}
		switch key {
		case "proxy_token":
			proxyToken = value
		case "tunnel_token":
			tunnelToken = value
		}
	}
	return proxyToken, tunnelToken
}

func parseSimpleYAMLScalar(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key = strings.TrimSpace(parts[0])
	raw := strings.TrimSpace(parts[1])
	if key == "" || raw == "" {
		return "", "", false
	}
	if unquoted, err := strconv.Unquote(raw); err == nil {
		return key, unquoted, true
	}
	return key, raw, true
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func execVSCode(codeExe string, p paths.Paths, folder string, sec secrets.Store, tokenRefresherExe string) error {
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, codex.LegacyLocalProxyAPIKeyValue)); err != nil {
		return err
	}
	_ = env.PersistUserEnv(codex.LocalProxyAPIKeyEnv, codex.LegacyLocalProxyAPIKeyValue)
	_ = os.Setenv(codex.LocalProxyAPIKeyEnv, codex.LegacyLocalProxyAPIKeyValue)
	if tokenRefresherExe != "" {
		_ = tokenrefresh.StartDaemon(tokenRefresherExe)
	}
	args := vscode.LaunchArgs(p.VSCodeUserDataDir, p.VSCodeExtDir, folder)
	cmd := exec.Command(codeExe, args...)
	cmd.Env = vscode.UpsertEnv(os.Environ(), codex.LocalProxyAPIKeyEnv, codex.LegacyLocalProxyAPIKeyValue)
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
	matches, err := filepath.Glob(filepath.Join(installDir, iconGlobSuffix))
	if err == nil && len(matches) > 0 {
		sort.Strings(matches)
		return matches[len(matches)-1]
	}
	return joinExe(installDir, defaultIconSuffix)
}
