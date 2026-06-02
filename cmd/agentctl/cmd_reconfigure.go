package main

import (
	"fmt"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func runReconfigure() {
	p, _ := paths.Default()
	store := state.NewStore(p.StateFile)
	_ = store.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusPending
		s.Onboarding.CompletedSteps = nil
		s.Onboarding.LastError = ""
		return nil
	})
	fmt.Println("state reset; relaunch launcher to start the onboarding UI again.")
}
