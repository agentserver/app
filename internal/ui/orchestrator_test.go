package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

const retiredCodexStoreProductID = "9NT1" + "R1C2HH7"

// TestOrchestratorImplementsInterface ensures the production type satisfies
// the interface; if a method signature drifts later, this test will fail.
func TestOrchestratorImplementsInterface(t *testing.T) {
	var _ Orchestrator = (*realOrchestrator)(nil)
}

func TestNoopOrchestratorFinalize(t *testing.T) {
	o := &noopOrchestrator{}
	if err := o.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSafeFrontendLaunchErrorCodexDiagnosticsAreSpecificAndSafe(t *testing.T) {
	tests := []struct {
		name     string
		sentinel error
		want     string
		required []string
	}{
		{
			name:     "not found",
			sentinel: codexdesktop.ErrNotFound,
			want:     fmt.Sprintf("未检测到 %s。请从 Microsoft Store 安装产品 ID %s 后重试。", codexdesktop.LongDisplayName, codexdesktop.CodexStoreProductID),
			required: []string{codexdesktop.LongDisplayName, "Microsoft Store", codexdesktop.CodexStoreProductID},
		},
		{
			name:     "scheme missing",
			sentinel: codexdesktop.ErrSchemeMissing,
			want:     "ChatGPT / Codex 的 codex:// 协议缺失。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。",
			required: []string{"codex:// 协议缺失"},
		},
		{
			name:     "scheme target invalid",
			sentinel: codexdesktop.ErrSchemeTargetInvalid,
			want:     "ChatGPT / Codex 的 codex:// 协议已注册，但处理程序无效或不受信任。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。",
			required: []string{"codex:// 协议已注册", "处理程序无效或不受信任"},
		},
		{
			name:     "launch failed",
			sentinel: codexdesktop.ErrLaunchFailed,
			want:     "ChatGPT / Codex 桌面应用本身无法启动。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。",
		},
		{
			name: "unknown cause",
			want: "ChatGPT / Codex 启动失败。请重新运行安装向导检查配置后重试。",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawCause := errors.New(`open C:\Users\alice\secret.txt token=top-secret HKEY_CLASSES_ROOT\codex`)
			cause := error(rawCause)
			if tt.sentinel != nil {
				cause = fmt.Errorf("%w: %w", tt.sentinel, rawCause)
			}

			safeErr := SafeFrontendLaunchError(state.FrontendModeCodexDesktop, cause)
			if safeErr == nil {
				t.Fatal("SafeFrontendLaunchError returned nil")
			}
			if got := safeErr.Error(); got != tt.want {
				t.Fatalf("safe error=%q, want %q", got, tt.want)
			}
			if !errors.Is(safeErr, cause) {
				t.Fatalf("errors.Is(safeErr, cause)=false; err=%v", safeErr)
			}
			if !errors.Is(safeErr, rawCause) {
				t.Fatalf("errors.Is(safeErr, rawCause)=false; err=%v", safeErr)
			}
			if tt.sentinel != nil && !errors.Is(safeErr, tt.sentinel) {
				t.Fatalf("errors.Is(safeErr, %v)=false; err=%v", tt.sentinel, safeErr)
			}
			if got := utf8.RuneCountInString(safeErr.Error()); got > 256 {
				t.Fatalf("safe error has %d runes, want at most 256", got)
			}
			for _, required := range tt.required {
				if !strings.Contains(safeErr.Error(), required) {
					t.Fatalf("safe error %q does not contain %q", safeErr, required)
				}
			}
			for _, forbidden := range []string{"alice", "secret.txt", "token=top-secret", `HKEY_CLASSES_ROOT\codex`, retiredCodexStoreProductID} {
				if strings.Contains(safeErr.Error(), forbidden) {
					t.Fatalf("safe error leaked %q: %q", forbidden, safeErr)
				}
			}
		})
	}
}

func TestSafeFrontendInstallErrorIsModeSpecificAndSafe(t *testing.T) {
	tests := []struct {
		name     string
		mode     state.FrontendMode
		sentinel error
		want     string
	}{
		{
			name:     "codex winget missing",
			mode:     state.FrontendModeCodexDesktop,
			sentinel: codexdesktop.ErrWingetNotFound,
			want:     "未找到 winget；请安装或更新 Windows App Installer 后重试。",
		},
		{
			name:     "codex scheme missing",
			mode:     state.FrontendModeCodexDesktop,
			sentinel: codexdesktop.ErrSchemeMissing,
			want:     "ChatGPT / Codex 的 codex:// 协议缺失。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。",
		},
		{
			name:     "codex scheme target invalid",
			mode:     state.FrontendModeCodexDesktop,
			sentinel: codexdesktop.ErrSchemeTargetInvalid,
			want:     "ChatGPT / Codex 的 codex:// 协议已注册，但处理程序无效或不受信任。请在 Windows 已安装的应用 > ChatGPT > 高级选项中依次尝试 Repair、Reset；仍失败请从 Microsoft Store Reinstall。",
		},
		{
			name:     "codex not found after install",
			mode:     state.FrontendModeCodexDesktop,
			sentinel: codexdesktop.ErrNotFound,
			want:     fmt.Sprintf("未能安装或检测到 %s。请从 Microsoft Store 安装产品 ID %s 后重试。", codexdesktop.LongDisplayName, codexdesktop.CodexStoreProductID),
		},
		{
			name: "codex operational failure",
			mode: state.FrontendModeCodexDesktop,
			want: codexdesktop.LongDisplayName + "安装或检查失败。请检查网络、Microsoft Store 和 Windows App Installer 后重试。",
		},
		{
			name: "minimal vscode",
			mode: state.FrontendModeMinimalVSCode,
			want: "VS Code 安装或检查失败。请检查网络、Microsoft Store 和 Windows App Installer 后重试。",
		},
		{
			name: "opencode desktop",
			mode: state.FrontendModeOpenCodeDesktop,
			want: "OpenCode Desktop 安装或检查失败。请检查网络和安装程序后重试。",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawCause := errors.New(`winget output: PowerShell output C:\Users\alice\secret.txt token=top-secret HKEY_CLASSES_ROOT\codex`)
			cause := error(rawCause)
			if tt.sentinel != nil {
				cause = fmt.Errorf("%w: %w", tt.sentinel, rawCause)
			}

			safeErr := SafeFrontendInstallError(tt.mode, cause)
			if safeErr == nil {
				t.Fatal("SafeFrontendInstallError returned nil")
			}
			if got := safeErr.Error(); got != tt.want {
				t.Fatalf("safe install error=%q, want %q", got, tt.want)
			}
			if !errors.Is(safeErr, cause) || !errors.Is(safeErr, rawCause) {
				t.Fatalf("safe install error did not retain cause identity: %v", safeErr)
			}
			if tt.sentinel != nil && !errors.Is(safeErr, tt.sentinel) {
				t.Fatalf("errors.Is(safeErr, %v)=false; err=%v", tt.sentinel, safeErr)
			}
			if got := utf8.RuneCountInString(safeErr.Error()); got > 256 {
				t.Fatalf("safe install error has %d runes, want at most 256", got)
			}
			for _, forbidden := range []string{"alice", "secret.txt", "token=top-secret", "PowerShell output", "winget output", `HKEY_CLASSES_ROOT\codex`, retiredCodexStoreProductID} {
				if strings.Contains(safeErr.Error(), forbidden) {
					t.Fatalf("safe install error leaked %q: %q", forbidden, safeErr)
				}
			}
		})
	}
}
