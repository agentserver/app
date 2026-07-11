package codexdesktop

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func readyInstallDetection(version string) Detected {
	return Detected{
		Installed:         true,
		Version:           version,
		Status:            StatusReady,
		PackageFamilyName: ChatGPTPackageFamily,
		InstallLocation:   `C:\Program Files\WindowsApps\ChatGPT`,
		AppUserModelID:    ChatGPTPackageFamily + "!ChatGPT",
		SchemeRegistered:  true,
		SchemeTargetValid: true,
	}
}

func TestEnsureInstalledSkipsWingetWhenDetected(t *testing.T) {
	calls := 0
	det, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			return readyInstallDetection("1.0.0"), nil
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

func TestEnsureInstalledRejectsInconsistentReadyDetectorResult(t *testing.T) {
	calls := 0
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			return Detected{
				Installed:         true,
				Status:            StatusReady,
				PackageFamilyName: ChatGPTPackageFamily,
				InstallLocation:   `C:\Program Files\WindowsApps\ChatGPT`,
				SchemeRegistered:  true,
				SchemeTargetValid: true,
			}, nil
		},
		RunWinget: func(context.Context, []string) (string, error) {
			calls++
			return "", nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "AppUserModelID") {
		t.Fatalf("err=%v, want invalid ready AppUserModelID rejection", err)
	}
	if calls != 0 {
		t.Fatalf("winget called %d times for inconsistent ready detection", calls)
	}
}

func TestEnsureInstalledRejectsInconsistentPostInstallReadyResult(t *testing.T) {
	detectCalls := 0
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			detectCalls++
			if detectCalls == 1 {
				return Detected{Status: StatusNotInstalled}, ErrNotFound
			}
			return Detected{
				Installed:         true,
				Status:            StatusReady,
				PackageFamilyName: ChatGPTPackageFamily,
				InstallLocation:   `C:\Program Files\WindowsApps\ChatGPT`,
				SchemeRegistered:  true,
				SchemeTargetValid: true,
			}, nil
		},
		RunWinget: func(context.Context, []string) (string, error) {
			return "installed", nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "AppUserModelID") {
		t.Fatalf("err=%v, want invalid post-install ready AppUserModelID rejection", err)
	}
}

func TestEnsureInstalledRunsWingetThenVerifies(t *testing.T) {
	detectCalls := 0
	var gotArgs []string
	det, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			detectCalls++
			if detectCalls == 1 {
				return Detected{Status: StatusNotInstalled}, ErrNotFound
			}
			return readyInstallDetection("2.0.0"), nil
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
	if strings.Join(gotArgs, " ") != "install --id=9NT1R1C2HH7J --source=msstore --exact --accept-package-agreements --accept-source-agreements --disable-interactivity" {
		t.Fatalf("args=%v", gotArgs)
	}
}

func TestEnsureInstalledDoesNotInstallOverBrokenScheme(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status Status
		err    error
	}{
		{name: "missing", status: StatusSchemeMissing, err: ErrSchemeMissing},
		{name: "invalid", status: StatusSchemeTargetInvalid, err: ErrSchemeTargetInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			_, err := EnsureInstalled(context.Background(), Options{
				Detect: func() (Detected, error) {
					return Detected{Installed: true, Status: tc.status}, tc.err
				},
				RunWinget: func(context.Context, []string) (string, error) {
					calls++
					return "", nil
				},
			})
			if !errors.Is(err, tc.err) {
				t.Fatalf("err=%v, want %v", err, tc.err)
			}
			for _, want := range []string{"Repair", "Reset", "Reinstall"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err=%v, want guidance %q", err, want)
				}
			}
			if calls != 0 {
				t.Fatalf("winget called %d times", calls)
			}
		})
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
		Detect: func() (Detected, error) { return Detected{Status: StatusNotInstalled}, ErrNotFound },
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
				return Detected{Status: StatusNotInstalled}, ErrNotFound
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
				return Detected{Status: StatusNotInstalled}, ErrNotFound
			}
			return Detected{Status: StatusNotInstalled}, ErrNotFound
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

func TestEnsureInstalledPostInstallBrokenSchemeWrapsSentinel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status Status
		err    error
	}{
		{name: "missing", status: StatusSchemeMissing, err: ErrSchemeMissing},
		{name: "invalid", status: StatusSchemeTargetInvalid, err: ErrSchemeTargetInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detectCalls := 0
			_, err := EnsureInstalled(context.Background(), Options{
				Detect: func() (Detected, error) {
					detectCalls++
					if detectCalls == 1 {
						return Detected{Status: StatusNotInstalled}, ErrNotFound
					}
					return Detected{Installed: true, Status: tc.status}, tc.err
				},
				RunWinget: func(context.Context, []string) (string, error) {
					return "installed output", nil
				},
			})
			if !errors.Is(err, tc.err) {
				t.Fatalf("err=%v, want %v", err, tc.err)
			}
			if !strings.Contains(err.Error(), "installed output") {
				t.Fatalf("err=%v, want winget output", err)
			}
		})
	}
}

func TestEnsureInstalledPostInstallInstalledFalseWrapsSentinel(t *testing.T) {
	detectCalls := 0
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) {
			detectCalls++
			if detectCalls == 1 {
				return Detected{Status: StatusNotInstalled}, ErrNotFound
			}
			return Detected{Status: StatusNotInstalled}, nil
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

func TestEnsureInstalledDefaultUnsupportedOnNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows behavior")
	}
	_, err := EnsureInstalled(context.Background(), Options{})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("err=%v, want unsupported platform", err)
	}
}
