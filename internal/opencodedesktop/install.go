package opencodedesktop

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

type Options struct {
	Detect             func() (Detected, error)
	RunInstaller       func(context.Context) error
	LocalInstallerPath string
}

func EnsureInstalled(ctx context.Context, opts Options) (Detected, error) {
	detect := opts.Detect
	if detect == nil {
		detect = Detect
	}
	det, err := detect()
	if err == nil && det.Installed {
		return det, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Detected{}, fmt.Errorf("detect OpenCode Desktop: %w", err)
	}
	runInstaller := opts.RunInstaller
	if runInstaller == nil {
		runInstaller = func(ctx context.Context) error {
			return runLocalInstaller(ctx, opts.LocalInstallerPath)
		}
	}
	if err := runInstaller(ctx); err != nil {
		return Detected{}, fmt.Errorf("install OpenCode Desktop: %w", err)
	}
	det, err = detect()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Detected{}, fmt.Errorf("opencode desktop 安装后仍未检测到: %w", err)
		}
		return Detected{}, fmt.Errorf("opencode desktop 安装后检测失败: %w", err)
	}
	if !det.Installed {
		return Detected{}, fmt.Errorf("opencode desktop 安装后仍未检测到: %w", ErrNotFound)
	}
	return det, nil
}

func runLocalInstaller(ctx context.Context, path string) error {
	if runtime.GOOS != "windows" {
		return ErrUnsupportedPlatform
	}
	if path == "" {
		return errors.New("local OpenCode Desktop installer path required")
	}
	cmd := exec.CommandContext(ctx, path)
	process.HideWindow(cmd)
	return cmd.Run()
}
