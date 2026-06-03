package main

import (
	"fmt"
	"io"
	"os"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func runDoctor() {
	p, err := paths.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, "paths:", err)
		os.Exit(1)
	}
	s, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load state:", err)
		os.Exit(1)
	}
	renderDoctor(os.Stdout, s)
}

func renderDoctor(w io.Writer, s *state.State) {
	fmt.Fprintf(w, "agentserver-vscode doctor\n")
	fmt.Fprintf(w, "  schema_version: %d\n", s.SchemaVersion)
	fmt.Fprintf(w, "  install_id: %s\n", s.InstallID)
	fmt.Fprintf(w, "  onboarding: %s\n", s.Onboarding.Status)
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
