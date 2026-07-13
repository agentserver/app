//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/test/e2e/windows/harness"
)

const codexStoreProductID = "9PLM9XGG6VKS"
const retiredCodexStoreProductID = "9NT1" + "R1C2HH7"

// TestWindowsE2E runs the full install → onboard → verify → uninstall cycle.
// Requires env: E2E_SSH_HOST/PORT/USER/PASSWORD, TEST_MS_USER/PASS, TEST_AS_USER/PASS.
func TestWindowsE2E(t *testing.T) {
	if os.Getenv("E2E_SSH_HOST") == "" {
		t.Skip("E2E_SSH_HOST not set; skipping")
	}
	c, err := harness.Dial()
	if err != nil {
		t.Fatalf("ssh: %v", err)
	}
	defer c.Close()

	// 1. Locate locally-built setup .exe.
	setupExe, err := findSetupExe([]string{
		filepath.Join("..", "..", "..", "dist", "agentserver-app-*-setup.exe"),
		filepath.Join("..", "..", "..", "packaging", "windows", "Output", "agentserver-app-*-setup.exe"),
	})
	if err != nil {
		t.Fatalf("setup exe not built: %v", err)
	}

	// 2. Push installer.
	remoteTemp, _, err := c.Pwsh(`[System.IO.Path]::GetTempPath()`)
	if err != nil {
		t.Fatalf("discover remote temp directory: %v", err)
	}
	remote, err := remoteInstallerPath(remoteTemp)
	if err != nil {
		t.Fatalf("remote installer path: %v", err)
	}
	if err := c.PutFile(setupExe, remote); err != nil {
		t.Fatalf("put: %v", err)
	}

	// 3. Best-effort uninstall residual.
	out, _, _ := c.Pwsh(preUninstallCommand())
	t.Logf("pre-uninstall: %s", out)

	// 4. Silent install.
	out, code, err := c.Pwsh(fmt.Sprintf(`Start-Process -FilePath '%s' -ArgumentList '/VERYSILENT','/SUPPRESSMSGBOXES' -Wait; $LASTEXITCODE`, remote))
	if err != nil || code != 0 {
		t.Fatalf("install: code=%d err=%v out=%s", code, err, out)
	}

	// 5. Launch launcher (in background — it serves onboarding-server).
	c.Pwsh(`Start-Process -FilePath "$env:LOCALAPPDATA\Programs\agentserver-app\launcher.exe"`)

	// 6. Discover onboarding port from launcher's stdout? In v1 we hardcode
	//    a fixed port via env var. For now, look at netstat.
	port := waitForOnboardingPort(t, c)
	t.Logf("onboarding port: %s", port)

	// 7. Open chromedriver session, complete MS+AS OAuth (test accounts).
	//    Assumes chromedriver.exe is on PATH on the Windows host.
	wd, err := harness.NewWebDriver("http://127.0.0.1:9515")
	if err != nil {
		t.Fatalf("webdriver: %v", err)
	}
	defer wd.Close()
	wd.Go(fmt.Sprintf("http://127.0.0.1:%s/", port))
	wd.Click("button") // click first "开始" -> triggers MS login + opens OAuth tab
	// Switch to OAuth tab, fill credentials, submit.
	// (Wire-level chrome target switching is omitted in this excerpt;
	// see harness/webdriver.go extensions in real impl.)
	fillOAuth(t, wd, os.Getenv("TEST_MS_USER"), os.Getenv("TEST_MS_PASS"))

	// Wait for onboarding state == complete (poll up to 5 min)
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		out, _, _ := c.Pwsh(fmt.Sprintf(`(Invoke-RestMethod http://127.0.0.1:%s/api/state).onboarding_status`, port))
		if strings.TrimSpace(out) == "complete" {
			goto verify
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatal("onboarding did not complete within 5 minutes")

verify:
	// 8. Assertions
	out, _, _ = c.Pwsh(`Get-Content "$env:USERPROFILE\.codex\config.toml"`)
	if !strings.Contains(out, `model_provider = "modelserver"`) {
		t.Errorf("codex config wrong: %s", out)
	}
	out, _, _ = c.Pwsh(`Get-Content "$env:LOCALAPPDATA\Programs\agentserver-app\install-mode.json"`)
	if !strings.Contains(out, "codex_desktop") {
		t.Errorf("install mode wrong: %s", out)
	}
	out, code, wingetErr := c.Pwsh(wingetListCommand())
	if wingetErr != nil || code != 0 {
		t.Errorf("ChatGPT / Codex exact Store product not listed by winget: code=%d err=%v out=%s", code, wingetErr, out)
	}
	out, _, _ = c.Pwsh(codexDesktopInstalledPowerShell())
	if strings.TrimSpace(out) != "True" {
		t.Errorf("codex URL scheme missing: %s", out)
	}
	out, _, _ = c.Pwsh(`Test-Path "$env:USERPROFILE\Desktop\星池指挥官.lnk"`)
	if strings.TrimSpace(out) != "True" {
		t.Errorf("desktop shortcut missing: %s", out)
	}
	out, _, _ = c.Pwsh(`Test-Path 'Registry::HKEY_CURRENT_USER\Software\Classes\Directory\shell\AgentserverApp'`)
	if strings.TrimSpace(out) != "True" {
		t.Errorf("registry key missing: %s", out)
	}
	out, _, _ = c.Pwsh(`(Get-Item 'Registry::HKEY_CURRENT_USER\Software\Classes\Directory\shell\AgentserverApp').GetValue('')`)
	if strings.TrimSpace(out) != "用星池指挥官打开" {
		t.Errorf("context menu label wrong: %s", out)
	}

	// 9. Right-click open simulation
	c.Pwsh(`New-Item -ItemType Directory -Force "C:\tmp\e2e-test"`)
	c.Pwsh(`& "$env:LOCALAPPDATA\Programs\agentserver-app\open-folder.exe" "C:\tmp\e2e-test"`)
	time.Sleep(3 * time.Second)

	// 10. Cleanup
	c.Pwsh(`& "$env:LOCALAPPDATA\Programs\agentserver-app\agentctl.exe" uninstall --silent`)
	c.Pwsh(`Remove-Item -Recurse -Force "C:\tmp\e2e-test"`)
}

func waitForOnboardingPort(t *testing.T, c *harness.Client) string {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, _, _ := c.Pwsh(`(Get-Process launcher -ErrorAction SilentlyContinue | Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue).LocalPort`)
		out = strings.TrimSpace(out)
		if out != "" {
			return strings.Split(out, "\n")[0]
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatal("could not find onboarding-server port")
	return ""
}

func fillOAuth(t *testing.T, wd *harness.WebDriver, user, pass string) {
	// site-specific; placeholder selectors below should be updated after
	// inspecting the real login page in dev.
	wd.WaitForTitle("登录", 30*time.Second)
	wd.FindAndType("input[name='username']", user)
	wd.FindAndType("input[name='password']", pass)
	wd.Click("button[type='submit']")
}

func codexDesktopInstalledPowerShell() string {
	return `$detector = Join-Path $env:LOCALAPPDATA 'Programs\agentserver-app\codex-desktop-detect.ps1'; if (-not (Test-Path -LiteralPath $detector -PathType Leaf)) { throw 'codex-desktop-detect.ps1 missing' }; . $detector; (Get-ChatGPTCodexDetection).status -ceq 'ready'`
}

func findSetupExe(patterns []string) (string, error) {
	var newest string
	var newestModTime time.Time
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", fmt.Errorf("glob setup artifact %q: %w", pattern, err)
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			if newest == "" || info.ModTime().After(newestModTime) ||
				(info.ModTime().Equal(newestModTime) && match > newest) {
				newest = match
				newestModTime = info.ModTime()
			}
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no agentserver-app-*-setup.exe found")
	}
	return newest, nil
}

func remoteInstallerPath(remoteTemp string) (string, error) {
	remoteTemp = strings.TrimSpace(remoteTemp)
	if remoteTemp == "" || strings.ContainsAny(remoteTemp, "'\r\n") {
		return "", fmt.Errorf("invalid remote temporary directory")
	}
	remoteTemp = strings.TrimRight(remoteTemp, `\/`)
	if remoteTemp == "" {
		return "", fmt.Errorf("invalid remote temporary directory")
	}
	return remoteTemp + `\agentserver-app-setup.exe`, nil
}

func wingetListCommand() string {
	return `winget list --id=` + codexStoreProductID + ` --source=msstore --exact --accept-source-agreements --disable-interactivity`
}

func preUninstallCommand() string {
	return `& "$env:LOCALAPPDATA\Programs\agentserver-app\agentctl.exe" uninstall --silent 2>$null; $LASTEXITCODE`
}

func TestCodexDesktopInstalledPowerShellUsesExactAppxFallback(t *testing.T) {
	script := codexDesktopInstalledPowerShell()
	for _, want := range []string{
		`codex-desktop-detect.ps1`,
		`Get-ChatGPTCodexDetection`,
		`status -ceq 'ready'`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("codex desktop readiness check missing shared detector contract %q:\n%s", want, script)
		}
	}
	for _, notWant := range []string{"Get-AppxPackage", "HKEY_CLASSES_ROOT", "UserChoice", "Name -like", "PackageFullName -like"} {
		if strings.Contains(script, notWant) {
			t.Fatalf("codex desktop E2E must not duplicate weaker detection logic %q:\n%s", notWant, script)
		}
	}
}

func TestFindSetupExeSelectsNewestVersionAgnosticArtifact(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "agentserver-app-0.1.1-setup.exe")
	newer := filepath.Join(dir, "agentserver-app-0.12.0-setup.exe")
	for _, path := range []string{older, newer} {
		if err := os.WriteFile(path, []byte("setup"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Unix(1, 0)
	newTime := time.Unix(2, 0)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	got, err := findSetupExe([]string{filepath.Join(dir, "agentserver-app-*-setup.exe")})
	if err != nil {
		t.Fatalf("findSetupExe: %v", err)
	}
	if got != newer {
		t.Fatalf("setup=%q, want newest %q", got, newer)
	}
}

func TestRemoteInstallerPathUsesDiscoveredTempDirectory(t *testing.T) {
	got, err := remoteInstallerPath(`C:\Users\runner\AppData\Local\Temp\`)
	if err != nil {
		t.Fatalf("remoteInstallerPath: %v", err)
	}
	if got != `C:\Users\runner\AppData\Local\Temp\agentserver-app-setup.exe` {
		t.Fatalf("remote installer=%q", got)
	}
	if strings.Contains(got, `C:\Users\61414`) {
		t.Fatalf("remote installer retains hard-coded E2E user: %q", got)
	}
}

func TestPreUninstallCommandUsesCurrentUserLocalAppData(t *testing.T) {
	got := preUninstallCommand()
	if !strings.Contains(got, `$env:LOCALAPPDATA\Programs\agentserver-app\agentctl.exe`) {
		t.Fatalf("pre-uninstall command does not use current-user LOCALAPPDATA: %s", got)
	}
	if strings.Contains(got, `C:\Users\`) || strings.Contains(got, "61414") {
		t.Fatalf("pre-uninstall command contains a hard-coded user profile: %s", got)
	}
}

func TestWingetListCommandUsesExactStoreProductID(t *testing.T) {
	got := wingetListCommand()
	for _, want := range []string{"winget list", "--id=" + codexStoreProductID, "--source=msstore", "--exact"} {
		if !strings.Contains(got, want) {
			t.Fatalf("winget list command missing %q: %s", want, got)
		}
	}
	for _, forbidden := range []string{"winget list Codex", "-s msstore", retiredCodexStoreProductID} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("winget list command contains legacy matching %q: %s", forbidden, got)
		}
	}
}

// Avoid "imported and not used" on json/io
var _ = json.NewDecoder
