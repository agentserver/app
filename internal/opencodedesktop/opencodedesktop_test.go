package opencodedesktop

import (
	"context"
	"os/exec"
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
