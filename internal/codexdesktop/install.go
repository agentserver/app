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
		if validationErr := validateDetected(det); validationErr != nil {
			return Detected{}, fmt.Errorf("validate %s detection before install: %w", ShortDisplayName, validationErr)
		}
		if det.Installed && det.Status == StatusReady {
			return det, nil
		}
		return Detected{}, fmt.Errorf("detect %s returned non-ready status %q without an error", ShortDisplayName, det.Status)
	} else if errors.Is(err, ErrSchemeMissing) || errors.Is(err, ErrSchemeTargetInvalid) {
		return Detected{}, repairRequiredError(err)
	} else if !errors.Is(err, ErrNotFound) {
		return Detected{}, fmt.Errorf("detect %s: %w", ShortDisplayName, err)
	}
	out, err := run(ctx, WingetInstallArgs())
	if err != nil {
		return Detected{}, ClassifyWingetError(err, out)
	}
	det, err = detect()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Detected{}, fmt.Errorf("%s安装后仍未检测到；winget 输出: %s: %w", LongDisplayName, out, err)
		}
		if errors.Is(err, ErrSchemeMissing) || errors.Is(err, ErrSchemeTargetInvalid) {
			return Detected{}, fmt.Errorf("%w；winget 输出: %s", repairRequiredError(err), out)
		}
		return Detected{}, fmt.Errorf("%s安装后检测失败: %w；winget 输出: %s", LongDisplayName, err, out)
	}
	if validationErr := validateDetected(det); validationErr != nil {
		return Detected{}, fmt.Errorf("validate %s detection after install: %w", ShortDisplayName, validationErr)
	}
	if !det.Installed || det.Status == StatusNotInstalled {
		return Detected{}, fmt.Errorf("%s安装后仍未检测到；winget 输出: %s: %w", LongDisplayName, out, ErrNotFound)
	}
	if det.Status != StatusReady {
		return Detected{}, fmt.Errorf("%s安装后状态为 %q，未达到 ready；winget 输出: %s", LongDisplayName, det.Status, out)
	}
	return det, nil
}

func repairRequiredError(err error) error {
	summary := "codex:// 协议已注册但处理器无效或不可信"
	if errors.Is(err, ErrSchemeMissing) {
		summary = "应用已安装但 codex:// 协议缺失"
	}
	return newSafeError(
		fmt.Sprintf("%s：%s。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall", LongDisplayName, summary),
		err,
	)
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
