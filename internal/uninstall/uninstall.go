package uninstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/agentserver/agentserver-pkg/internal/branding"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/shortcut"
	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

type Options struct {
	Paths   paths.Paths
	Secrets secrets.Store
	Out     io.Writer
	AppDir  string

	DeleteEnv            func(string) error
	RemoveAll            func(string) error
	StopProcess          func(context.Context, int, string) error
	StopInstallProcesses func(context.Context, string, []string) error
}

func Run(opts Options) error {
	var err error
	if opts.Paths.InstallRoot == "" {
		opts.Paths, err = paths.Default()
		if err != nil {
			return err
		}
	}
	if opts.Secrets == nil {
		opts.Secrets = secrets.New(opts.Paths.SecretsFile)
	}
	if opts.DeleteEnv == nil {
		opts.DeleteEnv = env.DeleteUserEnv
	}
	if opts.RemoveAll == nil {
		opts.RemoveAll = os.RemoveAll
	}
	if opts.StopProcess == nil {
		opts.StopProcess = slave.StopProcess
	}
	if opts.StopInstallProcesses == nil {
		opts.StopInstallProcesses = stopInstallProcesses
	}

	var errs []error
	if err := stopRunningProcesses(context.Background(), opts); err != nil {
		errs = append(errs, err)
	}
	removeShortcut := func(name string) {
		if err := shortcut.UninstallAll(shortcut.ContextMenuInput{
			RegistryKeySuffix: "AgentserverVscode",
		}, name); err != nil {
			errs = append(errs, err)
		}
	}
	removeShortcut(branding.DisplayName)
	removeShortcut(branding.ProductID)

	for _, key := range []string{
		tokenrefresh.AccessTokenKey,
		tokenrefresh.RefreshTokenKey,
		tokenrefresh.AccessTokenExpiresAtKey,
		"agentserver_ws_api_key",
		"agentserver_tunnel_token",
	} {
		if err := opts.Secrets.Delete(key); err != nil {
			errs = append(errs, fmt.Errorf("delete secret %s: %w", key, err))
		}
	}

	if err := opts.DeleteEnv(tokenrefresh.OpenAIAPIKeyEnv); err != nil {
		errs = append(errs, err)
	}
	if opts.Paths.InstallRoot != "" {
		if err := opts.RemoveAll(opts.Paths.InstallRoot); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", opts.Paths.InstallRoot, err))
		}
	}
	if opts.Paths.LocalAppDataRoot != "" {
		if err := opts.RemoveAll(opts.Paths.LocalAppDataRoot); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", opts.Paths.LocalAppDataRoot, err))
		}
	}
	if err := removeUninstallRegistry(branding.ProductID); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func stopRunningProcesses(ctx context.Context, opts Options) error {
	var errs []error
	slaveExe := appExePath(opts.AppDir, "slave-agent.exe")
	if opts.Paths.SlavesFile != "" && opts.Paths.SlavesDir != "" && slaveExe != "" {
		reg := slave.NewRegistry(opts.Paths.SlavesFile, opts.Paths.SlavesDir)
		slaves, err := reg.List()
		if err != nil {
			errs = append(errs, fmt.Errorf("read local slaves: %w", err))
		}
		for _, sl := range slaves {
			if sl.PID == 0 {
				continue
			}
			if err := opts.StopProcess(ctx, sl.PID, slaveExe); err != nil && !errors.Is(err, slave.ErrProcessNotRunning) {
				errs = append(errs, fmt.Errorf("stop local slave %s pid %d: %w", sl.ID, sl.PID, err))
			}
		}
	}
	if opts.AppDir != "" {
		if err := opts.StopInstallProcesses(ctx, opts.AppDir, installProcessNames()); err != nil {
			errs = append(errs, fmt.Errorf("stop install processes: %w", err))
		}
	}
	return errors.Join(errs...)
}

func installProcessNames() []string {
	return []string{
		"launcher.exe",
		"onboarding-server.exe",
		"open-folder.exe",
		"slave-agent.exe",
		"driver-agent.exe",
		"token-refresher.exe",
	}
}

func appExePath(appDir, name string) string {
	if appDir == "" {
		return ""
	}
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		name += ".exe"
	}
	return filepath.Join(appDir, name)
}
