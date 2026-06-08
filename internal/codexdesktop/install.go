package codexdesktop

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/agentserver/agentserver-pkg/internal/process"
)

type Options struct {
	Detect    func() (Detected, error)
	RunWinget func(context.Context, []string) (string, error)
}

func EnsureInstalled(ctx context.Context, opts Options) (Detected, error) {
	detect := opts.Detect
	if detect == nil {
		detect = Detect
	}
	run := opts.RunWinget
	if run == nil {
		run = runWinget
	}
	det, err := detect()
	if err == nil {
		if det.Installed {
			return det, nil
		}
	} else if !errors.Is(err, ErrNotFound) {
		return Detected{}, fmt.Errorf("detect Codex Desktop: %w", err)
	}
	out, err := run(ctx, WingetInstallArgs())
	if err != nil {
		return Detected{}, ClassifyWingetError(err, out)
	}
	det, err = detect()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Detected{}, fmt.Errorf("Codex Desktop 安装后仍未检测到；winget 输出: %s: %w", out, err)
		}
		return Detected{}, fmt.Errorf("Codex Desktop 安装后检测失败: %w；winget 输出: %s", err, out)
	}
	if !det.Installed {
		return Detected{}, fmt.Errorf("Codex Desktop 安装后仍未检测到；winget 输出: %s: %w", out, ErrNotFound)
	}
	return det, nil
}

func runWinget(ctx context.Context, args []string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", ErrUnsupportedPlatform
	}
	if err := RequireWinget(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "winget", args...)
	process.HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
