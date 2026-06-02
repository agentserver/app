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
	setupExe := filepath.Join("..", "..", "..", "packaging", "windows", "Output",
		"agentserver-vscode-0.1.0-setup.exe")
	if _, err := os.Stat(setupExe); err != nil {
		t.Fatalf("setup exe not built: %v", err)
	}

	// 2. Push installer.
	remote := `C:\Users\61414\Downloads\agentserver-vscode-setup.exe`
	if err := c.PutFile(setupExe, remote); err != nil {
		t.Fatalf("put: %v", err)
	}

	// 3. Best-effort uninstall residual.
	out, _, _ := c.Pwsh(`& 'C:\Users\61414\AppData\Local\Programs\agentserver-vscode\agentctl.exe' uninstall --silent 2>$null; $LASTEXITCODE`)
	t.Logf("pre-uninstall: %s", out)

	// 4. Silent install.
	out, code, err := c.Pwsh(fmt.Sprintf(`Start-Process -FilePath '%s' -ArgumentList '/VERYSILENT','/SUPPRESSMSGBOXES' -Wait; $LASTEXITCODE`, remote))
	if err != nil || code != 0 {
		t.Fatalf("install: code=%d err=%v out=%s", code, err, out)
	}

	// 5. Assert: desktop .lnk exists, registry has shell hook.
	out, _, _ = c.Pwsh(`Test-Path "$env:USERPROFILE\Desktop\agentserver-vscode.lnk"`)
	if strings.TrimSpace(out) != "True" {
		t.Errorf("desktop shortcut missing: %s", out)
	}
	out, _, _ = c.Pwsh(`Test-Path 'Registry::HKEY_CURRENT_USER\Software\Classes\Directory\shell\AgentserverVscode'`)
	if strings.TrimSpace(out) != "True" {
		t.Errorf("registry key missing: %s", out)
	}

	// 6. Launch launcher (in background — it serves onboarding-server).
	c.Pwsh(`Start-Process -FilePath "$env:LOCALAPPDATA\Programs\agentserver-vscode\launcher.exe"`)

	// 7. Discover onboarding port from launcher's stdout? In v1 we hardcode
	//    a fixed port via env var. For now, look at netstat.
	port := waitForOnboardingPort(t, c)
	t.Logf("onboarding port: %s", port)

	// 8. Open chromedriver session, complete MS+AS OAuth (test accounts).
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
	// 9. Assertions
	out, _, _ = c.Pwsh(`Get-Content "$env:USERPROFILE\.codex\config.toml"`)
	if !strings.Contains(out, `model_provider = "modelserver"`) {
		t.Errorf("codex config wrong: %s", out)
	}
	out, _, _ = c.Pwsh(`& "$env:LOCALAPPDATA\Programs\Microsoft VS Code\bin\code.cmd" --version`)
	if !strings.HasPrefix(strings.TrimSpace(out), "1.") {
		t.Errorf("vs code missing: %s", out)
	}

	// 10. Right-click open simulation
	c.Pwsh(`New-Item -ItemType Directory -Force "C:\tmp\e2e-test"`)
	c.Pwsh(`& "$env:LOCALAPPDATA\Programs\agentserver-vscode\open-folder.exe" "C:\tmp\e2e-test"`)
	time.Sleep(3 * time.Second)

	// 11. Cleanup
	c.Pwsh(`& "$env:LOCALAPPDATA\Programs\agentserver-vscode\agentctl.exe" uninstall --silent`)
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

// Avoid "imported and not used" on json/io
var _ = json.NewDecoder
