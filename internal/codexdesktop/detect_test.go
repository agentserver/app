package codexdesktop

import (
	"errors"
	"os"
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

func TestWindowsDetectUsesExactPackageFamily(t *testing.T) {
	body, err := os.ReadFile("detect_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	if !strings.Contains(source, "$_.PackageFamilyName -eq 'OpenAI.Codex_2p2nqsd0c76g0'") {
		t.Fatalf("detect_windows.go should match exact Codex PackageFamilyName:\n%s", source)
	}
	for _, notWant := range []string{"$_.Name -like '*Codex*'", "$_.PackageFullName -like '*Codex*'"} {
		if strings.Contains(source, notWant) {
			t.Fatalf("detect_windows.go should not use fuzzy Appx matching %q:\n%s", notWant, source)
		}
	}
}
