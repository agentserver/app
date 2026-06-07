package codexdesktop

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrWingetNotFound = errors.New("winget not found")

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
	if errors.Is(err, ErrWingetNotFound) {
		return errors.New("未找到 winget；请安装或更新 Windows App Installer / Windows Package Manager 后重试")
	}
	if strings.Contains(lower, "source") || strings.Contains(lower, "msstore") {
		return fmt.Errorf("Microsoft Store source 不可用；请检查 Store 源、网络或企业策略。winget 输出: %s", strings.TrimSpace(output))
	}
	if strings.Contains(lower, "network") || strings.Contains(lower, "internet") || strings.Contains(lower, "connection") {
		return fmt.Errorf("网络不可用，无法通过 winget 安装 Codex Desktop。winget 输出: %s", strings.TrimSpace(output))
	}
	return fmt.Errorf("winget install Codex -s msstore 失败: %w。输出: %s", err, strings.TrimSpace(output))
}
