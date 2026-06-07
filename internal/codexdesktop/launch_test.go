package codexdesktop

import (
	"context"
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
