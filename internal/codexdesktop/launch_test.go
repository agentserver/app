package codexdesktop

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestThreadURLWithoutFolder(t *testing.T) {
	if got := ThreadURL(""); got != "codex://threads/new" {
		t.Fatalf("ThreadURL empty = %q", got)
	}
}

func TestThreadURLWithFolder(t *testing.T) {
	got := ThreadURL(`C:\Users\Test User\Project`)
	if !strings.HasPrefix(got, "codex://threads/new?path=") {
		t.Fatalf("url=%q", got)
	}
	if !strings.Contains(got, "Test+User") {
		t.Fatalf("folder path not encoded: %q", got)
	}
}

func TestThreadURLRoundTripWindowsPath(t *testing.T) {
	folder := `C:\Users\Test User\Project`
	parsed, err := url.Parse(ThreadURL(folder))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := parsed.Query().Get("path"); got != folder {
		t.Fatalf("path=%q want %q", got, folder)
	}
}

func TestLaunchUsesOpener(t *testing.T) {
	var opened string
	err := Launch(context.Background(), `C:\Project`, func(url string) error {
		opened = url
		return nil
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !strings.HasPrefix(opened, "codex://threads/new?path=") {
		t.Fatalf("opened=%q", opened)
	}
}

func TestLaunchHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := Launch(ctx, `C:\Project`, func(string) error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if called {
		t.Fatal("opener called for canceled context")
	}
}
