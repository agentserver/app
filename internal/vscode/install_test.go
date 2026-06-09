package vscode

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestPlanInstall_Windows(t *testing.T) {
	p := planInstallFor("windows", "amd64")
	if p.URL == "" || p.SHA256 == "" {
		t.Errorf("missing URL/sha: %+v", p)
	}
	if p.InstallerType != "InnoSetup" {
		t.Errorf("type %q", p.InstallerType)
	}
	if len(p.SilentArgs) == 0 {
		t.Errorf("silent args empty")
	}
	if len(p.URLs) < 2 {
		t.Errorf("expected at least 2 mirror URLs (prss + update.code), got %v", p.URLs)
	}
	if p.URL != p.URLs[0] {
		t.Errorf("URL should equal URLs[0] for back-compat: got URL=%q URLs[0]=%q", p.URL, p.URLs[0])
	}
	// prss CDN URL should be tried first (fastest in CN per P13.4 measurements)
	if !strings.Contains(p.URLs[0], "prss.microsoft.com") {
		t.Errorf("expected prss URL first, got %q", p.URLs[0])
	}
}

func TestWindowsPackagingManifestMatchesPlanInstall(t *testing.T) {
	var manifest struct {
		Version      string   `json:"version"`
		SHA256       string   `json:"sha256"`
		ExpectedSize int64    `json:"expected_size"`
		URLs         []string `json:"urls"`
		SilentArgs   []string `json:"silent_args"`
	}
	b, err := os.ReadFile("../../packaging/windows/vscode-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}

	plan := planInstallFor("windows", "amd64")
	if manifest.Version != LockedVersion {
		t.Fatalf("manifest version %q != LockedVersion %q", manifest.Version, LockedVersion)
	}
	if manifest.SHA256 != plan.SHA256 {
		t.Fatalf("manifest sha %q != plan sha %q", manifest.SHA256, plan.SHA256)
	}
	if manifest.ExpectedSize <= 0 {
		t.Fatalf("manifest expected_size should be set so truncated downloads are detected")
	}
	if !reflect.DeepEqual(manifest.URLs, plan.URLs) {
		t.Fatalf("manifest URLs %#v != plan URLs %#v", manifest.URLs, plan.URLs)
	}
	if !reflect.DeepEqual(manifest.SilentArgs, plan.SilentArgs) {
		t.Fatalf("manifest silent args %#v != plan silent args %#v", manifest.SilentArgs, plan.SilentArgs)
	}
}

func TestWindowsInstallScriptsIncludeVSCodeInstaller(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want []string
	}{
		{
			name: "install.ps1",
			path: "../../packaging/windows/install.ps1",
			want: []string{
				"[switch]$MinimalVSCode",
				"ensure-vscode.ps1",
				"ensure-codex-desktop.ps1",
				"write-install-mode.ps1",
				"machine.ps1",
				"vscode-manifest.json",
				"codex-desktop-installer.exe",
				"slave-agent.exe",
				"uninstall.exe",
				"Ensuring VS Code is installed",
				"codex_desktop",
				"minimal_vscode",
				"$AppDisplayName = '星池指挥官'",
				"LegacyDesktopLnk",
				"Desktop\\$AppDisplayName.lnk",
				"Software\\Classes\\*\\shell\\AgentserverVscode",
				"用星池指挥官打开",
				"Join-Path $InstallDir 'uninstall.exe'",
				"Set-ScriptOutputEncoding",
				"Set-RegistryStringValue",
				"CreateSubKey",
			},
		},
		{
			name: "ensure-vscode.ps1",
			path: "../../packaging/windows/ensure-vscode.ps1",
			want: []string{
				"curl.exe",
				"-C",
				"LocalInstallerPath",
				"ExpectedSize",
				"MaxDownloadAttempts",
				"download incomplete",
				"Write-Progress",
				"Wait-ProcessWithProgress",
				"Installing VS Code",
				"Set-ScriptOutputEncoding",
			},
		},
		{
			name: "write-install-mode.ps1",
			path: "../../packaging/windows/write-install-mode.ps1",
			want: []string{
				"ConvertTo-Json",
				"frontend_mode",
				"UTF8Encoding $false",
				"WriteAllText",
			},
		},
		{
			name: "package-windows-zip.sh",
			path: "../../scripts/package-windows-zip.sh",
			want: []string{
				"VSCODE_CACHE",
				"LOOM_RELEASE=\"v0.0.3\"",
				"LOOM_DRIVER_CACHE",
				"LOOM_SLAVE_CACHE",
				"vscode-installer.exe",
				"packaging/windows/ensure-vscode.ps1",
				"packaging/windows/ensure-codex-desktop.ps1",
				"packaging/windows/write-install-mode.ps1",
				"packaging/windows/machine.ps1",
				"packaging/windows/vscode-manifest.json",
				"codex-desktop-installer.exe",
				"slave-agent.exe",
				"dist/windows/uninstall.exe",
				"dist/windows/token-refresher.exe",
				"cp \"$VSCODE_CACHE\"",
				"cp \"$CODEX_DESKTOP_CACHE\"",
				"cp \"$LOOM_DRIVER_CACHE\"",
				"cp \"$LOOM_SLAVE_CACHE\"",
				"cp packaging/windows/ensure-vscode.ps1",
				"cp packaging/windows/machine.ps1",
				"cp packaging/windows/vscode-manifest.json",
				"cp dist/windows/uninstall.exe",
				"cp dist/windows/token-refresher.exe",
			},
		},
		{
			name: "installer.iss",
			path: "../../packaging/windows/installer.iss",
			want: []string{
				"uninstall.exe",
				"token-refresher.exe",
				"driver-agent.windows-amd64.exe",
				"DestName: \"driver-agent.exe\"",
				"slave-agent.windows-amd64.exe",
				"DestName: \"slave-agent.exe\"",
				"codex-x86_64-pc-windows-msvc.exe",
				"DestName: \"codex.exe\"",
				"VSCodeUserSetup-x64-1.96.0.exe",
				"DestName: \"vscode-installer.exe\"",
				"Codex Installer.exe",
				"DestName: \"codex-desktop-installer.exe\"",
				"MessagesFile: \"ChineseSimplified.isl\"",
				"ensure-vscode.ps1",
				"minimalvscode",
				"ensure-codex-desktop.ps1",
				"write-install-mode.ps1",
				"vscode-manifest.json",
				"powershell",
				"ensure-vscode.ps1",
				"ShouldInstallCodexDesktop",
				"codex_desktop",
				"minimal_vscode",
				"machine.ps1",
				"ComputerNamePage: TInputQueryWizardPage",
				"GetEnv('COMPUTERNAME')",
			},
		},
		{
			name: "package-windows.sh",
			path: "../../scripts/package-windows.sh",
			want: []string{
				"CODEX_CACHE",
				"VSCODE_CACHE",
				"CODEX_DESKTOP_CACHE",
				"LOOM_RELEASE=\"v0.0.3\"",
				"LOOM_DRIVER_CACHE",
				"LOOM_SLAVE_CACHE",
				"codex-x86_64-pc-windows-msvc.exe",
				"driver-agent.windows-amd64.exe",
				"slave-agent.windows-amd64.exe",
				"VSCodeUserSetup-x64-$VSCODE_VERSION.exe",
				"Codex Installer.exe",
				"packaging/windows/vscode-manifest.json",
				"packaging/windows/ensure-codex-desktop.ps1",
				"packaging/windows/write-install-mode.ps1",
				"packaging/windows/machine.ps1",
				"packaging/windows/ChineseSimplified.isl",
				"dist/windows/uninstall.exe",
				"dist/windows/token-refresher.exe",
				"ISCC=()",
				"ISCC=(\"wine\" \"$HOME/.wine/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe\")",
				"\"${ISCC[@]}\" installer.iss",
				"\"$VSCODE_CACHE\"",
				"\"$CODEX_CACHE\"",
				"\"$CODEX_DESKTOP_CACHE\"",
				"\"$LOOM_DRIVER_CACHE\"",
				"\"$LOOM_SLAVE_CACHE\"",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(body), want) {
					t.Fatalf("%s does not contain %q", tc.name, want)
				}
			}
		})
	}
}

func TestWindowsPortableMinimalVSCodeUsesBundledInstaller(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	want := "& (Join-Path $InstallDir 'ensure-vscode.ps1') -ManifestPath (Join-Path $InstallDir 'vscode-manifest.json') -LocalInstallerPath (Join-Path $srcDir 'vscode-installer.exe')"
	if !strings.Contains(string(body), want) {
		t.Fatalf("install.ps1 should pass the portable bundled VS Code installer to ensure-vscode.ps1; missing %q", want)
	}
}

func TestWindowsPortableCodexDesktopUsesBundledInstaller(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	want := "& (Join-Path $InstallDir 'ensure-codex-desktop.ps1') -LocalInstallerPath (Join-Path $srcDir 'codex-desktop-installer.exe')"
	if !strings.Contains(string(body), want) {
		t.Fatalf("install.ps1 should pass the portable bundled Codex Desktop installer to ensure-codex-desktop.ps1; missing %q", want)
	}
}

func TestWindowsMachineScriptCreatesLockedMachineIdentityAndMigratesLegacyAutoName(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/machine.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"param(",
		`[string]$MachinePath = (Join-Path $env:USERPROFILE '.agentserver-vscode\machine.json')`,
		`[string]$ComputerName = $env:COMPUTERNAME`,
		"$ComputerName = $ComputerName.Trim()",
		"if ([string]::IsNullOrWhiteSpace($ComputerName))",
		"if (Test-Path -LiteralPath $MachinePath)",
		"computer_name_locked",
		"Migrated legacy machine name",
		"leaving unchanged",
		"[guid]::NewGuid().ToString()",
		"machine_id",
		"computer_name",
		"ConvertTo-Json",
		"UTF8Encoding $false",
		"WriteAllText",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("machine.ps1 should create locked machine identity and migrate legacy auto names; missing %q", want)
		}
	}

	exists := strings.Index(s, "if (Test-Path -LiteralPath $MachinePath)")
	newMachine := strings.LastIndex(s, "$machine = [ordered]@{")
	if exists < 0 || newMachine < 0 || exists > newMachine {
		t.Fatal("machine.ps1 must check for an existing machine.json before writing")
	}

	computerNameValidation := strings.Index(s, "$ComputerName = $ComputerName.Trim()")
	if computerNameValidation < 0 {
		t.Fatal("machine.ps1 should trim and validate ComputerName when creating machine.json")
	}
	if exists < computerNameValidation {
		t.Fatal("machine.ps1 must validate ComputerName before deciding whether a legacy auto name can be migrated")
	}
}

func TestWindowsMachineScriptDoesNotExitCaller(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/machine.ps1")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "exit") {
			t.Fatalf("machine.ps1 must not use %q; it is dot/call invoked by installers and can terminate the caller", trimmed)
		}
	}
}

func TestWindowsPortableInstallerInitializesMachineBeforeFrontend(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"$MachinePath = Join-Path $env:USERPROFILE '.agentserver-vscode\\machine.json'",
		"$InitialComputerName = $env:COMPUTERNAME",
		"$ShouldPromptComputerName = $false",
		"$ShouldPromptComputerName = $true",
		"$existing.computer_name_locked",
		"$existing.computer_name -eq $env:COMPUTERNAME",
		"if ((-not $Silent) -and $ShouldPromptComputerName)",
		"Read-Host",
		"& (Join-Path $InstallDir 'machine.ps1') -MachinePath $MachinePath -ComputerName $InitialComputerName",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("install.ps1 should initialize machine identity before frontend setup; missing %q", want)
		}
	}

	machine := strings.Index(s, "& (Join-Path $InstallDir 'machine.ps1')")
	frontend := strings.Index(s, "Writing install mode")
	if machine < 0 || frontend < 0 || machine > frontend {
		t.Fatal("install.ps1 should initialize machine identity before writing frontend install mode")
	}
}

func TestWindowsPortableInstallerPromptsForLegacyAutoMachineName(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	legacyCheck := strings.Index(s, "$existing.computer_name -eq $env:COMPUTERNAME")
	prompt := strings.Index(s, "if ((-not $Silent) -and $ShouldPromptComputerName)")
	if legacyCheck < 0 || prompt < 0 || legacyCheck > prompt {
		t.Fatal("install.ps1 should offer a computer-name prompt for existing unlocked auto-generated machine.json")
	}
	if strings.Contains(s, "if ((-not $Silent) -and (-not (Test-Path -LiteralPath $MachinePath)))") {
		t.Fatal("install.ps1 should not restrict the computer-name prompt to missing machine.json only")
	}
}

func TestWindowsInnoInstallerInitializesMachineBeforeFrontend(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"ComputerNamePage: TInputQueryWizardPage",
		"CreateInputQueryPage",
		"GetEnv('COMPUTERNAME')",
		"WizardSilent",
		"GetInitialComputerName",
		"GetMachinePath",
		"machine.ps1",
		"-MachinePath",
		"-ComputerName",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("installer.iss should prompt for and initialize machine identity; missing %q", want)
		}
	}

	machine := strings.Index(s, "RunEstimatedPowerShellStep('machine'")
	frontend := strings.Index(s, "RunEstimatedPowerShellStep('codex-mode'")
	alternateFrontend := strings.Index(s, "RunEstimatedPowerShellStep('vscode-mode'")
	if machine < 0 || frontend < 0 || alternateFrontend < 0 || machine > frontend || machine > alternateFrontend {
		t.Fatal("installer.iss should initialize machine identity before frontend mode setup")
	}
}

func TestWindowsInnoChineseLanguageFileIsBundled(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ChineseSimplified.isl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LanguageName=简体中文",
		"LanguageID=$0804",
		"LanguageCodePage=936",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("ChineseSimplified.isl does not contain %q", want)
		}
	}
}

func TestWindowsInnoInstallerUsesPerUserDesktopShortcut(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "{commondesktop}") {
		t.Fatal("installer.iss must not use {commondesktop}; low-privilege installs cannot write Public Desktop")
	}
	if !strings.Contains(text, "{userdesktop}") {
		t.Fatal("installer.iss should create optional desktop shortcuts under {userdesktop}")
	}
}

func TestWindowsInnoInstallerDesktopShortcutEnabledByDefault(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, `Name: "desktopicon"`) {
			if strings.Contains(line, "unchecked") {
				t.Fatalf("desktop shortcut task should be selected by default, got line: %s", line)
			}
			return
		}
	}
	t.Fatal("installer.iss missing desktopicon task")
}

func TestWindowsInnoInstallerFrontendInstallUsesEstimatedProgress(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"procedure CurStepChanged(CurStep: TSetupStep);",
		"RunEstimatedPowerShellStep(",
		"WizardForm.ProgressGauge.Position",
		"FormatDuration(",
		"预计还需",
		"已用",
		"仍在安装，请勿关闭",
		"write-install-mode.ps1",
		"ensure-codex-desktop.ps1",
		"ensure-vscode.ps1",
		"ShouldInstallCodexDesktop",
		"WizardIsTaskSelected('minimalvscode')",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("installer.iss should run long frontend install steps with estimated progress; missing %q", want)
		}
	}
	runSectionStart := strings.Index(s, "[Run]")
	codeSectionStart := strings.Index(s, "[Code]")
	if runSectionStart < 0 || codeSectionStart < 0 || codeSectionStart <= runSectionStart {
		t.Fatal("installer.iss should contain [Run] before [Code]")
	}
	runSection := s[runSectionStart:codeSectionStart]
	for _, notWant := range []string{"write-install-mode.ps1", "ensure-codex-desktop.ps1", "ensure-vscode.ps1"} {
		if strings.Contains(runSection, notWant) {
			t.Fatalf("[Run] must not hide-wait long frontend step %q; use estimated progress code instead:\n%s", notWant, runSection)
		}
	}
}

func TestWindowsInnoInstallerStopsRunningAppProcessesBeforeReplacingFiles(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"function PrepareToInstall(var NeedsRestart: Boolean): String;",
		"StopRunningAgentserverProcesses",
		"Get-CimInstance Win32_Process",
		"token-refresher",
		"launcher",
		"onboarding-server",
		"open-folder",
		"Stop-Process -Id",
		"$_.ExecutablePath -like ($installDir + ''*'')",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("installer.iss should stop running app processes before replacing files; missing %q", want)
		}
	}
}

func TestWindowsPackageScriptsDeleteBadVSCodePartFiles(t *testing.T) {
	for _, path := range []string{
		"../../scripts/package-windows.sh",
		"../../scripts/package-windows-zip.sh",
	} {
		t.Run(path, func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			s := string(body)
			for _, marker := range []string{
				`if [[ "$local_size" != "$VSCODE_SIZE" ]]; then`,
				`if [[ "$local_sum" != "$VSCODE_SHA256" ]]; then`,
			} {
				start := strings.Index(s, marker)
				if start < 0 {
					t.Fatalf("%s missing validation block %q", path, marker)
				}
				end := strings.Index(s[start:], "\n  fi")
				if end < 0 {
					t.Fatalf("%s validation block %q missing fi", path, marker)
				}
				block := s[start : start+end]
				if !strings.Contains(block, `rm -f "$VSCODE_CACHE.part"`) {
					t.Fatalf("%s validation block %q should delete bad partial file:\n%s", path, marker, block)
				}
			}
		})
	}
}

func TestMakefileBuildsWindowsHelperExecutables(t *testing.T) {
	body, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"uninstall", "token-refresher"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("Makefile should include %q so cross-windows builds %s.exe", want, want)
		}
	}
}

func TestMakefileCrossWindowsBuildsInteractiveHelpersAsGUI(t *testing.T) {
	body, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	start := strings.Index(s, "cross-windows: ui-build")
	if start < 0 {
		t.Fatal("Makefile missing cross-windows target")
	}
	end := strings.Index(s[start:], "\n\ntest:")
	if end < 0 {
		t.Fatal("could not find end of cross-windows target")
	}
	recipe := s[start : start+end]
	for _, want := range []string{
		`case "$$cmd" in launcher|onboarding-server|open-folder|token-refresher) ldflags="$(LDFLAGS) -H=windowsgui" ;; esac;`,
		`-ldflags="$$ldflags"`,
	} {
		if !strings.Contains(recipe, want) {
			t.Fatalf("cross-windows recipe should build interactive helper executables with Windows GUI subsystem; missing %q in:\n%s", want, recipe)
		}
	}
}

func TestWindowsInstallScriptAvoidsUnsupportedLiteralPathNewItem(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("New-Item -LiteralPath")) {
		t.Fatal("install.ps1 must not use New-Item -LiteralPath; Windows PowerShell 5.1 does not support that parameter")
	}
}

func TestWindowsInstallScriptRefreshesExplorerIconCache(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"Refresh-ShellIconCache",
		"SHChangeNotify",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("install.ps1 should refresh Explorer icon cache; missing %q", want)
		}
	}
}

func TestWindowsIconIncludesExpectedSizes(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/icon.ico")
	if err != nil {
		t.Fatal(err)
	}
	if len(body) < 6 {
		t.Fatalf("icon.ico too small: %d bytes", len(body))
	}
	if got := binary.LittleEndian.Uint16(body[2:4]); got != 1 {
		t.Fatalf("icon type = %d, want 1", got)
	}
	count := int(binary.LittleEndian.Uint16(body[4:6]))
	if len(body) < 6+count*16 {
		t.Fatalf("icon directory truncated: count=%d len=%d", count, len(body))
	}
	have := map[int]bool{}
	for i := 0; i < count; i++ {
		entry := body[6+i*16:]
		width := int(entry[0])
		if width == 0 {
			width = 256
		}
		have[width] = true
	}
	for _, want := range []int{16, 32, 48, 64, 128, 256} {
		if !have[want] {
			t.Fatalf("icon.ico missing %dx%d entry; have %v", want, want, have)
		}
	}
}

func TestWindowsPowerShellScriptsUseUTF8BOM(t *testing.T) {
	for _, path := range []string{
		"../../packaging/windows/install.ps1",
		"../../packaging/windows/ensure-vscode.ps1",
		"../../packaging/windows/ensure-codex-desktop.ps1",
		"../../packaging/windows/write-install-mode.ps1",
		"../../packaging/windows/machine.ps1",
		"../../packaging/windows/factory-reset.ps1",
	} {
		t.Run(path, func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(body, []byte{0xef, 0xbb, 0xbf}) {
				t.Fatalf("%s must be UTF-8 with BOM so Windows PowerShell 5.1 decodes Chinese progress text correctly", path)
			}
		})
	}
}

func TestEnsureCodexDesktopScriptUsesBundledInstallerBeforeWingetFallback(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-codex-desktop.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"LocalInstallerPath",
		"Invoke-CodexDesktopLocalInstaller",
		"codex-desktop-installer.exe",
		"Start-Process",
		"-Wait",
		"winget",
		"install",
		"Codex",
		"-s",
		"msstore",
		"--accept-source-agreements",
		"--accept-package-agreements",
		"--disable-interactivity",
		"Test-CodexDesktopInstalled",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-codex-desktop.ps1 missing %q", want)
		}
	}
}

func TestWindowsInnoInstallerScriptUsesUTF8BOM(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatal("installer.iss must be UTF-8 with BOM so Chinese display names compile correctly")
	}
}

func TestPlanInstall_Unsupported(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic for unsupported")
		}
	}()
	_ = planInstallFor("plan9", "amd64")
}

func TestInstallAndDetect_InstallOK_DetectOK(t *testing.T) {
	install := func(context.Context, string, InstallPlan) error { return nil }
	detect := func() (Detected, error) {
		return Detected{Installed: true, Path: "/x/code", Version: LockedVersion}, nil
	}
	det, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if det.Path != "/x/code" {
		t.Errorf("got %+v", det)
	}
}

func TestInstallAndDetect_InstallOK_DetectFails(t *testing.T) {
	install := func(context.Context, string, InstallPlan) error { return nil }
	detect := func() (Detected, error) {
		return Detected{}, errors.New("VS Code not found")
	}
	_, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err == nil {
		t.Fatal("expected error when install ok but detect fails")
	}
	if !strings.Contains(err.Error(), "install ok but detect failed") {
		t.Errorf("wrong err: %v", err)
	}
}

func TestInstallAndDetect_InstallFails_DetectFindsIt(t *testing.T) {
	// This is the Bug #1 scenario: 0xc0000409 spurious exit code.
	install := func(context.Context, string, InstallPlan) error {
		return errors.New("exit status 0xc0000409")
	}
	detect := func() (Detected, error) {
		return Detected{Installed: true, Path: "/x/code", Version: LockedVersion}, nil
	}
	det, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err != nil {
		t.Fatalf("expected fallback success, got: %v", err)
	}
	if det.Path != "/x/code" || det.Version != LockedVersion {
		t.Errorf("got %+v", det)
	}
}

func TestInstallAndDetect_InstallFails_DetectDoesntFindIt(t *testing.T) {
	install := func(context.Context, string, InstallPlan) error {
		return errors.New("ERROR 5: access denied")
	}
	detect := func() (Detected, error) {
		return Detected{}, errors.New("VS Code not found")
	}
	_, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err == nil {
		t.Fatal("expected error when both install and detect fail")
	}
	if !strings.Contains(err.Error(), "ERROR 5: access denied") {
		t.Errorf("install err should be wrapped: %v", err)
	}
}

func TestInstallAndDetect_InstallFails_DetectFindsWrongVersion(t *testing.T) {
	// e.g. user already had VS Code 1.85 installed; install fails for real;
	// don't pretend it succeeded.
	install := func(context.Context, string, InstallPlan) error {
		return errors.New("disk full")
	}
	detect := func() (Detected, error) {
		return Detected{Installed: true, Path: "/x/code", Version: "1.85.0"}, nil
	}
	_, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err == nil {
		t.Fatal("expected error when detected version != LockedVersion")
	}
}
