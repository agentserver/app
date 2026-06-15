package codexdesktop

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureInstalledSkipsWingetWhenDetected(t *testing.T) {
	calls := 0
	det, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			return Detected{Installed: true, Version: "1.0.0"}, nil
		},
		RunWinget: func(context.Context, []string) (string, error) {
			calls++
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if !det.Installed || det.Version != "1.0.0" {
		t.Fatalf("det=%+v", det)
	}
	if calls != 0 {
		t.Fatalf("winget called %d times", calls)
	}
}

func TestEnsureInstalledRunsWingetThenVerifies(t *testing.T) {
	detectCalls := 0
	var gotArgs []string
	det, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			detectCalls++
			if detectCalls == 1 {
				return Detected{Installed: false}, nil
			}
			return Detected{Installed: true, Version: "2.0.0"}, nil
		},
		RunWinget: func(_ context.Context, args []string) (string, error) {
			gotArgs = append([]string(nil), args...)
			return "installed", nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if !det.Installed || det.Version != "2.0.0" {
		t.Fatalf("det=%+v", det)
	}
	if strings.Join(gotArgs, " ") != "install Codex -s msstore --accept-source-agreements --accept-package-agreements" {
		t.Fatalf("args=%v", gotArgs)
	}
}

func TestEnsureInstalledReturnsInitialDetectError(t *testing.T) {
	detectErr := errors.New("registry unavailable")
	calls := 0
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			return Detected{}, detectErr
		},
		RunWinget: func(context.Context, []string) (string, error) {
			calls++
			return "", nil
		},
	})
	if !errors.Is(err, detectErr) {
		t.Fatalf("err=%v, want detect error", err)
	}
	if calls != 0 {
		t.Fatalf("winget called %d times", calls)
	}
}

func TestEnsureInstalledClassifiesWingetFailure(t *testing.T) {
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) { return Detected{Installed: false}, nil },
		RunWinget: func(context.Context, []string) (string, error) {
			return "source msstore was not found", errors.New("exit 1")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "microsoft store source") {
		t.Fatalf("err=%v", err)
	}
}

func TestEnsureInstalledSurfacesPostInstallDetectError(t *testing.T) {
	detectCalls := 0
	detectErr := errors.New("post-install registry read failed")
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			detectCalls++
			if detectCalls == 1 {
				return Detected{Installed: false}, ErrNotFound
			}
			return Detected{}, detectErr
		},
		RunWinget: func(context.Context, []string) (string, error) {
			return "installed output", nil
		},
	})
	if !errors.Is(err, detectErr) {
		t.Fatalf("err=%v, want post-install detect error", err)
	}
	if !strings.Contains(err.Error(), "installed output") {
		t.Fatalf("err=%v, want winget output", err)
	}
}

func TestEnsureInstalledPostInstallErrNotFoundWrapsSentinel(t *testing.T) {
	detectCalls := 0
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			detectCalls++
			if detectCalls == 1 {
				return Detected{Installed: false}, ErrNotFound
			}
			return Detected{Installed: false}, ErrNotFound
		},
		RunWinget: func(context.Context, []string) (string, error) {
			return "installed output", nil
		},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "installed output") {
		t.Fatalf("err=%v, want winget output", err)
	}
}

func TestEnsureInstalledPostInstallInstalledFalseWrapsSentinel(t *testing.T) {
	detectCalls := 0
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			detectCalls++
			if detectCalls == 1 {
				return Detected{Installed: false}, ErrNotFound
			}
			return Detected{Installed: false}, nil
		},
		RunWinget: func(context.Context, []string) (string, error) {
			return "installed output", nil
		},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "installed output") {
		t.Fatalf("err=%v, want winget output", err)
	}
}

func TestEnsureInstalledUsesInjectedInstallOverWinget(t *testing.T) {
	installCalls := 0
	wingetCalls := 0
	detectCalls := 0
	det, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			detectCalls++
			if detectCalls == 1 {
				return Detected{Installed: false}, nil
			}
			return Detected{Installed: true, Version: "3.0.0"}, nil
		},
		Install: func(context.Context) error {
			installCalls++
			return nil
		},
		RunWinget: func(context.Context, []string) (string, error) {
			wingetCalls++
			return "should not be used", nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if !det.Installed || det.Version != "3.0.0" {
		t.Fatalf("det=%+v", det)
	}
	if installCalls != 1 {
		t.Fatalf("Install called %d times, want 1", installCalls)
	}
	if wingetCalls != 0 {
		t.Fatalf("RunWinget called %d times, want 0 (Install takes precedence)", wingetCalls)
	}
}

// The default dispatcher (installDesktopPlatform) returns ErrUnsupportedPlatform
// only on non-desktop platforms. On darwin it would run the real dmg installer
// (network + hdiutil), so guard this to linux only.
func TestEnsureInstalledDefaultUnsupportedOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only: default dispatch returns ErrUnsupportedPlatform")
	}
	_, err := EnsureInstalled(context.Background(), Options{})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("err=%v, want unsupported platform", err)
	}
}
