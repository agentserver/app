package codexdesktop

import (
	"context"
	"fmt"
	"os/exec"
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
	if det, err := detect(); err == nil && det.Installed {
		return det, nil
	}
	out, err := run(ctx, WingetInstallArgs())
	if err != nil {
		return Detected{}, ClassifyWingetError(err, out)
	}
	det, err := detect()
	if err != nil || !det.Installed {
		return Detected{}, fmt.Errorf("Codex Desktop 安装后仍未检测到；winget 输出: %s", out)
	}
	return det, nil
}

func runWinget(ctx context.Context, args []string) (string, error) {
	if err := RequireWinget(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "winget", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
