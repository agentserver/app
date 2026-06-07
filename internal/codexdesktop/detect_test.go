package codexdesktop

import (
	"errors"
	"strings"
	"testing"
)

func TestDetectedFromPowerShellOutputNotFoundSentinel(t *testing.T) {
	det, err := detectedFromPowerShellOutput([]byte(detectNotFoundSentinel+"\r\n"), errors.New("exit 1"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
	if det.Installed {
		t.Fatalf("det=%+v, want not installed", det)
	}
}

func TestDetectedFromPowerShellOutputWrapsOperationalFailure(t *testing.T) {
	runErr := errors.New("powershell failed")
	_, err := detectedFromPowerShellOutput([]byte("access denied"), runErr)
	if !errors.Is(err, runErr) {
		t.Fatalf("err=%v, want run error", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, should not be ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("err=%v, want command output", err)
	}
}
