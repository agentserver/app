package uninstall

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
)

func TestRunRemovesProjectStateSecretsAndOpenAIEnv(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{
		InstallRoot:      filepath.Join(dir, ".agentserver-vscode"),
		SecretsFile:      filepath.Join(dir, ".agentserver-vscode", "secrets.json"),
		LocalAppDataRoot: filepath.Join(dir, "local-appdata", "agentserver-vscode"),
	}
	sec := secrets.New(p.SecretsFile)
	for _, key := range []string{
		"modelserver_api_key",
		"modelserver_refresh_token",
		"modelserver_access_token_expires_at",
		"agentserver_ws_api_key",
	} {
		if err := sec.Set(key, "value"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(p.InstallRoot, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.LocalAppDataRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	var deletedEnv string
	err := Run(Options{
		Paths:   p,
		Secrets: sec,
		DeleteEnv: func(key string) error {
			deletedEnv = key
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exists(p.InstallRoot) {
		t.Fatalf("InstallRoot still exists: %s", p.InstallRoot)
	}
	if exists(p.LocalAppDataRoot) {
		t.Fatalf("LocalAppDataRoot still exists: %s", p.LocalAppDataRoot)
	}
	if deletedEnv != "OPENAI_API_KEY" {
		t.Fatalf("deleted env = %q, want OPENAI_API_KEY", deletedEnv)
	}
	for _, key := range []string{
		"modelserver_api_key",
		"modelserver_refresh_token",
		"modelserver_access_token_expires_at",
		"agentserver_ws_api_key",
	} {
		if _, err := sec.Get(key); err != secrets.ErrNotFound {
			t.Fatalf("%s still present: %v", key, err)
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
