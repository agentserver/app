package codexdesktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigureLocaleWritesCodexDesktopLanguageFiles(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, ".codex", ".codex-global-state.json")
	computerUsePath := filepath.Join(dir, ".codex", "computer-use", "config.json")

	if err := ConfigureLocale(globalPath, computerUsePath, "zh-CN"); err != nil {
		t.Fatalf("ConfigureLocale: %v", err)
	}

	global := readJSONFile(t, globalPath)
	if got := global["localeOverride"]; got != "zh-CN" {
		t.Fatalf("global localeOverride=%v, want zh-CN", got)
	}

	computerUse := readJSONFile(t, computerUsePath)
	if got := computerUse["locale"]; got != "zh-CN" {
		t.Fatalf("computer-use locale=%v, want zh-CN", got)
	}
}

func TestConfigureLocalePreservesExistingJSONFields(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, ".codex", ".codex-global-state.json")
	computerUsePath := filepath.Join(dir, ".codex", "computer-use", "config.json")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte(`{"active-workspace-roots":["C:\\Work"],"localeOverride":"en-US"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(computerUsePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(computerUsePath, []byte(`{"accentColor":"#339cff","locale":"en-US","strings":{"usingComputer":"Codex is using your computer"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigureLocale(globalPath, computerUsePath, "zh-CN"); err != nil {
		t.Fatalf("ConfigureLocale: %v", err)
	}

	global := readJSONFile(t, globalPath)
	if got := global["localeOverride"]; got != "zh-CN" {
		t.Fatalf("global localeOverride=%v, want zh-CN", got)
	}
	if _, ok := global["active-workspace-roots"]; !ok {
		t.Fatalf("global state lost active-workspace-roots: %#v", global)
	}

	computerUse := readJSONFile(t, computerUsePath)
	if got := computerUse["locale"]; got != "zh-CN" {
		t.Fatalf("computer-use locale=%v, want zh-CN", got)
	}
	if _, ok := computerUse["strings"]; !ok {
		t.Fatalf("computer-use config lost strings: %#v", computerUse)
	}
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, b)
	}
	return out
}
