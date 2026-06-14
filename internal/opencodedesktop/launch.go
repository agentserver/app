package opencodedesktop

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/browser"
	"github.com/agentserver/agentserver-pkg/internal/process"
)

type LaunchOptions struct {
	Detected Detected
	Folder   string
	Run      func(*exec.Cmd) error
	OpenURL  func(string) error
}

func Launch(ctx context.Context, opts LaunchOptions) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if opts.Detected.Path != "" {
		run := opts.Run
		if run == nil {
			run = func(cmd *exec.Cmd) error { return cmd.Start() }
		}
		cmd := exec.CommandContext(ctx, opts.Detected.Path)
		cmd.Dir = opts.Folder
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		process.HideWindow(cmd)
		return run(cmd)
	}
	openURL := opts.OpenURL
	if openURL == nil {
		openURL = browser.Open
	}
	if openURL == nil {
		return errors.New("opencode desktop executable not found and no URL opener configured")
	}
	if err := openURL("opencode://"); err != nil {
		return fmt.Errorf("open opencode protocol: %w", err)
	}
	return nil
}
