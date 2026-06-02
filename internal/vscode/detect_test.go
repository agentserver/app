package vscode

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1.96.0\nabcdef\nx64\n", "1.96.0"},
		{"  1.85.2  ", "1.85.2"},
		{"", ""},
	}
	for _, c := range cases {
		if got := parseVersion(c.in); got != c.want {
			t.Errorf("parseVersion(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestDetect_FakeExe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "code")
	script := "#!/bin/bash\necho 1.96.0\necho abcdef\necho x64\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	det, err := detectAt(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !det.Installed || det.Version != "1.96.0" || det.Path != exe {
		t.Errorf("got %+v", det)
	}
}
