package codexdesktop

import (
	"context"
	"errors"
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

func TestEnsureInstalledClassifiesWingetFailure(t *testing.T) {
	_, err := EnsureInstalled(context.Background(), Options{
		Detect: func() (Detected, error) { return Detected{Installed: false}, nil },
		RunWinget: func(context.Context, []string) (string, error) {
			return "source msstore was not found", errors.New("exit 1")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "Microsoft Store source") {
		t.Fatalf("err=%v", err)
	}
}
