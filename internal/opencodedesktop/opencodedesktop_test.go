package opencodedesktop

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestParseDetectionOutput(t *testing.T) {
	out := `{"installed":true,"path":"C:\\Users\\alice\\AppData\\Local\\Programs\\OpenCode\\OpenCode.exe","version":"1.2.3"}`
	got, err := parseDetectOutput([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Installed || got.Path == "" || got.Version != "1.2.3" {
		t.Fatalf("detected = %+v", got)
	}
}

func TestEnsureInstalledUsesDetectFastPath(t *testing.T) {
	calledInstall := false
	got, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			return Detected{Installed: true, Path: `C:\OpenCode\OpenCode.exe`, Version: "1.2.3"}, nil
		},
		RunInstaller: func(context.Context) error {
			calledInstall = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calledInstall {
		t.Fatal("installer should not run on fast path")
	}
	if got.Version != "1.2.3" {
		t.Fatalf("got = %+v", got)
	}
}

func TestEnsureInstalledDetectsAfterInstaller(t *testing.T) {
	calls := 0
	got, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			calls++
			if calls == 1 {
				return Detected{Installed: false}, ErrNotFound
			}
			return Detected{Installed: true, Path: `C:\OpenCode\OpenCode.exe`, Version: "1.2.3"}, nil
		},
		RunInstaller: func(context.Context) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("detect calls = %d, want 2", calls)
	}
	if got.Path == "" {
		t.Fatalf("got = %+v", got)
	}
}

func TestEnsureInstalledUsesEnglishDetectionErrors(t *testing.T) {
	calls := 0
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			calls++
			return Detected{Installed: false}, ErrNotFound
		},
		RunInstaller: func(context.Context) error {
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 2 {
		t.Fatalf("detect calls = %d, want 2", calls)
	}
	if !strings.Contains(err.Error(), "OpenCode Desktop was not detected after installation") {
		t.Fatalf("error = %q", err)
	}
	if strings.Contains(err.Error(), "安装") {
		t.Fatalf("OpenCode Desktop errors should not mix Chinese and English: %q", err)
	}
}

func TestLaunchUsesDetectedExecutableAndFolderWorkingDirectory(t *testing.T) {
	var gotName string
	var gotDir string
	runner := func(cmd *exec.Cmd) error {
		gotName = cmd.Path
		gotDir = cmd.Dir
		return nil
	}
	err := Launch(context.Background(), LaunchOptions{
		Detected: Detected{Installed: true, Path: `C:\OpenCode\OpenCode.exe`},
		Folder:   `C:\work\repo`,
		Run:      runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotName != `C:\OpenCode\OpenCode.exe` || gotDir != `C:\work\repo` {
		t.Fatalf("launch path=%q dir=%q", gotName, gotDir)
	}
}

func TestLaunchInjectsOpenCodeConfigPathAndAPIKeyEnv(t *testing.T) {
	var gotEnv []string
	err := Launch(context.Background(), LaunchOptions{
		Detected: Detected{Installed: true, Path: `C:\OpenCode\OpenCode.exe`},
		Config: ConfigEnv{
			Path:      `C:\Users\alice\.config\opencode\opencode.jsonc`,
			APIKeyEnv: "AGENTSERVER_LOCAL_MODEL_PROXY_API_KEY",
			APIKey:    "local-proxy-token",
		},
		Run: func(cmd *exec.Cmd) error {
			gotEnv = cmd.Env
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`OPENCODE_CONFIG=C:\Users\alice\.config\opencode\opencode.jsonc`,
		"AGENTSERVER_LOCAL_MODEL_PROXY_API_KEY=local-proxy-token",
	} {
		if !containsEnv(gotEnv, want) {
			t.Fatalf("launch env missing %q in %#v", want, gotEnv)
		}
	}
}

func TestLaunchUsesDetectedExecutableEvenWithoutFolder(t *testing.T) {
	runnerCalled := false
	var gotName string
	var gotURL string
	err := Launch(context.Background(), LaunchOptions{
		Detected: Detected{Installed: true, Path: `C:\OpenCode\OpenCode.exe`},
		Run: func(cmd *exec.Cmd) error {
			runnerCalled = true
			gotName = cmd.Path
			return nil
		},
		OpenURL: func(url string) error {
			gotURL = url
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !runnerCalled {
		t.Fatal("launch should use detected executable when available")
	}
	if gotName != `C:\OpenCode\OpenCode.exe` {
		t.Fatalf("launch path=%q", gotName)
	}
	if gotURL != "" {
		t.Fatalf("protocol fallback should not run when executable exists; url=%q", gotURL)
	}
}

func TestLaunchFallsBackToProtocol(t *testing.T) {
	var gotURL string
	err := Launch(context.Background(), LaunchOptions{
		Detected: Detected{Installed: true},
		OpenURL: func(url string) error {
			gotURL = url
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotURL != "opencode://" {
		t.Fatalf("url = %q", gotURL)
	}
}

func TestWindowsDetectionScriptMatchesRealOpenCodeInstallLayout(t *testing.T) {
	body, err := os.ReadFile("detect_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		`Programs\OpenCode\OpenCode.exe`,
		`DisplayName -like 'OpenCode*'`,
		`UninstallString`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("detect_windows.go missing %q", want)
		}
	}
	if strings.Contains(s, `DisplayName -eq 'OpenCode'`) {
		t.Fatal("detect_windows.go must not require exact DisplayName 'OpenCode'")
	}
	if strings.Contains(s, `Programs\@opencode-aidesktop\OpenCode.exe`) {
		t.Fatal("detect_windows.go must not contain the stale @opencode-aidesktop candidate")
	}
}

func TestWindowsDetectionScriptDoesNotTrustStaleProtocolRegistration(t *testing.T) {
	body, err := os.ReadFile("detect_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if strings.Contains(s, "if (Test-Path $scheme) {\n    Emit $true '' ''") {
		t.Fatal("detect_windows.go must not treat a bare opencode protocol key as an installed desktop app")
	}
	for _, want := range []string{
		"function Get-OpenCodeProtocolExePath",
		"Get-ItemProperty -LiteralPath $scheme",
		"Test-Path -LiteralPath $protocolExe",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("detect_windows.go missing %q", want)
		}
	}
}

func TestWindowsDetectionSeparatesStderrFromJSONOutput(t *testing.T) {
	body, err := os.ReadFile("detect_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"var stderr bytes.Buffer",
		"cmd.Stderr = &stderr",
		"cmd.Output()",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("detect_windows.go missing %q", want)
		}
	}
	if strings.Contains(s, "CombinedOutput()") {
		t.Fatal("detect_windows.go must not mix PowerShell stderr into JSON stdout")
	}
}

func TestRunLocalInstallerValidatesAuthenticodeSignatureBeforeExecuting(t *testing.T) {
	var order []string
	err := runLocalInstallerWithDeps(
		context.Background(),
		`C:\OpenCode Desktop Installer.exe`,
		"windows",
		func(context.Context, string) error {
			order = append(order, "validate")
			return nil
		},
		func(context.Context, string) error {
			order = append(order, "run")
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "validate,run" {
		t.Fatalf("order = %v, want validate before run", order)
	}

	windowsBody, err := os.ReadFile("install_authenticode_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	windowsSource := string(windowsBody)
	for _, want := range []string{
		"Get-AuthenticodeSignature",
		"OpenCode Desktop installer Authenticode signature is",
		"signer subject",
	} {
		if !strings.Contains(windowsSource, want) {
			t.Fatalf("install_authenticode_windows.go missing %q", want)
		}
	}
}

func containsEnv(env []string, want string) bool {
	for _, got := range env {
		if got == want {
			return true
		}
	}
	return false
}
