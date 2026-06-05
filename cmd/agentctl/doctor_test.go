package main

import (
	"bytes"
	"strings"
	"testing"

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
		Modelserver: state.ModelserverState{ProjectID: "p1", APIKeySuffix: "wxyz"},
		Agentserver: state.AgentserverState{WorkspaceID: "ws-1"},
		VSCode:      state.VSCodeState{Path: `C:\Code.exe`, Version: "1.96.0"},
	}
	var buf bytes.Buffer
	renderDoctor(&buf, s)
	out := buf.String()
	for _, want := range []string{
		"星池指挥官 doctor",
		"onboarding: complete",
		"modelserver: project=p1",
		"vscode: 1.96.0",
		"steps: 5/5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
