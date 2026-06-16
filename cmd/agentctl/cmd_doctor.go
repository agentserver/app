package main

import (
	"fmt"
	"io"
	"os"

	"github.com/agentserver/agentserver-pkg/internal/branding"
	"github.com/agentserver/agentserver-pkg/internal/installmode"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func runDoctor() {
	p, err := paths.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, "paths:", err)
		os.Exit(1)
	}
	modePath, err := installmode.Path()
	if err != nil {
		fmt.Fprintln(os.Stderr, "install mode path:", err)
		os.Exit(1)
	}
	s, err := loadDoctorState(p, modePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load state:", err)
		os.Exit(1)
	}
	renderDoctor(os.Stdout, s)
}

func loadDoctorState(p paths.Paths, installModePath string) (*state.State, error) {
	store := state.NewStore(p.StateFile)
	if err := installmode.SyncStoreIfPresent(store, installModePath); err != nil {
		return nil, err
	}
	return store.Load()
}

func renderDoctor(w io.Writer, s *state.State) {
	fmt.Fprintf(w, "%s doctor\n", branding.DisplayName)
	fmt.Fprintf(w, "  schema_version: %d\n", s.SchemaVersion)
	fmt.Fprintf(w, "  install_id: %s\n", s.InstallID)
	fmt.Fprintf(w, "  onboarding: %s\n", s.Onboarding.Status)
	mode := state.NormalizeFrontendMode(s.FrontendMode)
	fmt.Fprintf(w, "  frontend: %s\n", mode)
	fmt.Fprintf(w, "  codex_desktop: installed=%t version=%s\n", s.CodexDesktop.Installed, s.CodexDesktop.Version)
	fmt.Fprintf(w, "  steps: %d/5 %v\n", len(s.Onboarding.CompletedSteps), s.Onboarding.CompletedSteps)
	fmt.Fprintf(w, "  modelserver: project=%s key=…%s\n",
		s.Modelserver.ProjectID, s.Modelserver.APIKeySuffix)
	fmt.Fprintf(w, "  agentserver: workspace=%s key=…%s\n",
		s.Agentserver.WorkspaceID, s.Agentserver.WorkspaceAPIKeySuffix)
	fmt.Fprintf(w, "  vscode: %s @ %s\n", s.VSCode.Version, s.VSCode.Path)
	if s.Onboarding.LastError != "" {
		fmt.Fprintf(w, "  last_error: %s\n", s.Onboarding.LastError)
	}
}
