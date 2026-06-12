package codexdesktop

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrWingetNotFound = errors.New("winget not found")
var ErrUnsupportedPlatform = errors.New("codex desktop winget install unsupported on this platform")

func WingetInstallArgs() []string {
	return []string{
		"install",
		"Codex",
		"-s",
		"msstore",
		"--accept-source-agreements",
		"--accept-package-agreements",
	}
}

func RequireWinget() error {
	if _, err := exec.LookPath("winget"); err != nil {
		return ErrWingetNotFound
	}
	return nil
}

func ClassifyWingetError(err error, output string) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(output)
	trimmed := strings.TrimSpace(output)
	if errors.Is(err, ErrUnsupportedPlatform) {
		return fmt.Errorf("codex desktop winget install is only supported on Windows: %w", err)
	}
	if errors.Is(err, ErrWingetNotFound) {
		return fmt.Errorf("未找到 winget；请安装或更新 Windows App Installer / Windows Package Manager 后重试: %w", err)
	}
	if isWingetSourceUnavailable(lower) {
		return fmt.Errorf("microsoft store source 不可用；请检查 Store 源、网络或企业策略。winget 输出: %s", trimmed)
	}
	if strings.Contains(lower, "network") || strings.Contains(lower, "internet") || strings.Contains(lower, "connection") {
		return fmt.Errorf("网络不可用，无法通过 winget 安装 Codex Desktop。winget 输出: %s", trimmed)
	}
	return fmt.Errorf("winget install Codex -s msstore 失败: %w。输出: %s", err, trimmed)
}

func isWingetSourceUnavailable(lower string) bool {
	compact := strings.Join(strings.Fields(lower), " ")
	if strings.Contains(compact, "no sources") || strings.Contains(compact, "no available sources") {
		return true
	}
	for _, phrase := range []string{
		"msstore source was not found",
		"source msstore was not found",
		"source 'msstore' was not found",
		"source \"msstore\" was not found",
		"failed to open source",
		"failed to open the source",
		"failed when opening source",
		"failed when opening the source",
		"could not open source",
		"could not open the source",
		"unable to open source",
		"unable to open the source",
	} {
		if strings.Contains(compact, phrase) {
			return true
		}
	}
	return false
}
