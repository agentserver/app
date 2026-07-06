package loom

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWriteDriverConfigDefaultsObserver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi-agent", "driver.yaml")

	err := WriteDriverConfig(path, DriverConfig{
		ServerURL:   "https://agent.cs.ac.cn",
		ServerName:  "driver-abc123",
		SandboxID:   "sb-1",
		TunnelToken: "tunnel-token",
		ProxyToken:  "sandbox-proxy-token",
		WorkspaceID: "ws-1",
		ShortID:     "abc123",
		CodexHome:   filepath.Join(dir, ".codex"),
	})
	if err != nil {
		t.Fatalf("WriteDriverConfig: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	tokenPath := filepath.ToSlash(filepath.Join(filepath.Dir(path), "observer.token"))
	for _, want := range []string{
		`  codex_home: "` + filepath.ToSlash(filepath.Join(dir, ".codex")) + `"`,
		"observer:",
		"  enabled: true",
		`  url: "https://loom.nj.cs.ac.cn:10062/"`,
		`  workspace_id: "ws-1"`,
		`  workspace_name: ""`,
		`  agent_id: "driver-abc123"`,
		`  api_key: "sandbox-proxy-token"`,
		`  token_state_path: "` + tokenPath + `"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "telemetry_enabled") {
		t.Fatalf("driver.yaml contains unsupported observer telemetry field:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "codex:") || strings.HasPrefix(line, "claude:") {
			t.Fatalf("driver.yaml contains legacy top-level key %q:\n%s", strings.TrimSuffix(line, ":"), text)
		}
	}
}

func TestStartDriverMCPServerKeepsBackgroundProcessAlive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell helper")
	}
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	pidPath := filepath.Join(dir, "pid.txt")
	eofPath := filepath.Join(dir, "eof.txt")
	exe := filepath.Join(dir, "driver-agent")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + strconv.Quote(argsPath) + "\n" +
		"echo $$ > " + strconv.Quote(pidPath) + "\n" +
		"echo 'driver: tunnel connected' >&2\n" +
		"cat >/dev/null\n" +
		"echo eof > " + strconv.Quote(eofPath) + "\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stopDriverBackgroundProcessesForTest()
	t.Cleanup(stopDriverBackgroundProcessesForTest)

	configPath := filepath.Join(dir, "driver.yaml")
	if err := StartDriverMCPServer(exe, configPath); err != nil {
		t.Fatalf("StartDriverMCPServer: %v", err)
	}

	args := waitForFile(t, argsPath)
	for _, want := range []string{"serve-mcp", "--config", configPath} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q:\n%s", want, args)
		}
	}
	pidText := strings.TrimSpace(waitForFile(t, pidPath))
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		t.Fatalf("pid %q: %v", pidText, err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(eofPath); err == nil {
		t.Fatalf("serve-mcp stdin reached EOF; background MCP server was not kept alive")
	}
	if proc, err := os.FindProcess(pid); err == nil {
		_ = proc.Kill()
	}
}

func TestStartDriverDaemonWaitsForMCPReadinessBeforeDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell helper")
	}
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	daemonPath := filepath.Join(dir, "daemon.txt")
	exe := filepath.Join(dir, "driver-agent")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"serve-mcp\" ]; then\n" +
		"  sleep 0.2\n" +
		"  : > " + strconv.Quote(readyPath) + "\n" +
		"  echo 'driver: tunnel connected' >&2\n" +
		"  cat >/dev/null\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ -f " + strconv.Quote(readyPath) + " ]; then echo ready > " + strconv.Quote(daemonPath) + "; else echo early > " + strconv.Quote(daemonPath) + "; fi\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stopDriverBackgroundProcessesForTest()
	t.Cleanup(stopDriverBackgroundProcessesForTest)

	if err := StartDriverDaemon(exe, filepath.Join(dir, "driver.yaml")); err != nil {
		t.Fatalf("StartDriverDaemon: %v", err)
	}

	got := strings.TrimSpace(waitForFile(t, daemonPath))
	if got != "ready" {
		t.Fatalf("daemon started %q; want it to wait for MCP readiness", got)
	}
}

func TestStartDriverDaemonDoesNotStartDuplicateDaemonForSameConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell helper")
	}
	dir := t.TempDir()
	daemonPIDsPath := filepath.Join(dir, "daemon-pids.txt")
	exe := filepath.Join(dir, "driver-agent")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"serve-mcp\" ]; then\n" +
		"  echo 'driver: tunnel connected' >&2\n" +
		"  cat >/dev/null\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"serve-daemon\" ]; then\n" +
		"  echo $$ >> " + strconv.Quote(daemonPIDsPath) + "\n" +
		"  trap 'exit 0' TERM INT\n" +
		"  while :; do sleep 1; done\n" +
		"fi\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stopDriverBackgroundProcessesForTest()
	t.Cleanup(func() {
		for _, line := range strings.Split(strings.TrimSpace(readFileIfExists(daemonPIDsPath)), "\n") {
			pidText := strings.TrimSpace(line)
			if pidText == "" {
				continue
			}
			pid, err := strconv.Atoi(pidText)
			if err != nil {
				continue
			}
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
		}
		stopDriverBackgroundProcessesForTest()
	})

	configPath := filepath.Join(dir, "driver.yaml")
	if err := StartDriverDaemon(exe, configPath); err != nil {
		t.Fatalf("StartDriverDaemon first: %v", err)
	}
	_ = waitForFile(t, daemonPIDsPath)
	if err := StartDriverDaemon(exe, configPath); err != nil {
		t.Fatalf("StartDriverDaemon second: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	got := nonEmptyLines(readFileIfExists(daemonPIDsPath))
	if len(got) != 1 {
		t.Fatalf("daemon starts=%d, want 1; pids=%v", len(got), got)
	}
}

func readFileIfExists(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return ""
}
