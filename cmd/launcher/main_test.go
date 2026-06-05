package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/paths"
)

func TestExecVSCodeEnsuresCodexConfigBeforeLaunch(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		VSCodeUserDataDir: filepath.Join(dir, "vscode-data"),
		VSCodeExtDir:      filepath.Join(dir, "vscode-extensions"),
		CodexConfigFile:   filepath.Join(dir, ".codex", "config.toml"),
	}

	err := execVSCode(filepath.Join(dir, "missing-code.exe"), p, "", nil, "")
	if err == nil {
		t.Fatal("expected missing VS Code executable error")
	}

	b, readErr := os.ReadFile(p.CodexConfigFile)
	if readErr != nil {
		t.Fatalf("expected codex config to be written before launching VS Code: %v", readErr)
	}
	s := string(b)
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`[windows]`,
		`sandbox = "unelevated"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestPreferredIconPathUsesCacheBustingIconWhenPresent(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "icon-abc123.ico")
	if err := os.WriteFile(filepath.Join(dir, "icon.ico"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versioned, []byte("versioned"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := preferredIconPath(dir); got != versioned {
		t.Fatalf("preferredIconPath() = %q, want %q", got, versioned)
	}
}
