package browser

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsBrowserOpenHidesHelperWindow(t *testing.T) {
	body, err := os.ReadFile("open_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "process.HideWindow(cmd)") {
		t.Fatalf("open_windows.go should hide the rundll32 helper window:\n%s", body)
	}
}

func TestWindowsBrowserOpenWaitsForHelperWithContext(t *testing.T) {
	body, err := os.ReadFile("open_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{
		"windows.GetSystemDirectory",
		"filepath.IsAbs",
		"filepath.Join",
		"exec.CommandContext",
		`"rundll32.exe"`,
		`"url.dll"`,
		`urlDLL+",FileProtocolHandler"`,
		"cmd.Run()",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("open_windows.go missing %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "cmd.Start()") {
		t.Fatalf("open_windows.go must wait for the Shell helper instead of reporting Start success:\n%s", source)
	}
	if strings.Contains(source, `exec.CommandContext(ctx, "rundll32`) {
		t.Fatalf("open_windows.go must not resolve rundll32 through PATH:\n%s", source)
	}
	if strings.Contains(source, `"url.dll,FileProtocolHandler"`) {
		t.Fatalf("open_windows.go must not let rundll32 resolve url.dll through DLL search order:\n%s", source)
	}
}
