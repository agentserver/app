package paths

import (
	"path/filepath"
	"testing"
)

func TestDefaultIncludesOpenCodeConfigPath(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p.OpenCodeConfigDir) != "opencode" {
		t.Fatalf("OpenCodeConfigDir = %q", p.OpenCodeConfigDir)
	}
	if filepath.Base(p.OpenCodeConfigFile) != "opencode.jsonc" {
		t.Fatalf("OpenCodeConfigFile = %q", p.OpenCodeConfigFile)
	}
}
