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
