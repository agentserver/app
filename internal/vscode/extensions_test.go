package vscode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallExtensions_RecordsCalls(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	codeExe := filepath.Join(dir, "code")
	logFile := filepath.Join(dir, "calls.log")
	script := "#!/bin/bash\necho \"$@\" >> " + logFile + "\n"
	if err := os.WriteFile(codeExe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	err := InstallExtensions(context.Background(), Installer{
		CodeExe:       codeExe,
		UserDataDir:   filepath.Join(dir, "data"),
		ExtensionsDir: filepath.Join(dir, "ext"),
		Extensions:    []string{"MS-CEINTL.vscode-language-pack-zh-hans", "/tmp/our.vsix"},
	})
	if err != nil {
		t.Fatal(err)
	}
	logged, _ := os.ReadFile(logFile)
	s := string(logged)
	if !strings.Contains(s, "MS-CEINTL.vscode-language-pack-zh-hans") {
		t.Errorf("missing zh pack call: %s", s)
	}
	if !strings.Contains(s, "/tmp/our.vsix") {
		t.Errorf("missing vsix call: %s", s)
	}
	if !strings.Contains(s, "--user-data-dir") {
		t.Errorf("missing --user-data-dir: %s", s)
	}
}
