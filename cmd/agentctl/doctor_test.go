package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func TestRenderDoctor(t *testing.T) {
	s := &state.State{
		SchemaVersion: 1,
		Onboarding: state.OnboardingState{
			Status: state.StatusComplete,
			CompletedSteps: []string{"modelserver_login", "agentserver_login",
				"vscode_installed", "vscode_configured", "shortcuts_created"},
		},
		FrontendMode: state.FrontendModeCodexDesktop,
		CodexDesktop: state.CodexDesktopState{Installed: true, Version: "1.0.0"},
		Modelserver:  state.ModelserverState{ProjectID: "p1", APIKeySuffix: "wxyz"},
		Agentserver:  state.AgentserverState{WorkspaceID: "ws-1"},
		VSCode:       state.VSCodeState{Path: `C:\Code.exe`, Version: "1.96.0"},
	}
	var buf bytes.Buffer
	renderDoctor(&buf, s)
	out := buf.String()
	for _, want := range []string{
		"星池指挥官 doctor",
		"onboarding: complete",
		"frontend: codex_desktop",
		"codex_desktop: installed=true",
		"modelserver: project=p1",
		"vscode: 1.96.0",
		"steps: 5/5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestLoadDoctorStateSyncsInstallModeFile(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{StateFile: filepath.Join(dir, "state.json")}
	modePath := filepath.Join(dir, "app", "install-mode.json")
	if err := installmode.Write(modePath, state.FrontendModeMinimalVSCode); err != nil {
		t.Fatal(err)
	}

	got, err := loadDoctorState(p, modePath)
	if err != nil {
		t.Fatalf("loadDoctorState: %v", err)
	}
	if got.FrontendMode != state.FrontendModeMinimalVSCode {
		t.Fatalf("FrontendMode = %q, want %q", got.FrontendMode, state.FrontendModeMinimalVSCode)
	}
}
