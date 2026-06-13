package main

import (
	"os"
	"strings"
	"testing"
)

func TestRunUninstallWiresVSCodeFlagIntoUninstallOptions(t *testing.T) {
	body, err := os.ReadFile("cmd_uninstall.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	if !strings.Contains(source, "RemoveVSCode: *removeVSCode") {
		t.Fatalf("runUninstall should pass --vscode into uninstall.Options:\n%s", source)
	}
	if strings.Contains(source, "removal not implemented") {
		t.Fatalf("runUninstall still contains --vscode stub text:\n%s", source)
	}
}
