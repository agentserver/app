package loom

import (
	"context"
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

func TestWriteDriverConfigWritesCodexExtraArgs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi-agent", "driver.yaml")

	err := WriteDriverConfig(path, DriverConfig{
		ServerURL:      "https://agent.cs.ac.cn",
		ServerName:     "driver-abc123",
		SandboxID:      "sb-1",
		TunnelToken:    "tunnel-token",
		ProxyToken:     "sandbox-proxy-token",
		WorkspaceID:    "ws-1",
		ShortID:        "abc123",
		CodexBin:       filepath.Join(dir, "agentctl.exe"),
		CodexExtraArgs: []string{"codex-debug-wrapper", "--codex", filepath.Join(dir, "codex.exe")},
	})
	if err != nil {
		t.Fatalf("WriteDriverConfig: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`  bin: "` + filepath.ToSlash(filepath.Join(dir, "agentctl.exe")) + `"`,
		`  extra_args: ["codex-debug-wrapper", "--codex", "` + filepath.ToSlash(filepath.Join(dir, "codex.exe")) + `"]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("driver.yaml missing %q:\n%s", want, text)
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

func TestStopDriverDaemonStopsTrackedMCPAndDaemonForConfig(t *testing.T) {
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
	t.Cleanup(stopDriverBackgroundProcessesForTest)

	configPath := filepath.Join(dir, "driver.yaml")
	if _, err := StartDriverDaemonManaged(exe, configPath); err != nil {
		t.Fatalf("StartDriverDaemonManaged: %v", err)
	}
	pid := parsePID(t, strings.TrimSpace(waitForFile(t, daemonPIDsPath)))
	if !DriverDaemonRunning(exe, configPath, nil) {
		t.Fatal("DriverDaemonRunning=false, want true")
	}
	if err := StopDriverDaemon(exe, configPath, nil); err != nil {
		t.Fatalf("StopDriverDaemon: %v", err)
	}
	if processExistsForTest(pid) {
		t.Fatalf("daemon pid %d still exists after stop", pid)
	}
	if DriverDaemonRunning(exe, configPath, nil) {
		t.Fatal("DriverDaemonRunning=true after stop")
	}
}

func TestStopDriverDaemonRefusesPersistedPIDWithNonMatchingExecutable(t *testing.T) {
	resetDriverProcessHooksForTest(t)
	calledTerminate := false
	inspectDriverProcess = func(pid int) (DriverProcessMetadata, bool, error) {
		return DriverProcessMetadata{
			PID:       pid,
			Exe:       "/other/driver-agent.exe",
			Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
			CreatedAt: "linux:boot:1",
		}, true, nil
	}
	terminateDriverProcess = func(context.Context, int) error {
		calledTerminate = true
		return nil
	}

	err := StopDriverDaemon("/expected/driver-agent.exe", "/tmp/driver.yaml", []DriverProcessMetadata{{
		PID:       123,
		Exe:       "/expected/driver-agent.exe",
		Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
		CreatedAt: "linux:boot:1",
	}})
	if err != nil {
		t.Fatalf("StopDriverDaemon: %v", err)
	}
	if calledTerminate {
		t.Fatal("terminated non-matching executable")
	}
}

func TestStopDriverDaemonRefusesPersistedPIDWithNonMatchingArgv(t *testing.T) {
	resetDriverProcessHooksForTest(t)
	calledTerminate := false
	inspectDriverProcess = func(pid int) (DriverProcessMetadata, bool, error) {
		return DriverProcessMetadata{
			PID:       pid,
			Exe:       "/expected/driver-agent.exe",
			Args:      []string{"serve-daemon", "--config", "/tmp/other.yaml"},
			CreatedAt: "linux:boot:1",
		}, true, nil
	}
	terminateDriverProcess = func(context.Context, int) error {
		calledTerminate = true
		return nil
	}

	err := StopDriverDaemon("/expected/driver-agent.exe", "/tmp/driver.yaml", []DriverProcessMetadata{{
		PID:       123,
		Exe:       "/expected/driver-agent.exe",
		Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
		CreatedAt: "linux:boot:1",
	}})
	if err != nil {
		t.Fatalf("StopDriverDaemon: %v", err)
	}
	if calledTerminate {
		t.Fatal("terminated process with non-matching argv")
	}
}

func TestStopDriverDaemonTerminatesPersistedPIDWhenInspectedArgvUnavailable(t *testing.T) {
	resetDriverProcessHooksForTest(t)
	var terminatedPID int
	inspectDriverProcess = func(pid int) (DriverProcessMetadata, bool, error) {
		return DriverProcessMetadata{
			PID:       pid,
			Exe:       "/expected/driver-agent.exe",
			Args:      nil,
			CreatedAt: "windows:1",
		}, true, nil
	}
	terminateDriverProcess = func(_ context.Context, pid int) error {
		terminatedPID = pid
		return nil
	}

	err := StopDriverDaemon("/expected/driver-agent.exe", "/tmp/driver.yaml", []DriverProcessMetadata{{
		PID:       123,
		Exe:       "/expected/driver-agent.exe",
		Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
		CreatedAt: "windows:1",
	}})
	if err != nil {
		t.Fatalf("StopDriverDaemon: %v", err)
	}
	if terminatedPID != 123 {
		t.Fatalf("terminatedPID=%d, want 123", terminatedPID)
	}
}

func TestStopDriverDaemonRefusesPersistedPIDWithMismatchedCreationTime(t *testing.T) {
	resetDriverProcessHooksForTest(t)
	calledTerminate := false
	inspectDriverProcess = func(pid int) (DriverProcessMetadata, bool, error) {
		return DriverProcessMetadata{
			PID:       pid,
			Exe:       "/expected/driver-agent.exe",
			Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
			CreatedAt: "linux:boot:2",
		}, true, nil
	}
	terminateDriverProcess = func(context.Context, int) error {
		calledTerminate = true
		return nil
	}

	err := StopDriverDaemon("/expected/driver-agent.exe", "/tmp/driver.yaml", []DriverProcessMetadata{{
		PID:       123,
		Exe:       "/expected/driver-agent.exe",
		Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
		CreatedAt: "linux:boot:1",
	}})
	if err != nil {
		t.Fatalf("StopDriverDaemon: %v", err)
	}
	if calledTerminate {
		t.Fatal("terminated process with mismatched creation time")
	}
}

func TestStopDriverDaemonRevalidatesPersistedPIDImmediatelyBeforeTerminate(t *testing.T) {
	resetDriverProcessHooksForTest(t)
	inspectCalls := 0
	calledTerminate := false
	inspectDriverProcess = func(pid int) (DriverProcessMetadata, bool, error) {
		inspectCalls++
		if inspectCalls == 1 {
			return DriverProcessMetadata{
				PID:       pid,
				Exe:       "/expected/driver-agent.exe",
				Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
				CreatedAt: "linux:boot:1",
			}, true, nil
		}
		return DriverProcessMetadata{
			PID:       pid,
			Exe:       "/other/driver-agent.exe",
			Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
			CreatedAt: "linux:boot:2",
		}, true, nil
	}
	terminateDriverProcess = func(context.Context, int) error {
		calledTerminate = true
		return nil
	}

	err := StopDriverDaemon("/expected/driver-agent.exe", "/tmp/driver.yaml", []DriverProcessMetadata{{
		PID:       123,
		Exe:       "/expected/driver-agent.exe",
		Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
		CreatedAt: "linux:boot:1",
	}})
	if err != nil {
		t.Fatalf("StopDriverDaemon: %v", err)
	}
	if calledTerminate {
		t.Fatal("terminated after pre-terminate revalidation changed")
	}
	if inspectCalls < 2 {
		t.Fatalf("inspectCalls=%d, want pre-terminate revalidation", inspectCalls)
	}
}

func TestDriverDaemonRunningReportsFalseForPersistedPIDWithNonMatchingExecutable(t *testing.T) {
	resetDriverProcessHooksForTest(t)
	inspectDriverProcess = func(pid int) (DriverProcessMetadata, bool, error) {
		return DriverProcessMetadata{
			PID:       pid,
			Exe:       "/other/driver-agent.exe",
			Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
			CreatedAt: "linux:boot:1",
		}, true, nil
	}

	if DriverDaemonRunning("/expected/driver-agent.exe", "/tmp/driver.yaml", []DriverProcessMetadata{{
		PID:       123,
		Exe:       "/expected/driver-agent.exe",
		Args:      []string{"serve-daemon", "--config", "/tmp/driver.yaml"},
		CreatedAt: "linux:boot:1",
	}}) {
		t.Fatal("DriverDaemonRunning=true for non-matching executable")
	}
}

func readFileIfExists(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func parsePID(t *testing.T, text string) int {
	t.Helper()
	lines := nonEmptyLines(text)
	if len(lines) == 0 {
		t.Fatalf("no pid in %q", text)
	}
	pid, err := strconv.Atoi(lines[len(lines)-1])
	if err != nil {
		t.Fatalf("parse pid %q: %v", lines[len(lines)-1], err)
	}
	return pid
}

func processExistsForTest(pid int) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	return err == nil
}

func resetDriverProcessHooksForTest(t *testing.T) {
	t.Helper()
	origInspect := inspectDriverProcess
	origTerminate := terminateDriverProcess
	t.Cleanup(func() {
		inspectDriverProcess = origInspect
		terminateDriverProcess = origTerminate
	})
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
