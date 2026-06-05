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
				"ensure-vscode.ps1",
				"vscode-manifest.json",
				"uninstall.exe",
				"Ensuring VS Code is installed",
				"$AppDisplayName = '星池指挥官'",
				"LegacyDesktopLnk",
				"Desktop\\$AppDisplayName.lnk",
				"Software\\Classes\\*\\shell\\AgentserverVscode",
				"用 星池指挥官 打开",
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
			name: "package-windows-zip.sh",
			path: "../../scripts/package-windows-zip.sh",
			want: []string{
				"VSCODE_CACHE",
				"vscode-installer.exe",
				"packaging/windows/ensure-vscode.ps1",
				"packaging/windows/vscode-manifest.json",
				"dist/windows/uninstall.exe",
				"dist/windows/token-refresher.exe",
				"cp \"$VSCODE_CACHE\"",
				"cp packaging/windows/ensure-vscode.ps1",
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
				"ensure-vscode.ps1",
				"vscode-manifest.json",
				"powershell",
				"ensure-vscode.ps1",
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
