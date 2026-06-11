package vscode

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanInstall_WindowsUsesStoreBootstrapper(t *testing.T) {
	p := planInstallFor("windows", "amd64")
	if p.BootstrapperURL == "" {
		t.Fatalf("missing BootstrapperURL: %+v", p)
	}
	if !strings.Contains(p.BootstrapperURL, "get.microsoft.com/installer/download") {
		t.Fatalf("BootstrapperURL=%q", p.BootstrapperURL)
	}
	if p.StoreProductID != "XP9KHM4BK9FZ7Q" {
		t.Fatalf("StoreProductID=%q", p.StoreProductID)
	}
	if p.FileExt != ".exe" {
		t.Fatalf("FileExt=%q", p.FileExt)
	}
	if p.SHA256 != "" {
		t.Fatalf("Store bootstrapper should not use locked VS Code installer sha, got %q", p.SHA256)
	}
}

func TestWindowsDetectCandidatesIncludeStoreAliases(t *testing.T) {
	got := detectCandidatesWindows(`C:\Users\me\AppData\Local`, `C:\Program Files`, `C:\Program Files (x86)`)
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		`C:\Users\me\AppData\Local\Microsoft\WindowsApps\code.exe`,
		`C:\Users\me\AppData\Local\Microsoft\WindowsApps\code.cmd`,
		`C:\Users\me\AppData\Local\Programs\Microsoft VS Code\bin\code.cmd`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("detect candidates missing %q:\n%s", want, joined)
		}
	}
}

func TestDownloadBootstrapperUsesGETBecauseMicrosoftEndpointRejectsHEAD(t *testing.T) {
	body := []byte("bootstrapper")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method != http.MethodGet {
			t.Fatalf("method=%s", r.Method)
		}
		w.Write(body)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "vscode-store-bootstrapper.exe")
	if err := DownloadBootstrapper(context.Background(), srv.URL, dst, http.DefaultClient); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("body=%q", got)
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
				"ensure-codex.ps1",
				"ensure-codex-desktop.ps1",
				"write-install-mode.ps1",
				"machine.ps1",
				"codex-manifest.json",
				"codex-desktop-installer.exe",
				"slave-agent.exe",
				"uninstall.exe",
				"Ensuring Codex runtime",
				"Ensuring VS Code is installed",
				"codex_desktop",
				"minimal_vscode",
				"$AppDisplayName = '星池指挥官'",
				"LegacyDesktopLnk",
				"Desktop\\$AppDisplayName.lnk",
				"Software\\Classes\\*\\shell\\AgentserverApp",
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
				"BootstrapperURL",
				"XP9KHM4BK9FZ7Q",
				"get.microsoft.com/installer/download",
				"vscode-store-bootstrapper.exe",
				"DownloadBootstrapper",
				"Start-Process",
				"Wait-ProcessWithProgress",
				"Get-VSCodeDetection",
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
				"LOOM_RELEASE=\"v0.0.3\"",
				"LOOM_DRIVER_CACHE",
				"LOOM_SLAVE_CACHE",
				"packaging/windows/ensure-vscode.ps1",
				"packaging/windows/ensure-codex.ps1",
				"packaging/windows/codex-manifest.json",
				"packaging/windows/ensure-codex-desktop.ps1",
				"packaging/windows/write-install-mode.ps1",
				"packaging/windows/machine.ps1",
				"codex-desktop-installer.exe",
				"slave-agent.exe",
				"dist/windows/uninstall.exe",
				"dist/windows/token-refresher.exe",
				"cp \"$CODEX_DESKTOP_CACHE\"",
				"cp \"$LOOM_DRIVER_CACHE\"",
				"cp \"$LOOM_SLAVE_CACHE\"",
				"cp packaging/windows/ensure-vscode.ps1",
				"cp packaging/windows/ensure-codex.ps1",
				"cp packaging/windows/codex-manifest.json",
				"cp packaging/windows/machine.ps1",
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
				"Codex Installer.exe",
				"DestName: \"codex-desktop-installer.exe\"",
				"MessagesFile: \"ChineseSimplified.isl\"",
				"ensure-vscode.ps1",
				"ensure-codex.ps1",
				"codex-manifest.json",
				"minimalvscode",
				"ensure-codex-desktop.ps1",
				"write-install-mode.ps1",
				"powershell",
				"ensure-vscode.ps1",
				"RunEstimatedPowerShellStep('codex-runtime'",
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
				"CODEX_DESKTOP_CACHE",
				"LOOM_RELEASE=\"v0.0.3\"",
				"LOOM_DRIVER_CACHE",
				"LOOM_SLAVE_CACHE",
				"driver-agent.windows-amd64.exe",
				"slave-agent.windows-amd64.exe",
				"Codex Installer.exe",
				"packaging/windows/ensure-codex.ps1",
				"packaging/windows/codex-manifest.json",
				"packaging/windows/ensure-codex-desktop.ps1",
				"packaging/windows/write-install-mode.ps1",
				"packaging/windows/machine.ps1",
				"packaging/windows/ChineseSimplified.isl",
				"dist/windows/uninstall.exe",
				"dist/windows/token-refresher.exe",
				"ISCC=()",
				"ISCC=(\"wine\" \"$HOME/.wine/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe\")",
				"\"${ISCC[@]}\" installer.iss",
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

func TestWindowsPackagingDoesNotBundleCodexExeOrVSCodeInstaller(t *testing.T) {
	for _, path := range []string{
		"../../packaging/windows/installer.iss",
		"../../packaging/windows/install.ps1",
		"../../scripts/package-windows.sh",
		"../../scripts/package-windows-zip.sh",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s := string(body)
		for _, notWant := range []string{
			"codex-x86_64-pc-windows-msvc" + ".exe",
			"VSCode" + "UserSetup",
			"vscode-installer" + ".exe",
			"CODEX" + "_CACHE",
			"VSCODE" + "_CACHE",
			"vscode-manifest" + ".json",
		} {
			if strings.Contains(s, notWant) {
				t.Fatalf("%s must not contain %q", path, notWant)
			}
		}
	}
}

func TestWindowsPackagingIncludesCodexRuntimeEnsure(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
	}{
		{
			path: "../../packaging/windows/installer.iss",
			want: []string{
				"ensure-codex.ps1",
				"codex-manifest.json",
				"RunEstimatedPowerShellStep('codex-runtime'",
				"ensure-codex.ps1",
				"RunEstimatedPowerShellStep('codex-mode'",
				"RunEstimatedPowerShellStep('vscode-mode'",
			},
		},
		{
			path: "../../packaging/windows/install.ps1",
			want: []string{
				"'ensure-codex.ps1'",
				"'codex-manifest.json'",
				"Ensuring Codex runtime",
				"install-mode.json",
			},
		},
		{
			path: "../../scripts/package-windows.sh",
			want: []string{
				"packaging/windows/ensure-codex.ps1",
				"packaging/windows/codex-manifest.json",
			},
		},
		{
			path: "../../scripts/package-windows-zip.sh",
			want: []string{
				"cp packaging/windows/ensure-codex.ps1",
				"cp packaging/windows/codex-manifest.json",
			},
		},
	} {
		body, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.want {
			if !strings.Contains(string(body), want) {
				t.Fatalf("%s missing %q", tc.path, want)
			}
		}
	}
}

func TestWindowsEnsureCodexScriptCallsAgentctlInstallCodex(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-codex.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"param(",
		"ManifestPath",
		"AgentctlPath",
		"install-codex",
		"--manifest",
		"--dest-root",
		"--cache-dir",
		"agentserver-app",
		"cache\\codex",
		"Set-ScriptOutputEncoding",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-codex.ps1 missing %q", want)
		}
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

func TestWindowsMachineScriptCreatesStableMachineIDAndUpdatesComputerName(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/machine.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"param(",
		`[string]$MachinePath = (Join-Path $env:USERPROFILE '.agentserver-app\machine.json')`,
		`[string]$ComputerName = $env:COMPUTERNAME`,
		`[string]$ComputerNamePath = ''`,
		"ReadAllText($ComputerNamePath",
		"$ComputerName = $ComputerName.Trim()",
		"if ([string]::IsNullOrWhiteSpace($ComputerName))",
		"if (Test-Path -LiteralPath $MachinePath)",
		"$machine['computer_name'] = $ComputerName",
		"Updated machine name",
		"[guid]::NewGuid().ToString()",
		"machine_id",
		"computer_name",
		"ConvertTo-Json",
		"UTF8Encoding $false",
		"WriteAllText",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("machine.ps1 should keep machine_id stable while updating computer_name; missing %q", want)
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
		t.Fatal("machine.ps1 must validate ComputerName before updating an existing machine.json")
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
		"$MachinePath = Join-Path $env:USERPROFILE '.agentserver-app\\machine.json'",
		"$InitialComputerName = $env:COMPUTERNAME",
		"$existing.computer_name",
		"$InitialComputerName = $existingComputerName.Trim()",
		"if (-not $Silent)",
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

func TestWindowsPortableInstallerPromptsWithExistingMachineNameDefault(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	existingDefault := strings.Index(s, "$InitialComputerName = $existingComputerName.Trim()")
	prompt := strings.Index(s, "if (-not $Silent)")
	if existingDefault < 0 || prompt < 0 || existingDefault > prompt {
		t.Fatal("install.ps1 should default the computer-name prompt to existing machine.json computer_name")
	}
	if strings.Contains(s, "$ShouldPromptComputerName") {
		t.Fatal("install.ps1 should allow editing the computer name on every non-silent install")
	}
}

func TestWindowsPortableInstallerDoesNotAbortWhenShortcutCreationFails(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	shortcut := strings.Index(s, "$wsh.CreateShortcut($DesktopLnk)")
	save := strings.Index(s, "$shortcut.Save()")
	registry := strings.Index(s, "Registering file and folder context menus")
	catch := strings.Index(s, "failed to create desktop shortcut")
	if shortcut < 0 || save < 0 || registry < 0 || catch < 0 {
		t.Fatal("install.ps1 should create the desktop shortcut in a handled best-effort block")
	}
	tryBeforeShortcut := strings.LastIndex(s[:shortcut], "try {")
	if tryBeforeShortcut < 0 || catch < save || catch > registry {
		t.Fatal("install.ps1 should not abort installation when desktop shortcut creation fails")
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
		"LoadStringsFromFile",
		"JsonStringValue",
		"computer_name",
		"machine.ps1",
		"-MachinePath",
		"-ComputerNamePath",
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

func TestWindowsInnoInstallerDefaultsComputerNamePageFromMachineJson(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	existing := strings.Index(s, "GetExistingComputerName")
	initial := strings.Index(s, "function GetInitialComputerName")
	pageValue := strings.Index(s, "ComputerNamePage.Values[0] := GetInitialComputerName()")
	if existing < 0 || initial < 0 || pageValue < 0 {
		t.Fatal("installer.iss should default the editable computer-name page from machine.json")
	}
	if existing > initial || initial > pageValue {
		t.Fatal("installer.iss should read existing machine name before initializing the page value")
	}
	if strings.Contains(s, "ShouldSkipPage") {
		t.Fatal("installer.iss should not skip the computer-name page when machine.json exists")
	}
}

func TestWindowsInnoInstallerPassesComputerNameThroughUTF8File(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"SaveStringsToUTF8FileWithoutBOM",
		"SaveUTF8Text",
		"agentserver-machine-name.txt",
		"-ComputerNamePath",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("installer.iss should pass non-ASCII computer names through a UTF-8 file; missing %q", want)
		}
	}
	if strings.Contains(s, "-ComputerName ' + PowerShellQuote(ComputerName)") {
		t.Fatal("installer.iss must not embed the chosen computer name directly in the PowerShell script body")
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
		"$installRoot = [System.IO.Path]::GetFullPath($installDir).TrimEnd(''\\'')",
		"$exe.StartsWith($installRoot + ''\\'', [System.StringComparison]::OrdinalIgnoreCase)",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("installer.iss should stop running app processes before replacing files; missing %q", want)
		}
	}
	if strings.Contains(s, "$_.ExecutablePath -like ($installDir + ''*'')") {
		t.Fatal("installer.iss must not match install processes using an unsafe string prefix")
	}
}

func TestWindowsInnoInstallerStopsLocalAppDataCodexBeforeReplacingFiles(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"$localAppDataRoot = Join-Path $env:LOCALAPPDATA ''agentserver-app''",
		"$codexBin = Join-Path $localAppDataRoot ''bin\\codex.exe''",
		"$exe -ieq $codexBin",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("installer.iss should stop LocalAppData bundled codex.exe before replacing files; missing %q", want)
		}
	}
}

func TestWindowsPortableInstallerStopsRunningProcessesBeforeCopy(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"function Stop-RunningAgentserverProcesses",
		"Get-CimInstance Win32_Process",
		"$localAppDataRoot = Join-Path $env:LOCALAPPDATA 'agentserver-app'",
		"$codexBin = Join-Path $localAppDataRoot 'bin\\codex.exe'",
		"$exe -ieq $codexBin",
		"\nStop-RunningAgentserverProcesses\n",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("install.ps1 should stop old app processes and LocalAppData codex.exe before overwriting files; missing %q", want)
		}
	}
	stopCall := strings.Index(s, "\nStop-RunningAgentserverProcesses\n")
	copyStart := strings.Index(s, "# Mkdir + copy")
	if stopCall < 0 || copyStart < 0 || stopCall >= copyStart {
		t.Fatal("install.ps1 must stop running processes before copying payload files")
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
