package updater

import (
	"os"
	"strings"
	"testing"
)

func TestDownloadAndStartUsesBackgroundContextForDefaultInstaller(t *testing.T) {
	body, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	if !strings.Contains(source, "start = StartInstaller") {
		t.Fatalf("service.go should use package StartInstaller as the default:\n%s", source)
	}
	if !strings.Contains(source, "startContext = context.Background()") {
		t.Fatalf("service.go should detach default installer startup from caller context:\n%s", source)
	}
}
