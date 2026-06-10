package uninstall

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/slave"
)

func TestRunRemovesProjectStateSecretsAndOpenAIEnv(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		InstallRoot:      filepath.Join(dir, ".agentserver-vscode"),
		SecretsFile:      filepath.Join(dir, ".agentserver-vscode", "secrets.json"),
		LocalAppDataRoot: filepath.Join(dir, "local-appdata", "agentserver-vscode"),
	}
	sec := secrets.New(p.SecretsFile)
	for _, key := range []string{
		"modelserver_api_key",
		"modelserver_refresh_token",
		"modelserver_access_token_expires_at",
		"agentserver_ws_api_key",
		"agentserver_tunnel_token",
	} {
		if err := sec.Set(key, "value"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(p.InstallRoot, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.LocalAppDataRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	var deletedEnv string
	err := Run(Options{
		Paths:   p,
		Secrets: sec,
		DeleteEnv: func(key string) error {
			deletedEnv = key
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exists(p.InstallRoot) {
		t.Fatalf("InstallRoot still exists: %s", p.InstallRoot)
	}
	if exists(p.LocalAppDataRoot) {
		t.Fatalf("LocalAppDataRoot still exists: %s", p.LocalAppDataRoot)
	}
	if deletedEnv != "OPENAI_API_KEY" {
		t.Fatalf("deleted env = %q, want OPENAI_API_KEY", deletedEnv)
	}
	for _, key := range []string{
		"modelserver_api_key",
		"modelserver_refresh_token",
		"modelserver_access_token_expires_at",
		"agentserver_ws_api_key",
	} {
		if _, err := sec.Get(key); err != secrets.ErrNotFound {
			t.Fatalf("%s still present: %v", key, err)
		}
	}
}

func TestRunStopsLocalSlaveAndInstallProcessesBeforeRemovingState(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app")
	p := paths.Paths{
		InstallRoot:      filepath.Join(dir, ".agentserver-vscode"),
		SecretsFile:      filepath.Join(dir, ".agentserver-vscode", "secrets.json"),
		SlavesFile:       filepath.Join(dir, ".agentserver-vscode", "slaves.json"),
		SlavesDir:        filepath.Join(dir, ".agentserver-vscode", "slaves"),
		LocalAppDataRoot: filepath.Join(dir, "local-appdata", "agentserver-vscode"),
	}
	if err := os.MkdirAll(filepath.Dir(p.SlavesFile), 0o755); err != nil {
		t.Fatal(err)
	}
	created := time.Unix(1, 0).UTC()
	registry := []slave.Slave{
		{ID: "running", Status: slave.StatusRunning, PID: 4242, CreatedAt: created, UpdatedAt: created},
		{ID: "stopped", Status: slave.StatusStopped, CreatedAt: created, UpdatedAt: created},
	}
	writeJSONFile(t, p.SlavesFile, registry)

	var stopped []struct {
		pid int
		exe string
	}
	var fallbackDir string
	var fallbackNames []string
	var removed []string
	err := Run(Options{
		Paths:   p,
		Secrets: secrets.New(p.SecretsFile),
		AppDir:  appDir,
		StopProcess: func(_ context.Context, pid int, expectedExe string) error {
			stopped = append(stopped, struct {
				pid int
				exe string
			}{pid: pid, exe: expectedExe})
			if len(removed) > 0 {
				t.Fatalf("stop process called after removal started: removed=%v", removed)
			}
			return nil
		},
		StopInstallProcesses: func(_ context.Context, dir string, names []string) error {
			fallbackDir = dir
			fallbackNames = append([]string(nil), names...)
			if len(removed) > 0 {
				t.Fatalf("fallback stop called after removal started: removed=%v", removed)
			}
			return nil
		},
		DeleteEnv: func(string) error { return nil },
		RemoveAll: func(path string) error {
			removed = append(removed, path)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(stopped) != 1 {
		t.Fatalf("stopped=%+v, want exactly one running slave PID", stopped)
	}
	if stopped[0].pid != 4242 || stopped[0].exe != filepath.Join(appDir, "slave-agent.exe") {
		t.Fatalf("stopped=%+v", stopped)
	}
	if fallbackDir != appDir {
		t.Fatalf("fallbackDir=%q, want %q", fallbackDir, appDir)
	}
	for _, want := range []string{"slave-agent.exe", "driver-agent.exe", "token-refresher.exe"} {
		if !containsString(fallbackNames, want) {
			t.Fatalf("fallback process names missing %q: %v", want, fallbackNames)
		}
	}
}

func TestWindowsFallbackStopWaitsForProcessesToExit(t *testing.T) {
	body, err := os.ReadFile("process_stop_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"Wait-Process",
		"$deadline = (Get-Date).AddSeconds(",
		"Get-CimInstance Win32_Process | Where-Object $filter",
		"} while ($remaining.Count -gt 0 -and (Get-Date) -lt $deadline)",
		"if ($remaining.Count -gt 0)",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("Windows fallback stop should wait for orphan install processes to exit; missing %q in:\n%s", want, s)
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func containsString(values []string, want string) bool {
	for _, got := range values {
		if got == want {
			return true
		}
	}
	return false
}
