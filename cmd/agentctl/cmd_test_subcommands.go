// Test subcommands for P13.4 remote verification. These let us exercise
// each phase of the onboarding flow individually without needing real
// modelserver/agentserver OAuth credentials.
//
// File name intentionally is NOT *_test.go so it's part of the
// production build, not the test binary.

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/download"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/vscode"
)

// runTestInstallVSCode triggers EnsureVSCode's install path. Idempotent.
func runTestInstallVSCode() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	det, _ := vscode.Detect()
	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	if det.Installed {
		fmt.Printf("VS Code already installed: %s (version %s)\n", det.Path, det.Version)
		// Persist detected install into state so downstream test-configure
		// doesn't fail with "VS Code path unknown".
		store := state.NewStore(p.StateFile)
		if err := store.Update(func(s *state.State) error {
			recordTestVSCodeInstall(s, p, det, false)
			return nil
		}); err != nil {
			die(err)
		}
		return
	}
	plan := vscode.PlanInstall()
	cache := filepath.Join(p.CacheDir, "vscode-"+vscode.LockedVersion+plan.FileExt)
	if err := os.MkdirAll(p.CacheDir, 0o755); err != nil {
		die(err)
	}
	fmt.Printf("Downloading VS Code %s from %s ...\n", vscode.LockedVersion, plan.URL)
	progress := make(chan download.ProgressEvent, 16)
	done := make(chan struct{})
	go func() {
		var last time.Time
		for ev := range progress {
			if time.Since(last) < 2*time.Second {
				continue
			}
			last = time.Now()
			fmt.Printf("  %s\n", ev.String())
		}
		close(done)
	}()
	if err := download.DownloadResumable(ctx, plan.URL, cache, plan.SHA256, progress); err != nil {
		die(fmt.Errorf("download: %w", err))
	}
	close(progress)
	<-done
	fmt.Println("Download done, running installer...")
	det2, err := vscode.InstallAndDetect(ctx, cache, plan, vscode.SilentInstall, vscode.Detect)
	if err != nil {
		die(fmt.Errorf("install: %w", err))
	}
	store := state.NewStore(p.StateFile)
	if err := store.Update(func(s *state.State) error {
		recordTestVSCodeInstall(s, p, det2, true)
		return nil
	}); err != nil {
		die(err)
	}
	fmt.Printf("VS Code installed at %s (version %s)\n", det2.Path, det2.Version)
}

func recordTestVSCodeInstall(s *state.State, p paths.Paths, det vscode.Detected, installedByUs bool) {
	s.FrontendMode = state.FrontendModeMinimalVSCode
	s.VSCode.Path = det.Path
	s.VSCode.Version = det.Version
	s.VSCode.InstalledByUs = installedByUs
	s.VSCode.UserDataDir = p.VSCodeUserDataDir
	s.VSCode.ExtensionsDir = p.VSCodeExtDir
	s.Onboarding.AddCompleted("vscode_installed")
}

// runTestDownloadCodex fetches codex.exe to the configured bin path. Idempotent.
func runTestDownloadCodex() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	if _, err := os.Stat(p.CodexExePath); err == nil {
		fmt.Printf("codex.exe already at %s\n", p.CodexExePath)
		return
	}
	if err := os.MkdirAll(filepath.Dir(p.CodexExePath), 0o755); err != nil {
		die(err)
	}
	url := "https://github.com/openai/codex/releases/download/rust-v0.136.0/" +
		"codex-x86_64-pc-windows-msvc.exe"
	fmt.Printf("Downloading codex.exe from %s ...\n", url)
	progress := make(chan download.ProgressEvent, 16)
	done := make(chan struct{})
	go func() {
		var last time.Time
		for ev := range progress {
			if time.Since(last) < 2*time.Second {
				continue
			}
			last = time.Now()
			fmt.Printf("  %s\n", ev.String())
		}
		close(done)
	}()
	if err := download.DownloadResumable(ctx, url, p.CodexExePath, "", progress); err != nil {
		die(fmt.Errorf("download codex: %w", err))
	}
	close(progress)
	<-done
	info, _ := os.Stat(p.CodexExePath)
	fmt.Printf("codex.exe downloaded to %s (%d bytes)\n", p.CodexExePath, info.Size())
}

// runTestConfigure writes settings.json, config.toml, setx, and installs extensions.
// Uses a dummy API key so OPENAI_API_KEY gets set to something visible.
func runTestConfigure() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	s, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		die(err)
	}
	if s.VSCode.Path == "" {
		die(fmt.Errorf("VS Code path unknown — run 'test-install-vscode' first"))
	}
	settingsPath := filepath.Join(p.VSCodeUserDataDir, "User", "settings.json")
	if err := vscode.WriteSettings(settingsPath, vscode.SettingsInput{
		CodexAbsPath: p.CodexExePath,
	}); err != nil {
		die(err)
	}
	fmt.Printf("wrote settings.json: %s\n", settingsPath)

	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverSettings()); err != nil {
		die(err)
	}
	fmt.Printf("wrote codex config: %s\n", p.CodexConfigFile)

	if err := env.PersistUserEnv("OPENAI_API_KEY", "ms-dummy-test-key"); err != nil {
		die(err)
	}
	fmt.Println("setx OPENAI_API_KEY=ms-dummy-test-key (HKCU\\Environment)")

	// .vsix sits next to the running agentctl.exe
	exeDir, _ := os.Executable()
	exeDir = filepath.Dir(exeDir)
	vsixPath := filepath.Join(exeDir, "agentserver-vscode.vsix")
	fmt.Printf("Installing extensions (this can take ~30s each) ...\n")
	if err := vscode.InstallExtensions(ctx, vscode.Installer{
		CodeExe:       s.VSCode.Path,
		UserDataDir:   p.VSCodeUserDataDir,
		ExtensionsDir: p.VSCodeExtDir,
		Extensions: []string{
			"MS-CEINTL.vscode-language-pack-zh-hans",
			vsixPath,
		},
	}); err != nil {
		die(err)
	}
	store := state.NewStore(p.StateFile)
	if err := store.Update(func(s *state.State) error {
		recordTestVSCodeConfigure(s)
		return nil
	}); err != nil {
		die(err)
	}
	fmt.Println("configure complete")
}

func recordTestVSCodeConfigure(s *state.State) {
	s.FrontendMode = state.FrontendModeMinimalVSCode
	s.Onboarding.AddCompleted("vscode_configured")
}

func runTestInstallCodexDesktop() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	det, err := codexdesktop.EnsureInstalled(ctx, codexdesktop.Options{})
	if err != nil {
		die(err)
	}
	store := state.NewStore(p.StateFile)
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		s.CodexDesktop.Installed = true
		s.CodexDesktop.Version = det.Version
		s.CodexDesktop.InstalledByUs = true
		s.Onboarding.AddCompleted("codex_desktop_installed")
		return nil
	}); err != nil {
		die(err)
	}
	fmt.Printf("Codex Desktop installed (version %s)\n", det.Version)
}

func runTestConfigureCodexDesktop() {
	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverSettings()); err != nil {
		die(err)
	}
	sec := secrets.New(p.SecretsFile)
	if err := sec.Set("modelserver_api_key", "ms-dummy-test-key"); err != nil {
		die(err)
	}
	if err := env.PersistUserEnv("OPENAI_API_KEY", "ms-dummy-test-key"); err != nil {
		die(err)
	}
	store := state.NewStore(p.StateFile)
	if err := store.Update(func(s *state.State) error {
		s.FrontendMode = state.FrontendModeCodexDesktop
		s.Onboarding.AddCompleted("codex_desktop_configured")
		return nil
	}); err != nil {
		die(err)
	}
	fmt.Printf("wrote codex config: %s\n", p.CodexConfigFile)
}

// runTestOpenFolder mirrors what cmd/open-folder does, but as a test entry.
func runTestOpenFolder(args []string) {
	if len(args) != 1 {
		die(fmt.Errorf("usage: agentctl test-open-folder <path>"))
	}
	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	s, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		die(err)
	}
	msg, err := openTestFolder(context.Background(), s, p, args[0], nil, nil)
	if err != nil {
		die(err)
	}
	fmt.Println(msg)
}

func openTestFolder(ctx context.Context, s *state.State, p paths.Paths, folder string, opener codexdesktop.Opener, runVSCode func(string, []string) (int, error)) (string, error) {
	if state.NormalizeFrontendMode(s.FrontendMode) == state.FrontendModeCodexDesktop {
		if err := codex.UpdateConfig(p.CodexConfigFile, codex.ModelserverSettings()); err != nil {
			return "", err
		}
		if err := codexdesktop.Launch(ctx, folder, opener); err != nil {
			return "", err
		}
		return fmt.Sprintf("opened %s with Codex Desktop", folder), nil
	}
	if s.VSCode.Path == "" {
		return "", fmt.Errorf("VS Code path unknown")
	}
	if runVSCode == nil {
		runVSCode = startTestVSCode
	}
	pid, err := runVSCode(s.VSCode.Path, vscode.LaunchArgs(p.VSCodeUserDataDir, p.VSCodeExtDir, folder))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("opened %s with VS Code (pid %d)", folder, pid), nil
}

func startTestVSCode(codeExe string, args []string) (int, error) {
	cmd := exec.Command(codeExe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}

// runTestMarkComplete writes onboarding.status = complete so that the launcher
// takes the "already configured" branch and execs VS Code directly.
func runTestMarkComplete() {
	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	store := state.NewStore(p.StateFile)
	if err := store.Update(func(s *state.State) error {
		mode := state.NormalizeFrontendMode(s.FrontendMode)
		if mode == state.FrontendModeMinimalVSCode {
			for _, st := range []string{"modelserver_login", "agentserver_login", "vscode_installed", "vscode_configured", "shortcuts_created"} {
				s.Onboarding.AddCompleted(st)
			}
		} else {
			for _, st := range []string{"modelserver_login", "agentserver_login", "codex_desktop_installed", "codex_desktop_configured", "shortcuts_created"} {
				s.Onboarding.AddCompleted(st)
			}
		}
		s.Onboarding.Status = state.StatusComplete
		return nil
	}); err != nil {
		die(err)
	}
	fmt.Println("state marked complete")
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
