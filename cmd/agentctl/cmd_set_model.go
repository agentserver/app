package main

import (
	"fmt"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/protoconv"
)

func runSetModel(args []string) {
	if err := runSetModelWithConfigResolved(args); err != nil {
		die(err)
	}
}

// runSetModelWithConfigResolved resolves the real Codex config path and applies.
func runSetModelWithConfigResolved(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: agentctl set-model <name>; known models: %v", protoconv.KnownModels())
	}
	if err := validateModelSelection(args[0]); err != nil {
		return err
	}
	p, err := paths.Default()
	if err != nil {
		return err
	}
	return runSetModelWithConfig(p.CodexConfigFile, args)
}

// runSetModelWithConfig applies the selection to an explicit config path (testable).
func runSetModelWithConfig(configPath string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: set-model <name>; known models: %v", protoconv.KnownModels())
	}
	if err := validateModelSelection(args[0]); err != nil {
		return err
	}
	if err := codex.SetModel(configPath, args[0]); err != nil {
		return err
	}
	fmt.Printf("model set to %s in %s\n", args[0], configPath)
	return nil
}

// validateModelSelection rejects models not in the protoconv catalog.
func validateModelSelection(model string) error {
	if model == "" {
		return fmt.Errorf("model name required; known models: %v", protoconv.KnownModels())
	}
	if _, ok := protoconv.LookupRoute(model); !ok {
		return fmt.Errorf("unknown model %q; known models: %v", model, protoconv.KnownModels())
	}
	return nil
}
