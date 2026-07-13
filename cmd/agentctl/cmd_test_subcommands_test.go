package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/vscode"
)

func TestOpenTestFolderCodexDesktopUsesDeepLinkAndWritesConfig(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{CodexConfigFile: filepath.Join(dir, ".codex", "config.toml")}
	s := &state.State{FrontendMode: state.FrontendModeCodexDesktop}
	var opened string
	runnerCalled := false

	msg, err := openTestFolder(context.Background(), s, p, `C:\Project Folder`, func(_ context.Context, folder string) error {
		opened = codexdesktop.ThreadURL(folder)
		return nil
	}, func(string, []string) (int, error) {
		runnerCalled = true
		return 0, nil
	})
	if err != nil {
		t.Fatalf("openTestFolder: %v", err)
	}
	if runnerCalled {
		t.Fatal("VS Code runner should not be called in Codex Desktop mode")
	}
	if !strings.HasPrefix(opened, "codex://threads/new?path=") {
		t.Fatalf("opened=%q", opened)
	}
	if !strings.Contains(opened, "Project+Folder") {
		t.Fatalf("path not encoded: %q", opened)
	}
	if !strings.Contains(msg, "with ChatGPT / Codex") {
		t.Fatalf("msg=%q", msg)
	}
	b, err := os.ReadFile(p.CodexConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `model_provider = "modelserver"`) {
		t.Fatalf("config missing modelserver provider:\n%s", b)
	}
	if !strings.Contains(string(b), `base_url = "`+modelproxy.DefaultBaseURL+`"`) {
		t.Fatalf("config missing local proxy base_url:\n%s", b)
	}
	if !strings.Contains(string(b), `experimental_bearer_token = "`+codex.LegacyLocalProxyAPIKeyValue+`"`) {
		t.Fatalf("config missing local proxy bearer token:\n%s", b)
	}
}

func TestOpenTestFolderMinimalVSCodeUsesRunner(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		VSCodeUserDataDir: filepath.Join(dir, "vscode-data"),
		VSCodeExtDir:      filepath.Join(dir, "vscode-extensions"),
		CodexConfigFile:   filepath.Join(dir, ".codex", "config.toml"),
	}
	s := &state.State{
		FrontendMode: state.FrontendModeMinimalVSCode,
		VSCode:       state.VSCodeState{Path: filepath.Join(dir, "code.exe")},
	}
	var gotExe string
	var gotArgs []string
	openerCalled := false

	msg, err := openTestFolder(context.Background(), s, p, filepath.Join(dir, "work"), func(context.Context, string) error {
		openerCalled = true
		return nil
	}, func(codeExe string, args []string) (int, error) {
		gotExe = codeExe
		gotArgs = args
		return 123, nil
	})
	if err != nil {
		t.Fatalf("openTestFolder: %v", err)
	}
	if openerCalled {
		t.Fatal("Codex Desktop opener should not be called in minimal VS Code mode")
	}
	if gotExe != s.VSCode.Path {
		t.Fatalf("codeExe=%q, want %q", gotExe, s.VSCode.Path)
	}
	if len(gotArgs) == 0 || gotArgs[len(gotArgs)-1] != filepath.Join(dir, "work") {
		t.Fatalf("args=%q", gotArgs)
	}
	if !strings.Contains(msg, "pid 123") {
		t.Fatalf("msg=%q", msg)
	}
	b, err := os.ReadFile(p.CodexConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `base_url = "`+modelproxy.DefaultBaseURL+`"`) {
		t.Fatalf("config missing local proxy base_url:\n%s", b)
	}
}

func TestRecordTestVSCodeInstallSetsMinimalMode(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		VSCodeUserDataDir: filepath.Join(dir, "vscode-data"),
		VSCodeExtDir:      filepath.Join(dir, "vscode-extensions"),
	}
	s := &state.State{}

	recordTestVSCodeInstall(s, p, vscode.Detected{
		Path:    filepath.Join(dir, "code.exe"),
		Version: "1.96.0",
	}, true)

	if s.FrontendMode != state.FrontendModeMinimalVSCode {
		t.Fatalf("FrontendMode=%q, want %q", s.FrontendMode, state.FrontendModeMinimalVSCode)
	}
	if !s.Onboarding.HasCompleted("vscode_installed") {
		t.Fatal("vscode_installed step missing")
	}
	if s.VSCode.UserDataDir != p.VSCodeUserDataDir || s.VSCode.ExtensionsDir != p.VSCodeExtDir {
		t.Fatalf("VSCode dirs not recorded: %+v", s.VSCode)
	}
}

func TestRecordTestVSCodeConfigureSetsMinimalMode(t *testing.T) {
	s := &state.State{}

	recordTestVSCodeConfigure(s)

	if s.FrontendMode != state.FrontendModeMinimalVSCode {
		t.Fatalf("FrontendMode=%q, want %q", s.FrontendMode, state.FrontendModeMinimalVSCode)
	}
	if !s.Onboarding.HasCompleted("vscode_configured") {
		t.Fatal("vscode_configured step missing")
	}
}
