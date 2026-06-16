package opencodedesktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/browser"
	"github.com/agentserver/agentserver-pkg/internal/process"
)

const ConfigEnvName = "OPENCODE_CONFIG"

type LaunchOptions struct {
	Detected Detected
	Folder   string
	Config   ConfigEnv
	Run      func(*exec.Cmd) error
	OpenURL  func(string) error
}

type ConfigEnv struct {
	Path      string
	APIKeyEnv string
	APIKey    string
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
		cmd.Env = opencodeEnv(os.Environ(), opts.Config)
		process.HideWindow(cmd)
		return run(cmd)
	}
	return openProtocol(opts.OpenURL)
}

func opencodeEnv(base []string, cfg ConfigEnv) []string {
	env := append([]string(nil), base...)
	if strings.TrimSpace(cfg.Path) != "" {
		env = upsertEnv(env, ConfigEnvName, cfg.Path)
	}
	if strings.TrimSpace(cfg.APIKeyEnv) != "" && cfg.APIKey != "" {
		env = upsertEnv(env, cfg.APIKeyEnv, cfg.APIKey)
	}
	return env
}

func upsertEnv(env []string, key, value string) []string {
	prefix := key + "="
	entry := prefix + value
	for i, got := range env {
		if strings.HasPrefix(got, prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func openProtocol(openURL func(string) error) error {
	if openURL == nil {
		openURL = browser.Open
	}
	if openURL == nil {
		return errors.New("opencode desktop protocol opener is not configured")
	}
	if err := openURL("opencode://"); err != nil {
		return fmt.Errorf("open opencode protocol: %w", err)
	}
	return nil
}
