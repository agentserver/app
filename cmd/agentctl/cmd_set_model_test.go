package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateModelSelection(t *testing.T) {
	cases := []struct {
		model string
		ok    bool
	}{
		{"gpt-5.5", true},
		{"deepseek-v4-pro", true},
		{"glm-5.2", true},
		{"bogus-model", false},
		{"", false},
	}
	for _, c := range cases {
		if err := validateModelSelection(c.model); (err == nil) != c.ok {
			t.Errorf("validateModelSelection(%q) err=%v, want ok=%v", c.model, err, c.ok)
		}
	}
}

func TestRunSetModelWritesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := runSetModelWithConfig(path, []string{"deepseek-v4-pro"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	b, _ := os.ReadFile(path)
	if !contains(string(b), `model = "deepseek-v4-pro"`) {
		t.Errorf("model not written; got:\n%s", b)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
