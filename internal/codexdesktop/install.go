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
	// Install performs the platform-specific Codex Desktop install and takes
	// precedence over RunWinget / the default platform dispatcher when set.
	Install func(context.Context) error
}

func EnsureInstalled(ctx context.Context, opts Options) (Detected, error) {
	detect := opts.Detect
	if detect == nil {
		detect = Detect
	}
	det, err := detect()
	if err == nil {
		if det.Installed {
			return det, nil
		}
	} else if !errors.Is(err, ErrNotFound) {
		return Detected{}, fmt.Errorf("detect Codex Desktop: %w", err)
	}

	// Prefer an explicitly injected Install (full override). Otherwise, if a
	// RunWinget is supplied, run winget with error classification (this keeps
	// existing test/override diagnostics intact). Finally fall back to the
	// per-platform installDesktopPlatform (windows=winget, darwin=dmg, other=
	// ErrUnsupportedPlatform).
	if opts.Install != nil {
		if err := opts.Install(ctx); err != nil {
			return Detected{}, err
		}
	} else if opts.RunWinget != nil {
		out, runErr := opts.RunWinget(ctx, WingetInstallArgs())
		if runErr != nil {
			return Detected{}, ClassifyWingetError(runErr, out)
		}
		det, err = detect()
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return Detected{}, fmt.Errorf("codex desktop 安装后仍未检测到；winget 输出: %s: %w", out, err)
			}
			return Detected{}, fmt.Errorf("codex desktop 安装后检测失败: %w；winget 输出: %s", err, out)
		}
		if !det.Installed {
			return Detected{}, fmt.Errorf("codex desktop 安装后仍未检测到；winget 输出: %s: %w", out, ErrNotFound)
		}
		return det, nil
	} else {
		if err := installDesktopPlatform(ctx); err != nil {
			return Detected{}, err
		}
	}

	det, err = detect()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Detected{}, fmt.Errorf("codex desktop 安装后仍未检测到: %w", err)
		}
		return Detected{}, fmt.Errorf("codex desktop 安装后检测失败: %w", err)
	}
	if !det.Installed {
		return Detected{}, fmt.Errorf("codex desktop 安装后仍未检测到: %w", ErrNotFound)
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
