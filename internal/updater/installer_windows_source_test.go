package updater

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsStartInstallerDetachesFromCallerContext(t *testing.T) {
	body, err := os.ReadFile("installer_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	if strings.Contains(source, "exec.CommandContext") {
		t.Fatalf("installer_windows.go should not tie installer process lifetime to caller context:\n%s", source)
	}
	for _, want := range []string{
		"ctx.Err()",
		"exec.Command(",
		"process.HideWindow(cmd)",
		"cmd.Start()",
		"cmd.Process.Release()",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("installer_windows.go should contain %q:\n%s", want, source)
		}
	}
}
