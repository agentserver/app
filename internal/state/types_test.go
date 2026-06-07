package state

import (
	"encoding/json"
	"testing"
)

func TestStateRoundtrip(t *testing.T) {
	s := State{
		SchemaVersion: 1,
		InstallID:     "abc-123",
		Onboarding: OnboardingState{
			Status:         StatusPending,
			CompletedSteps: []string{"modelserver_login"},
		},
		Modelserver: ModelserverState{
			BaseURL:      "https://code.cs.ac.cn",
			ProjectID:    "proj-1",
			APIKeySuffix: "abcd",
		},
	}
	b, err := json.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got State
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != 1 || got.InstallID != "abc-123" {
		t.Errorf("roundtrip lost data: %+v", got)
	}
	if len(got.Onboarding.CompletedSteps) != 1 ||
		got.Onboarding.CompletedSteps[0] != "modelserver_login" {
		t.Errorf("steps wrong: %+v", got.Onboarding.CompletedSteps)
	}
}

func TestAddCompletedDedup(t *testing.T) {
	o := &OnboardingState{}
	o.AddCompleted("a")
	o.AddCompleted("a")
	o.AddCompleted("b")
	if len(o.CompletedSteps) != 2 {
		t.Errorf("expected 2 unique steps, got %v", o.CompletedSteps)
	}
}

func TestHasCompleted(t *testing.T) {
	o := OnboardingState{CompletedSteps: []string{"x", "y"}}
	if !o.HasCompleted("x") || o.HasCompleted("z") {
		t.Errorf("HasCompleted wrong")
	}
}

func TestFrontendModeNormalize(t *testing.T) {
	for _, tc := range []struct {
		in   FrontendMode
		want FrontendMode
	}{
		{"", FrontendModeCodexDesktop},
		{"bogus", FrontendModeCodexDesktop},
		{FrontendModeCodexDesktop, FrontendModeCodexDesktop},
		{FrontendModeMinimalVSCode, FrontendModeMinimalVSCode},
	} {
		if got := NormalizeFrontendMode(tc.in); got != tc.want {
			t.Fatalf("NormalizeFrontendMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFreshStateDefaultsToCodexDesktop(t *testing.T) {
	got := freshState()
	if got.FrontendMode != FrontendModeCodexDesktop {
		t.Fatalf("FrontendMode = %q, want %q", got.FrontendMode, FrontendModeCodexDesktop)
	}
}

func TestStateRoundtripFrontendModeAndCodexDesktop(t *testing.T) {
	s := State{
		SchemaVersion: CurrentSchemaVersion,
		InstallID:     "front-1",
		FrontendMode:  FrontendModeMinimalVSCode,
		CodexDesktop: CodexDesktopState{
			Installed:     true,
			Version:       "1.2.3",
			InstalledByUs: true,
		},
	}
	b, err := json.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got State
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.FrontendMode != FrontendModeMinimalVSCode {
		t.Fatalf("FrontendMode = %q", got.FrontendMode)
	}
	if !got.CodexDesktop.Installed || got.CodexDesktop.Version != "1.2.3" || !got.CodexDesktop.InstalledByUs {
		t.Fatalf("CodexDesktop roundtrip lost data: %+v", got.CodexDesktop)
	}
}
