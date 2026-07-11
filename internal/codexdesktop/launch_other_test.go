//go:build !windows

package codexdesktop

import (
	"context"
	"testing"
)

func TestLaunchOtherRetainsBestEffortProtocolOpen(t *testing.T) {
	original := openOtherProtocol
	t.Cleanup(func() { openOtherProtocol = original })

	var opened string
	openOtherProtocol = func(_ context.Context, rawURL string) error {
		opened = rawURL
		return nil
	}
	if err := Launch(context.Background(), `C:\Project`); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if opened != ThreadURL(`C:\Project`) {
		t.Fatalf("opened=%q, want %q", opened, ThreadURL(`C:\Project`))
	}
}
