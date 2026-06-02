package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateConfig_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := UpdateConfig(path, Settings{
		Provider: "modelserver", Model: "gpt-5.5",
		BaseURL: "https://code.ai.cs.ac.cn/v1", EnvKey: "OPENAI_API_KEY",
		WireAPI: "responses",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`model = "gpt-5.5"`,
		`[model_providers.modelserver]`,
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestUpdateConfig_MergeKeepsOtherProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	prior := `model_provider = "old"
model = "gpt-4"
some_other_key = "stays"

[model_providers.old]
name = "old"
base_url = "https://old/v1"
`
	if err := os.WriteFile(path, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	err := UpdateConfig(path, Settings{
		Provider: "modelserver", Model: "gpt-5.5",
		BaseURL: "https://code.ai.cs.ac.cn/v1", EnvKey: "OPENAI_API_KEY",
		WireAPI: "responses",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	// Must keep [model_providers.old] and the unrelated key
	for _, want := range []string{
		`[model_providers.old]`,
		`some_other_key = "stays"`,
		`[model_providers.modelserver]`,
		`model_provider = "modelserver"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in merged config:\n%s", want, s)
		}
	}
	// Backup created
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) == 0 {
		t.Errorf("expected backup")
	}
}
