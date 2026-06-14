package vscode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	stubBootstrapperSignatureValidator(t)
	body := fakeBootstrapperBody()
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

func TestDownloadBootstrapperRejectsNonExecutableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), int(minBootstrapperSize)))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "vscode-store-bootstrapper.exe")
	err := DownloadBootstrapper(context.Background(), srv.URL, dst, http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "MZ") {
		t.Fatalf("err=%v, want MZ validation failure", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("invalid bootstrapper should not be promoted, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(dst + ".part"); !os.IsNotExist(statErr) {
		t.Fatalf("invalid bootstrapper partial should be removed, stat err=%v", statErr)
	}
}

func TestDownloadBootstrapperAbortsStalledBodyAfterIdleTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-release
	}))
	defer srv.Close()
	defer close(release)

	dst := filepath.Join(t.TempDir(), "vscode-store-bootstrapper.exe")
	err := downloadBootstrapper(context.Background(), srv.URL, dst, http.DefaultClient, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "download idle timeout") {
		t.Fatalf("err=%v, want idle timeout", err)
	}
	if _, statErr := os.Stat(dst + ".part"); !os.IsNotExist(statErr) {
		t.Fatalf("stalled bootstrapper partial should be removed, stat err=%v", statErr)
	}
}

func TestEnsureVSCodeScriptBoundsBootstrapperProcessAndPublisher(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-vscode.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"ExpectedBootstrapperPublisherPattern",
		"SignerCertificate.Subject",
		"X509Chain",
		"O=Microsoft Corporation",
		"function Wait-ProcessWithProgress([System.Diagnostics.Process]$Process, [string]$Activity, [string]$Status, [int]$TimeoutSeconds)",
		"Stop-Process -Id $Process.Id -Force",
		"Wait-ProcessWithProgress $proc \"Installing VS Code\" \"正在通过微软商店引导器安装 VS Code，请稍候...\" $InstallTimeoutSeconds",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-vscode.ps1 missing %q", want)
		}
	}
}

func TestWindowsBootstrapperGoValidatorChecksAuthenticodePublisher(t *testing.T) {
	body, err := os.ReadFile("install_authenticode_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"Get-AuthenticodeSignature",
		"ExpectedBootstrapperPublisherPattern",
		"SignerCertificate.Subject",
		"X509Chain",
		"O=Microsoft Corporation",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("install_authenticode_windows.go missing %q", want)
		}
	}
}

func fakeBootstrapperBody() []byte {
	body := bytes.Repeat([]byte{0}, int(minBootstrapperSize))
	body[0] = 'M'
	body[1] = 'Z'
	return body
}

func stubBootstrapperSignatureValidator(t *testing.T) {
	t.Helper()
	old := bootstrapperSignatureValidator
	bootstrapperSignatureValidator = func(context.Context, string) error { return nil }
	t.Cleanup(func() {
		bootstrapperSignatureValidator = old
	})
}

func TestWindowsInstallScriptsIncludeExpectedInstallerAssets(t *testing.T) {
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
				"install-driver-support.ps1",
				"write-install-mode.ps1",
				"machine.ps1",
				"codex-manifest.json",
				"codex-desktop-installer.exe",
				"slave-agent.exe",
				"driver-skills.tar.gz",
				"driver-superpower-skills.tar.gz",
				"driver-codex-prompts.tar.gz",
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
				"Test-BootstrapperFile",
				"MinBootstrapperSize",
				"DownloadTimeoutSeconds",
				"DownloadIdleTimeoutSeconds",
				"--max-time",
				"--speed-time",
				"--speed-limit",
				"-TimeoutSec",
				"Get-AuthenticodeSignature",
				"Move-Item",
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
			name: "windows-package-common.sh",
			path: "../../scripts/windows-package-common.sh",
			want: []string{
				"LOOM_RELEASE=\"v0.0.5\"",
				"LOOM_DRIVER_SHA256=\"be3836eba3fabc5006d83a8edf687b0c0183e87beb493d2cb3c1799577f0c322\"",
				"LOOM_SLAVE_SHA256=\"8e0dfe1b7ce57dac387207f19ed7ebe4f2ab3a398990fcb3acc6c0c2a52bd27d\"",
				"LOOM_DRIVER_SKILLS_SHA256=\"4466b0342eaa90284dc4de0f0c03e6d08dbe02e4c12d0da6e7cb433c61ea1a0c\"",
				"LOOM_DRIVER_CACHE",
				"LOOM_SLAVE_CACHE",
				"LOOM_DRIVER_SKILLS_CACHE",
				"SUPERPOWER_SKILLS_CACHE",
				"LOOM_DRIVER_CODEX_PROMPTS_CACHE",
				"packaging/windows/ensure-vscode.ps1",
				"packaging/windows/ensure-codex.ps1",
				"packaging/windows/codex-manifest.json",
				"packaging/windows/ensure-codex-desktop.ps1",
				"packaging/windows/install-driver-support.ps1",
				"packaging/windows/write-install-mode.ps1",
				"packaging/windows/machine.ps1",
				"codex-desktop-installer.exe",
				"slave-agent.exe",
				"dist/windows/uninstall.exe",
				"dist/windows/token-refresher.exe",
				"$CODEX_DESKTOP_CACHE::codex-desktop-installer.exe",
				"$LOOM_DRIVER_CACHE::driver-agent.exe",
				"$LOOM_SLAVE_CACHE::slave-agent.exe",
				"$LOOM_DRIVER_SKILLS_CACHE::driver-skills.tar.gz",
				"$SUPERPOWER_SKILLS_CACHE::driver-superpower-skills.tar.gz",
				"$LOOM_DRIVER_CODEX_PROMPTS_CACHE::driver-codex-prompts.tar.gz",
				"packaging/windows/ensure-vscode.ps1::ensure-vscode.ps1",
				"packaging/windows/ensure-codex.ps1::ensure-codex.ps1",
				"packaging/windows/codex-manifest.json::codex-manifest.json",
				"packaging/windows/install-driver-support.ps1::install-driver-support.ps1",
				"packaging/windows/machine.ps1::machine.ps1",
				"dist/windows/uninstall.exe::uninstall.exe",
				"dist/windows/token-refresher.exe::token-refresher.exe",
			},
		},
		{
			name: "installer.iss",
			path: "../../packaging/windows/installer.iss",
			want: []string{
				"uninstall.exe",
				"token-refresher.exe",
				"driver-agent.windows-amd64.exe",
				"v0.0.5",
				"DestName: \"driver-agent.exe\"",
				"slave-agent.windows-amd64.exe",
				"DestName: \"slave-agent.exe\"",
				"driver-skills.tar.gz",
				"driver-superpower-skills.tar.gz",
				"driver-codex-prompts.tar.gz",
				"install-driver-support.ps1",
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
				"source scripts/windows-package-common.sh",
				"fetch_windows_package_assets",
				"check_windows_package_required_files",
				"ISCC=()",
				"ISCC=(\"wine\" \"$HOME/.wine/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe\")",
				"\"${ISCC[@]}\" installer.iss",
			},
		},
		{
			name: "package-windows-zip.sh",
			path: "../../scripts/package-windows-zip.sh",
			want: []string{
				"source scripts/windows-package-common.sh",
				"fetch_windows_package_assets",
				"check_windows_package_required_files",
				"copy_portable_payloads \"$STAGE\"",
				"agentserver-app-$VERSION-portable.zip",
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

func TestWindowsInstallScriptSupportsOpenCodeDesktopMode(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"[switch]$OpenCodeDesktop",
		"$OpenCodeDesktop -and $MinimalVSCode",
		"opencode_desktop",
		"ensure-opencode-desktop.ps1",
		"Writing install mode opencode_desktop",
		"Ensuring OpenCode Desktop is installed",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("install.ps1 missing %q", want)
		}
	}
	if strings.Contains(s, "opencode-desktop-installer.exe") {
		t.Fatal("install.ps1 must not require a bundled OpenCode Desktop installer")
	}
}

func TestWriteInstallModeAllowsOpenCodeDesktop(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/write-install-mode.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "'opencode_desktop'") {
		t.Fatal("write-install-mode.ps1 must allow opencode_desktop")
	}
}

func TestWindowsPackagingDoesNotBundleOpenCodeDesktopInstaller(t *testing.T) {
	body, err := os.ReadFile("../../scripts/windows-package-common.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"packaging/windows/ensure-opencode-desktop.ps1",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("windows-package-common.sh missing %q", want)
		}
	}
	for _, notWant := range []string{
		"OPENCODE_DESKTOP_CACHE",
		"OPENCODE_DESKTOP_ASSET",
		"verify_opencode_desktop_installer",
		"opencode-desktop-installer.exe",
	} {
		if strings.Contains(s, notWant) {
			t.Fatalf("windows-package-common.sh must not bundle OpenCode Desktop installer; found %q", notWant)
		}
	}
}

func TestWindowsInnoInstallerSupportsOpenCodeDesktopMode(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"opencodedesktop",
		"OpenCode Desktop",
		"ensure-opencode-desktop.ps1",
		"ShouldInstallOpenCodeDesktop",
		"opencode_desktop",
		"opencode-install",
		"CurPageID = wpSelectTasks",
		"请选择一种界面模式。",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("installer.iss missing %q", want)
		}
	}
	for _, notWant := range []string{
		"OpenCode Desktop Installer.exe",
		"opencode-desktop-installer.exe",
		"dist\\cache\\opencode-desktop",
		"((not WizardIsTaskSelected('opencodedesktop')) and (not WizardIsTaskSelected('minimalvscode')))",
	} {
		if strings.Contains(s, notWant) {
			t.Fatalf("installer.iss must not bundle OpenCode Desktop installer; found %q", notWant)
		}
	}
}

func TestWindowsEnsureOpenCodeDesktopScriptDownloadsLatestInstaller(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-opencode-desktop.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"function Test-OpenCodeDesktopInstalled",
		"function Test-OpenCodeDesktopInstallerFile",
		"function Invoke-OpenCodeDesktopInstallerDownload",
		"function Invoke-OpenCodeDesktopDownloadedInstaller",
		"DisplayName -like 'OpenCode*'",
		"UninstallString",
		"Invoke-WebRequest",
		"Get-AuthenticodeSignature",
		"SignerCertificate",
		"OpenCode Desktop installer is not a valid MZ executable",
		"https://opencode.ai/download/stable/windows-x64-nsis",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-opencode-desktop.ps1 missing %q", want)
		}
	}
	for _, notWant := range []string{
		"Bundled OpenCode Desktop installer",
		"LocalInstallerPath",
		"winget",
		"O=Microsoft Corporation",
		"Microsoft Corporation",
		"Programs\\@opencode-aidesktop\\OpenCode.exe",
		"Wait-OpenCodeDesktopInstalled",
	} {
		if strings.Contains(s, notWant) {
			t.Fatalf("ensure-opencode-desktop.ps1 must not contain %q", notWant)
		}
	}
}

func TestWindowsEnsureOpenCodeDesktopScriptDoesNotTrustStaleProtocolRegistration(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-opencode-desktop.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if strings.Contains(s, "if (Test-Path $p) { return $true }") {
		t.Fatal("ensure-opencode-desktop.ps1 must not treat a bare opencode protocol key as an installed desktop app")
	}
	for _, want := range []string{
		"function Test-OpenCodeDesktopExecutable",
		"WaitForExit(10000)",
		"function Get-OpenCodeProtocolExePath",
		"Get-ItemProperty -LiteralPath $Path",
		"Test-OpenCodeDesktopExecutable $protocolExe",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-opencode-desktop.ps1 missing %q", want)
		}
	}
	for _, notWant := range []string{
		"if (Test-Path -LiteralPath $p) {\n            return $p",
		"if ($protocolExe -and (Test-Path -LiteralPath $protocolExe))",
	} {
		if strings.Contains(s, notWant) {
			t.Fatalf("ensure-opencode-desktop.ps1 must verify executable health before accepting install path; found %q", notWant)
		}
	}
}

func TestWindowsEnsureOpenCodeDesktopScriptRejectsUnsignedOpenCodeInstaller(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-opencode-desktop.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		`throw "OpenCode Desktop installer Authenticode signature is`,
		`throw "OpenCode Desktop installer has no signer certificate"`,
		"$sig.SignerCertificate.Subject",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-opencode-desktop.ps1 missing %q", want)
		}
	}
	for _, notWant := range []string{
		"Write-Warning \"OpenCode Desktop installer Authenticode signature is",
		"Write-Warning \"OpenCode Desktop installer has no signer certificate\"",
	} {
		if strings.Contains(s, notWant) {
			t.Fatalf("ensure-opencode-desktop.ps1 must fail, not warn, for unsigned OpenCode installers; found %q", notWant)
		}
	}
}

func TestWindowsEnsureOpenCodeDesktopScriptRunsDownloadedInstallerSilently(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-opencode-desktop.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"Start-Process -FilePath $installerPath -ArgumentList '/S' -Wait -PassThru",
		"Running downloaded OpenCode Desktop installer silently",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-opencode-desktop.ps1 missing %q", want)
		}
	}
}

func TestWindowsEnsureOpenCodeDesktopScriptUsesCurlForInstallerDownload(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-opencode-desktop.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"Get-Command 'curl.exe'",
		"& $curl.Source -fL",
		"--max-time 1200",
		"--speed-time 30",
		"--speed-limit 1024",
		"-o $partialPath",
		"Invoke-WebRequest -Uri $InstallerURL -OutFile $partialPath -UseBasicParsing -TimeoutSec 1200",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-opencode-desktop.ps1 missing %q", want)
		}
	}
}

func TestWindowsPackageScriptsUseSharedPayloadManifest(t *testing.T) {
	common, err := os.ReadFile("../../scripts/windows-package-common.sh")
	if err != nil {
		t.Fatal(err)
	}
	commonText := string(common)
	for _, want := range []string{
		"WINDOWS_PACKAGE_REQUIRED_FILES=(",
		"PORTABLE_PAYLOADS=(",
		"fetch_windows_package_assets()",
		"copy_portable_payloads()",
		"dist/windows/token-refresher.exe::token-refresher.exe",
		"packaging/windows/codex-manifest.json::codex-manifest.json",
		"$LOOM_DRIVER_CODEX_PROMPTS_CACHE::driver-codex-prompts.tar.gz",
	} {
		if !strings.Contains(commonText, want) {
			t.Fatalf("windows-package-common.sh missing %q", want)
		}
	}
	for _, path := range []string{
		"../../scripts/package-windows.sh",
		"../../scripts/package-windows-zip.sh",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s := string(body)
		for _, want := range []string{
			`source scripts/windows-package-common.sh`,
			`fetch_windows_package_assets`,
			`check_windows_package_required_files`,
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("%s missing shared manifest use %q", path, want)
			}
		}
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
			path: "../../scripts/windows-package-common.sh",
			want: []string{
				"packaging/windows/ensure-codex.ps1",
				"packaging/windows/codex-manifest.json",
			},
		},
		{
			path: "../../scripts/package-windows-zip.sh",
			want: []string{
				"source scripts/windows-package-common.sh",
				"copy_portable_payloads",
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

func TestWindowsInstallersDeleteObsoleteBundledPayloads(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
	}{
		{
			path: "../../packaging/windows/install.ps1",
			want: []string{
				"$obsoletePayloads = @(",
				"'codex.exe'",
				"('vscode-installer' + '.exe')",
				"('vscode-manifest' + '.json')",
				"Remove-Item -LiteralPath $obsoletePath -Force",
			},
		},
		{
			path: "../../packaging/windows/installer.iss",
			want: []string{
				"procedure DeleteObsoleteBundledPayloads();",
				"DeleteFile(ExpandConstant('{app}\\codex.exe'))",
				"DeleteFile(ExpandConstant('{app}\\vscode-installer' + '.exe'))",
				"DeleteFile(ExpandConstant('{app}\\vscode-manifest' + '.json'))",
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

func TestWindowsInstallScriptInstallsDriverSupportDuringInstall(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"install-driver-support.ps1",
		"& (Join-Path $InstallDir 'install-driver-support.ps1') -InstallDir $InstallDir",
		"[System.IO.Path]::GetFullPath($srcPath)",
		"[System.IO.Path]::GetFullPath($dstPath)",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("install.ps1 should install driver support during install; missing %q", want)
		}
	}
	copyIdx := strings.Index(s, `Write-Step "Copied $($required.Count) files."`)
	supportIdx := strings.Index(s, "& (Join-Path $InstallDir 'install-driver-support.ps1') -InstallDir $InstallDir")
	frontendIdx := strings.Index(s, "Writing install mode minimal_vscode")
	if copyIdx < 0 || supportIdx < 0 || frontendIdx < 0 {
		t.Fatalf("install.ps1 missing expected copy/support/frontend markers")
	}
	if supportIdx < copyIdx {
		t.Fatal("Install-DriverSupport must run after payload files are copied into the install directory")
	}
	if supportIdx > frontendIdx {
		t.Fatal("Install-DriverSupport should run before frontend setup so installed Codex sees fresh skills and AGENTS.md")
	}
}

func TestWindowsDriverSupportScriptInstallsSkillsAndConcisePrompt(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/install-driver-support.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"function Expand-SkillsArchive",
		"function Expand-SafeTarGzArchive",
		"function Assert-SafeTarArchive",
		"function Read-SkillsManifest",
		"function Write-SkillsManifest",
		"function Install-ManagedSkillFile",
		"function Read-DriverCodexPrompt",
		"function Merge-DriverCodexAgentsPrompt",
		"driver-superpower-skills.tar.gz",
		".agentserver-managed-skills.json",
		"Get-FileHash -Algorithm SHA256",
		"$oldHash -and ($currentHash -eq $oldHash)",
		"prompts-codex\\AGENTS.md",
		"ReadAllText($promptPath)",
		"agentserver-app loom driver prompt:start",
		"Assert-SafeTarArchive $ArchivePath",
		"& tar.exe -xzf $ArchivePath -C $Destination",
		".agents\\skills",
		".codex\\skills",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("install-driver-support.ps1 missing %q", want)
		}
	}
	for _, notWant := range []string{
		"$CodexDriverPrompt",
		"Copy-Item $_.FullName -Destination $DestRoot -Recurse -Force",
		"& tar.exe -xzf $ArchivePath -C $tmp",
	} {
		if strings.Contains(s, notWant) {
			t.Fatalf("install-driver-support.ps1 should not contain %q", notWant)
		}
	}
}

func TestWindowsDriverCodexPromptsPackageUsesConcisePrompt(t *testing.T) {
	promptPath := "../../packaging/windows/driver-codex-prompts/prompts-codex/AGENTS.md"
	body, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"# Agentserver Driver Workspace",
		"Use the `multiagent` skill",
		"`mcp_servers.driver`",
		"use the installed Superpower skills",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("driver Codex prompt source missing %q:\n%s", want, s)
		}
	}
	for _, notWant := range []string{
		"# Multi-Agent Driver",
		"## Core tools",
		"mcp__driver__list_agents",
		"## Permissions skill",
	} {
		if strings.Contains(s, notWant) {
			t.Fatalf("driver Codex prompt source still contains verbose Loom prompt %q:\n%s", notWant, s)
		}
	}

	out := filepath.Join(t.TempDir(), "driver-codex-prompts.tar.gz")
	python, err := exec.LookPath("python3")
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("python3 is not available in the Windows-target test environment")
		}
		t.Fatal(err)
	}
	cmd := exec.Command(python, "../../scripts/package-driver-codex-prompts.py", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("package-driver-codex-prompts.py: %v\n%s", err, output)
	}
	got := readTarGzEntry(t, out, "prompts-codex/AGENTS.md")
	if got != s {
		t.Fatalf("packaged prompt differs from source\nwant:\n%s\ngot:\n%s", s, got)
	}
}

func TestWindowsPackageScriptsBuildDriverCodexPromptsFromLocalConciseSource(t *testing.T) {
	for _, path := range []string{
		"../../scripts/windows-package-common.sh",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s := string(body)
		want := `python3 scripts/package-driver-codex-prompts.py "$LOOM_DRIVER_CODEX_PROMPTS_CACHE"`
		if !strings.Contains(s, want) {
			t.Fatalf("%s should build driver-codex-prompts.tar.gz from the local concise prompt; missing %q", path, want)
		}
		notWant := `download_loom_asset "$LOOM_DRIVER_CODEX_PROMPTS_ASSET"`
		if strings.Contains(s, notWant) {
			t.Fatalf("%s should not package the verbose upstream Loom prompt archive directly", path)
		}
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

func TestWindowsInnoInstallerInstallsDriverSupportBeforeFrontend(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		`Source: "install-driver-support.ps1"; DestDir: "{app}"; Flags: ignoreversion`,
		"RunEstimatedPowerShellStep('driver-support'",
		"install-driver-support.ps1",
		"-InstallDir",
		"正在安装 driver skills 和 Codex 指令",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("installer.iss should install driver support during install; missing %q", want)
		}
	}
	runtime := strings.Index(s, "RunEstimatedPowerShellStep('codex-runtime'")
	support := strings.Index(s, "RunEstimatedPowerShellStep('driver-support'")
	codexFrontend := strings.Index(s, "RunEstimatedPowerShellStep('codex-mode'")
	vscodeFrontend := strings.Index(s, "RunEstimatedPowerShellStep('vscode-mode'")
	if runtime < 0 || support < 0 || codexFrontend < 0 || vscodeFrontend < 0 {
		t.Fatal("installer.iss missing codex runtime/support/frontend markers")
	}
	if runtime > support || support > codexFrontend || support > vscodeFrontend {
		t.Fatal("installer.iss must install driver support after preparing Codex runtime and before mode-specific frontend setup")
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
		"../../packaging/windows/ensure-opencode-desktop.ps1",
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

func TestEnsureCodexDesktopDetectionUsesExactPackageFamily(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-codex-desktop.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, "$_.PackageFamilyName -eq 'OpenAI.Codex_2p2nqsd0c76g0'") {
		t.Fatalf("ensure-codex-desktop.ps1 should match the exact Codex PackageFamilyName:\n%s", s)
	}
	for _, notWant := range []string{"$_.Name -like '*Codex*'", "$_.PackageFullName -like '*Codex*'"} {
		if strings.Contains(s, notWant) {
			t.Fatalf("ensure-codex-desktop.ps1 should not use fuzzy Codex appx matching %q:\n%s", notWant, s)
		}
	}
}

func TestEnsureCodexDesktopScriptFallsBackToWingetWhenBundledInstallerFails(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-codex-desktop.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if strings.Contains(s, "$p.ExitCode -eq 1612") {
		t.Fatal("ensure-codex-desktop.ps1 must not treat exit code 1612 as a successful Microsoft Store handoff")
	}
	for _, want := range []string{
		"try {",
		"Start-Process -FilePath $LocalInstallerPath -Wait -PassThru",
		"} catch {",
		"Bundled Codex Desktop installer failed verification or startup",
		"Bundled Codex Desktop installer failed with exit code",
		"falling back to winget",
		"return $false",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-codex-desktop.ps1 missing %q", want)
		}
	}
}

func TestWindowsPackageScriptsRefreshCodexDesktopInstallerEveryBuild(t *testing.T) {
	body, err := os.ReadFile("../../scripts/windows-package-common.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	fetch := strings.Index(s, "Fetching Codex Desktop installer")
	if fetch < 0 {
		t.Fatal("windows-package-common.sh missing Codex Desktop installer download block")
	}
	if strings.Contains(s[:fetch], `if [[ ! -f "$CODEX_DESKTOP_CACHE" ]]`) {
		t.Fatal("Codex Desktop installer must refresh every build, not skip download when cache exists")
	}
	for _, want := range []string{
		`CODEX_DESKTOP_MIN_SIZE=`,
		`verify_codex_desktop_installer()`,
		`head -c 2 "$path"`,
		`[[ "$magic" == "MZ" ]]`,
		`codex_desktop_tmp=$(mktemp "$CODEX_DESKTOP_CACHE.part.XXXXXX")`,
		`curl --fail --location --retry 2 --retry-delay 2 --output "$codex_desktop_tmp" "$CODEX_DESKTOP_URL"`,
		`if ! verify_codex_desktop_installer "$codex_desktop_tmp"; then`,
		`ERROR: invalid Codex Desktop installer download`,
		`mv -f "$codex_desktop_tmp" "$CODEX_DESKTOP_CACHE"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("windows-package-common.sh should refresh Codex Desktop installer; missing %q", want)
		}
	}
	if strings.Contains(s, `rm -f "$CODEX_DESKTOP_CACHE"`) {
		t.Fatal("windows-package-common.sh should not remove the shared Codex Desktop cache before publishing a verified replacement")
	}
	for _, path := range []string{
		"../../scripts/package-windows.sh",
		"../../scripts/package-windows-zip.sh",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `fetch_windows_package_assets`) {
			t.Fatalf("%s should call shared Codex Desktop refresh", path)
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
		return Detected{Installed: true, Path: "/x/code", Version: "1.85.0"}, nil
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
		return Detected{Installed: true, Path: "/x/code", Version: "1.85.0"}, nil
	}
	det, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err != nil {
		t.Fatalf("expected fallback success, got: %v", err)
	}
	if det.Path != "/x/code" || det.Version != "1.85.0" {
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
	if strings.Contains(err.Error(), "1.96.0") {
		t.Errorf("Store bootstrapper error should not mention a locked VS Code version: %v", err)
	}
}

func TestInstallAndDetect_InstallFails_DetectFindsAnyVersion(t *testing.T) {
	// Store bootstrapper installs the current Store version; if post-install
	// detection finds VS Code, accept that instead of requiring a pinned build.
	install := func(context.Context, string, InstallPlan) error {
		return errors.New("exit status 0xc0000409")
	}
	detect := func() (Detected, error) {
		return Detected{Installed: true, Path: "/x/code", Version: "1.85.0"}, nil
	}
	det, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err != nil {
		t.Fatalf("expected fallback success with detected VS Code, got: %v", err)
	}
	if det.Path != "/x/code" || det.Version != "1.85.0" {
		t.Errorf("got %+v", det)
	}
}

func readTarGzEntry(t *testing.T, archivePath, entryName string) string {
	t.Helper()
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			t.Fatalf("%s not found in %s", entryName, archivePath)
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name != entryName {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}
