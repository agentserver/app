package uninstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/process"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/slave"
)

type testSkillsManifest struct {
	Version int                      `json:"version"`
	Files   []testSkillsManifestFile `json:"files"`
}

type testSkillsManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func TestRunRemovesManagedCodexAgentsBlockAndManagedSkillFiles(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		UserHome:         dir,
		InstallRoot:      filepath.Join(dir, ".agentserver-app"),
		SecretsFile:      filepath.Join(dir, ".agentserver-app", "secrets.json"),
		LocalAppDataRoot: filepath.Join(dir, "local-appdata", "agentserver-app"),
		CodexDir:         filepath.Join(dir, ".codex"),
	}
	agentsPath := filepath.Join(p.CodexDir, "AGENTS.md")
	writeTextFile(t, agentsPath, strings.Join([]string{
		"keep before",
		"",
		"<!-- agentserver-app loom driver prompt:start -->",
		"managed prompt",
		"<!-- agentserver-app loom driver prompt:end -->",
		"",
		"keep after",
		"",
	}, "\n"))

	codexSkill := filepath.Join(p.CodexDir, "skills", "multiagent", "SKILL.md")
	codexSkillBody := []byte("managed codex skill\n")
	writeBytesFile(t, codexSkill, codexSkillBody)
	writeJSONFile(t, filepath.Join(p.CodexDir, ".agentserver-managed-skills.json"), testSkillsManifest{
		Version: 1,
		Files:   []testSkillsManifestFile{{Path: "multiagent/SKILL.md", SHA256: testSHA256Hex(codexSkillBody)}},
	})

	agentsRoot := filepath.Join(dir, ".agents")
	agentsSkill := filepath.Join(agentsRoot, "skills", "using-superpowers", "SKILL.md")
	agentsSkillBody := []byte("managed agents skill\n")
	writeBytesFile(t, agentsSkill, agentsSkillBody)
	writeJSONFile(t, filepath.Join(agentsRoot, ".agentserver-managed-skills.json"), testSkillsManifest{
		Version: 1,
		Files:   []testSkillsManifestFile{{Path: "using-superpowers/SKILL.md", SHA256: testSHA256Hex(agentsSkillBody)}},
	})

	err := Run(Options{
		Paths:     p,
		Secrets:   secrets.New(p.SecretsFile),
		DeleteEnv: func(string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	agentsBody := readTextFile(t, agentsPath)
	for _, unwanted := range []string{
		"agentserver-app loom driver prompt:start",
		"managed prompt",
		"agentserver-app loom driver prompt:end",
	} {
		if strings.Contains(agentsBody, unwanted) {
			t.Fatalf("AGENTS.md still contains managed block content %q:\n%s", unwanted, agentsBody)
		}
	}
	for _, want := range []string{"keep before", "keep after"} {
		if !strings.Contains(agentsBody, want) {
			t.Fatalf("AGENTS.md missing user content %q:\n%s", want, agentsBody)
		}
	}
	for _, path := range []string{codexSkill, agentsSkill} {
		if exists(path) {
			t.Fatalf("managed skill still exists: %s", path)
		}
	}
	for _, path := range []string{
		filepath.Join(p.CodexDir, "skills"),
		filepath.Join(agentsRoot, "skills"),
	} {
		if exists(path) {
			t.Fatalf("empty managed skills root still exists: %s", path)
		}
	}
	for _, path := range []string{
		filepath.Join(p.CodexDir, ".agentserver-managed-skills.json"),
		filepath.Join(agentsRoot, ".agentserver-managed-skills.json"),
	} {
		if exists(path) {
			t.Fatalf("managed skills manifest still exists: %s", path)
		}
	}
}

func TestRunPreservesModifiedManagedSkillFilesAndUserSkills(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		UserHome:         dir,
		InstallRoot:      filepath.Join(dir, ".agentserver-app"),
		SecretsFile:      filepath.Join(dir, ".agentserver-app", "secrets.json"),
		LocalAppDataRoot: filepath.Join(dir, "local-appdata", "agentserver-app"),
		CodexDir:         filepath.Join(dir, ".codex"),
	}
	agentsPath := filepath.Join(p.CodexDir, "AGENTS.md")
	writeTextFile(t, agentsPath, strings.Join([]string{
		"user before",
		"<!-- agentserver-app loom driver prompt:start -->",
		"managed prompt",
		"<!-- agentserver-app loom driver prompt:end -->",
		"user after",
		"",
	}, "\n"))

	originalBody := []byte("original managed skill\n")
	modifiedBody := []byte("user edited managed skill\n")
	modifiedSkill := filepath.Join(p.CodexDir, "skills", "multiagent", "SKILL.md")
	writeBytesFile(t, modifiedSkill, modifiedBody)
	unrelatedSkill := filepath.Join(p.CodexDir, "skills", "custom", "SKILL.md")
	writeTextFile(t, unrelatedSkill, "user skill\n")
	writeJSONFile(t, filepath.Join(p.CodexDir, ".agentserver-managed-skills.json"), testSkillsManifest{
		Version: 1,
		Files:   []testSkillsManifestFile{{Path: "multiagent/SKILL.md", SHA256: testSHA256Hex(originalBody)}},
	})

	err := Run(Options{
		Paths:     p,
		Secrets:   secrets.New(p.SecretsFile),
		DeleteEnv: func(string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := readTextFile(t, modifiedSkill); got != string(modifiedBody) {
		t.Fatalf("modified managed skill = %q, want %q", got, string(modifiedBody))
	}
	if got := readTextFile(t, unrelatedSkill); got != "user skill\n" {
		t.Fatalf("unrelated skill = %q", got)
	}
	agentsBody := readTextFile(t, agentsPath)
	if strings.Contains(agentsBody, "managed prompt") || strings.Contains(agentsBody, "agentserver-app loom driver prompt") {
		t.Fatalf("AGENTS.md still contains managed block:\n%s", agentsBody)
	}
	for _, want := range []string{"user before", "user after"} {
		if !strings.Contains(agentsBody, want) {
			t.Fatalf("AGENTS.md missing user content %q:\n%s", want, agentsBody)
		}
	}
	if exists(filepath.Join(p.CodexDir, ".agentserver-managed-skills.json")) {
		t.Fatalf("managed skills manifest still exists")
	}
}

func TestRunRemovesLoomDriverCredentialsAndCodexMCPServer(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		UserHome:         dir,
		InstallRoot:      filepath.Join(dir, ".agentserver-app"),
		SecretsFile:      filepath.Join(dir, ".agentserver-app", "secrets.json"),
		LocalAppDataRoot: filepath.Join(dir, "local-appdata", "agentserver-app"),
		CodexDir:         filepath.Join(dir, ".codex"),
		CodexConfigFile:  filepath.Join(dir, ".codex", "config.toml"),
	}
	loomDir := filepath.Join(dir, ".config", "multi-agent")
	driverConfig := filepath.Join(loomDir, "driver.yaml")
	observerToken := filepath.Join(loomDir, "observer.token")
	writeTextFile(t, driverConfig, strings.Join([]string{
		"credentials:",
		`  tunnel_token: "plain-tunnel-token"`,
		`  proxy_token: "plain-proxy-token"`,
		"observer:",
		`  api_key: "plain-observer-key"`,
		`  token_state_path: "` + filepath.ToSlash(observerToken) + `"`,
		"",
	}, "\n"))
	writeTextFile(t, observerToken, "observer refresh token\n")
	writeTextFile(t, p.CodexConfigFile, strings.Join([]string{
		`model_provider = "modelserver"`,
		``,
		`[mcp_servers.driver]`,
		`command = "C:\\Agentserver\\driver-agent.exe"`,
		`args = ["serve-mcp", "--config", "` + filepath.ToSlash(driverConfig) + `"]`,
		`startup_timeout_sec = 30`,
		``,
		`[mcp_servers.other]`,
		`command = "other.exe"`,
		`args = ["serve"]`,
		``,
	}, "\n"))

	err := Run(Options{
		Paths:     p,
		Secrets:   secrets.New(p.SecretsFile),
		DeleteEnv: func(string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, path := range []string{driverConfig, observerToken, loomDir} {
		if exists(path) {
			t.Fatalf("Loom driver credential path still exists: %s", path)
		}
	}
	configBody := readTextFile(t, p.CodexConfigFile)
	for _, unwanted := range []string{
		`[mcp_servers.driver]`,
		`C:\\Agentserver\\driver-agent.exe`,
		filepath.ToSlash(driverConfig),
		`startup_timeout_sec = 30`,
	} {
		if strings.Contains(configBody, unwanted) {
			t.Fatalf("config.toml still contains driver MCP content %q:\n%s", unwanted, configBody)
		}
	}
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`[mcp_servers.other]`,
		`command = "other.exe"`,
	} {
		if !strings.Contains(configBody, want) {
			t.Fatalf("config.toml missing preserved content %q:\n%s", want, configBody)
		}
	}
}

func TestRunRemovesCodexDesktopStateFiles(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		UserHome:                          dir,
		InstallRoot:                       filepath.Join(dir, ".agentserver-app"),
		SecretsFile:                       filepath.Join(dir, ".agentserver-app", "secrets.json"),
		LocalAppDataRoot:                  filepath.Join(dir, "local-appdata", "agentserver-app"),
		CodexDir:                          filepath.Join(dir, ".codex"),
		CodexDesktopGlobalStateFile:       filepath.Join(dir, ".codex", ".codex-global-state.json"),
		CodexDesktopComputerUseConfigFile: filepath.Join(dir, ".codex", "computer-use", "config.json"),
	}
	writeTextFile(t, p.CodexDesktopGlobalStateFile, `{"localeOverride":"zh-CN"}`)
	writeTextFile(t, p.CodexDesktopComputerUseConfigFile, `{"locale":"zh-CN"}`)

	err := Run(Options{
		Paths:     p,
		Secrets:   secrets.New(p.SecretsFile),
		DeleteEnv: func(string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, path := range []string{
		p.CodexDesktopGlobalStateFile,
		p.CodexDesktopComputerUseConfigFile,
		filepath.Dir(p.CodexDesktopComputerUseConfigFile),
	} {
		if exists(path) {
			t.Fatalf("Codex Desktop state path still exists: %s", path)
		}
	}
}

func TestRunPreservesUnrelatedLoomConfigFiles(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		UserHome:         dir,
		InstallRoot:      filepath.Join(dir, ".agentserver-app"),
		SecretsFile:      filepath.Join(dir, ".agentserver-app", "secrets.json"),
		LocalAppDataRoot: filepath.Join(dir, "local-appdata", "agentserver-app"),
		CodexDir:         filepath.Join(dir, ".codex"),
		CodexConfigFile:  filepath.Join(dir, ".codex", "config.toml"),
	}
	loomDir := filepath.Join(dir, ".config", "multi-agent")
	driverConfig := filepath.Join(loomDir, "driver.yaml")
	observerToken := filepath.Join(loomDir, "observer.token")
	unrelated := filepath.Join(loomDir, "custom.yaml")
	writeTextFile(t, driverConfig, `proxy_token: "plain-proxy-token"`+"\n")
	writeTextFile(t, observerToken, "observer refresh token\n")
	writeTextFile(t, unrelated, "user config\n")

	err := Run(Options{
		Paths:     p,
		Secrets:   secrets.New(p.SecretsFile),
		DeleteEnv: func(string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if exists(driverConfig) {
		t.Fatalf("driver config still exists: %s", driverConfig)
	}
	if exists(observerToken) {
		t.Fatalf("observer token still exists: %s", observerToken)
	}
	if got := readTextFile(t, unrelated); got != "user config\n" {
		t.Fatalf("unrelated Loom config = %q", got)
	}
	if !exists(loomDir) {
		t.Fatalf("Loom config dir with unrelated file was pruned: %s", loomDir)
	}
}

func TestRunRemoveVSCodeRunsManagedUninstallerAndRemovesUserData(t *testing.T) {
	dir := t.TempDir()
	installRoot := filepath.Join(dir, ".agentserver-app")
	userDataDir := filepath.Join(dir, "vscode-data")
	extDir := filepath.Join(dir, "vscode-extensions")
	codeDir := filepath.Join(dir, "Programs", "Microsoft VS Code")
	codePath := filepath.Join(codeDir, "Code.exe")
	uninstallerPath := filepath.Join(codeDir, "unins000.exe")
	p := paths.Paths{
		UserHome:          dir,
		InstallRoot:       installRoot,
		StateFile:         filepath.Join(installRoot, "state.json"),
		SecretsFile:       filepath.Join(installRoot, "secrets.json"),
		VSCodeUserDataDir: userDataDir,
		VSCodeExtDir:      extDir,
	}
	if err := os.MkdirAll(filepath.Dir(p.StateFile), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, p.StateFile, map[string]any{
		"schema_version": 1,
		"install_id":     "install-1",
		"frontend_mode":  "minimal_vscode",
		"vscode": map[string]any{
			"path":            codePath,
			"installed_by_us": true,
		},
	})
	writeTextFile(t, codePath, "code")
	writeTextFile(t, uninstallerPath, "uninstall")
	writeTextFile(t, filepath.Join(userDataDir, "User", "settings.json"), "{}")
	writeTextFile(t, filepath.Join(extDir, "agentserver", "package.json"), "{}")
	writeTextFile(t, filepath.Join(dir, ".vscode", "extensions.json"), "{}")
	appDataCode := filepath.Join(dir, "AppData", "Roaming", "Code")
	writeTextFile(t, filepath.Join(appDataCode, "User", "settings.json"), "{}")
	t.Setenv("APPDATA", filepath.Join(dir, "AppData", "Roaming"))

	var uninstalled []string
	err := Run(Options{
		Paths:        p,
		Secrets:      secrets.New(p.SecretsFile),
		RemoveVSCode: true,
		RunVSCodeUninstaller: func(_ context.Context, path string, args ...string) error {
			uninstalled = append(uninstalled, path+" "+strings.Join(args, " "))
			return nil
		},
		DeleteEnv: func(string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(uninstalled) != 1 || !strings.HasPrefix(uninstalled[0], uninstallerPath+" ") {
		t.Fatalf("uninstalled=%v, want %s", uninstalled, uninstallerPath)
	}
	for _, wantArg := range []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"} {
		if !strings.Contains(uninstalled[0], wantArg) {
			t.Fatalf("uninstaller args missing %s: %v", wantArg, uninstalled)
		}
	}
	for _, path := range []string{userDataDir, extDir, filepath.Join(dir, ".vscode"), appDataCode} {
		if exists(path) {
			t.Fatalf("VS Code data path still exists: %s", path)
		}
	}
}

func TestRunRemovesProjectStateSecretsAndOpenAIEnv(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		InstallRoot:      filepath.Join(dir, ".agentserver-app"),
		SecretsFile:      filepath.Join(dir, ".agentserver-app", "secrets.json"),
		LocalAppDataRoot: filepath.Join(dir, "local-appdata", "agentserver-app"),
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
		InstallRoot:      filepath.Join(dir, ".agentserver-app"),
		SecretsFile:      filepath.Join(dir, ".agentserver-app", "secrets.json"),
		SlavesFile:       filepath.Join(dir, ".agentserver-app", "slaves.json"),
		SlavesDir:        filepath.Join(dir, ".agentserver-app", "slaves"),
		LocalAppDataRoot: filepath.Join(dir, "local-appdata", "agentserver-app"),
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
	var fallbackCalls []fallbackStopCall
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
			fallbackCalls = append(fallbackCalls, fallbackStopCall{dir: dir, names: append([]string(nil), names...)})
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
	if stopped[0].pid != 4242 || stopped[0].exe != filepath.Join(appDir, process.ExeName("slave-agent")) {
		t.Fatalf("stopped=%+v", stopped)
	}
	appCall, ok := findFallbackCall(fallbackCalls, appDir)
	if !ok {
		t.Fatalf("fallback calls=%+v, want app dir %s", fallbackCalls, appDir)
	}
	for _, want := range []string{process.ExeName("slave-agent"), process.ExeName("driver-agent"), process.ExeName("token-refresher")} {
		if !containsString(appCall.names, want) {
			t.Fatalf("fallback process names missing %q: %v", want, appCall.names)
		}
	}
	localCall, ok := findFallbackCall(fallbackCalls, p.LocalAppDataRoot)
	if !ok || !containsString(localCall.names, process.ExeName("codex")) {
		t.Fatalf("fallback calls=%+v, want local codex stop under %s", fallbackCalls, p.LocalAppDataRoot)
	}
}

func TestRunStopsLocalAppDataCodexBeforeRemovingState(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app")
	localRoot := filepath.Join(dir, "local-appdata", "agentserver-app")
	p := paths.Paths{
		InstallRoot:      filepath.Join(dir, ".agentserver-app"),
		SecretsFile:      filepath.Join(dir, ".agentserver-app", "secrets.json"),
		LocalAppDataRoot: localRoot,
	}
	var fallbackCalls []fallbackStopCall
	var removed []string

	err := Run(Options{
		Paths:   p,
		Secrets: secrets.New(p.SecretsFile),
		AppDir:  appDir,
		StopInstallProcesses: func(_ context.Context, dir string, names []string) error {
			fallbackCalls = append(fallbackCalls, fallbackStopCall{dir: dir, names: append([]string(nil), names...)})
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
	for _, call := range fallbackCalls {
		if call.dir == localRoot && containsString(call.names, process.ExeName("codex")) {
			return
		}
	}
	t.Fatalf("fallback calls=%+v, want codex stopped under %s", fallbackCalls, localRoot)
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

func writeTextFile(t *testing.T, path, body string) {
	t.Helper()
	writeBytesFile(t, path, []byte(body))
}

func writeBytesFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func testSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func containsString(values []string, want string) bool {
	for _, got := range values {
		if got == want {
			return true
		}
	}
	return false
}

type fallbackStopCall struct {
	dir   string
	names []string
}

func findFallbackCall(calls []fallbackStopCall, dir string) (fallbackStopCall, bool) {
	for _, call := range calls {
		if call.dir == dir {
			return call, true
		}
	}
	return fallbackStopCall{}, false
}
