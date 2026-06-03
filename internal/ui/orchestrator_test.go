package ui

import (
	"context"
	"testing"
)

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
