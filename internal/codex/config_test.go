package codex

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
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
		`model_reasoning_effort = "high"`,
		`approvals_reviewer = "guardian_subagent"`,
		`sandbox_mode = "danger-full-access"`,
		`developer_instructions = "请始终使用简体中文与用户交流；除非用户明确要求其他语言。"`,
		`[windows]`,
		`sandbox = "unelevated"`,
		`[model_providers.modelserver]`,
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, `[projects.`) {
		t.Errorf("unexpected project trust config in:\n%s", s)
	}
}

func TestModelserverProxySettingsUsesConfiguredLocalCredential(t *testing.T) {
	got := ModelserverProxySettings("http://127.0.0.1:53452/v1", "random-local-token")
	if got.Provider != "modelserver" {
		t.Fatalf("Provider = %q, want modelserver", got.Provider)
	}
	if got.BaseURL != "http://127.0.0.1:53452/v1" {
		t.Fatalf("BaseURL = %q, want local proxy URL", got.BaseURL)
	}
	if got.EnvKey != "" {
		t.Fatalf("EnvKey = %q, want empty local proxy env key", got.EnvKey)
	}
	if got.WireAPI != "responses" {
		t.Fatalf("WireAPI = %q, want responses", got.WireAPI)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := UpdateConfig(path, got); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if strings.Contains(s, "env_key") {
		t.Fatalf("proxy config should not require an environment variable:\n%s", s)
	}
	if !strings.Contains(s, `experimental_bearer_token = "random-local-token"`) {
		t.Fatalf("proxy config missing stable bearer token:\n%s", s)
	}
	if strings.Contains(s, "agentserver-local-proxy") {
		t.Fatalf("proxy config contains compiled default token:\n%s", s)
	}
}

func TestHasModelserverDirectConfigRequiresExactDirectProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if ok, err := HasModelserverDirectConfig(path); err != nil || ok {
		t.Fatalf("missing config: ok=%v err=%v, want false nil", ok, err)
	}
	writeCodexTestFile(t, path, strings.Join([]string{
		`model_provider = "modelserver"`,
		``,
		`[model_providers.modelserver]`,
		`name = "modelserver"`,
		`base_url = "http://127.0.0.1:53452/v1"`,
		`experimental_bearer_token = "random-local-token"`,
		`wire_api = "responses"`,
		``,
	}, "\n"))
	if ok, err := HasModelserverDirectConfig(path); err != nil || ok {
		t.Fatalf("proxy config: ok=%v err=%v, want false nil", ok, err)
	}
	if err := UpdateConfig(path, ModelserverSettings()); err != nil {
		t.Fatal(err)
	}
	if ok, err := HasModelserverDirectConfig(path); err != nil || !ok {
		t.Fatalf("direct config: ok=%v err=%v, want true nil", ok, err)
	}
}

func TestUpdateConfig_MergeKeepsOtherProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	prior := `model_provider = "old"
model = "gpt-4"
some_other_key = "stays"

[windows]
sandbox_private_desktop = false

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
		`[windows]`,
		`sandbox = "unelevated"`,
		`sandbox_private_desktop = false`,
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

func TestUpdateConfigPreservesUnknownModelProviderFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, strings.Join([]string{
		`model_provider = "modelserver"`,
		``,
		`[model_providers.modelserver]`,
		`name = "old-name"`,
		`base_url = "https://old/v1"`,
		`env_key = "OLD_KEY"`,
		`wire_api = "chat"`,
		`custom_header = "keep"`,
		``,
	}, "\n"))

	if err := UpdateConfig(path, ModelserverSettings()); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
		`custom_header = "keep"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestUpdateConfigReturnsBackupError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, `model_provider = "old"`+"\n")
	wantErr := errors.New("backup failed")
	prev := writeConfigBackup
	writeConfigBackup = func(string, []byte) error {
		return wantErr
	}
	t.Cleanup(func() {
		writeConfigBackup = prev
	})

	err := UpdateConfig(path, ModelserverSettings())
	if !errors.Is(err, wantErr) {
		t.Fatalf("UpdateConfig error=%v, want %v", err, wantErr)
	}
}

func TestUpdateConfigWritesPrivateFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := UpdateConfig(path, ModelserverSettings()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%#o, want 0600", got)
	}
}

func TestUpdateConfigPrunesOldBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, `model_provider = "old"`+"\n")
	for i := 1; i <= maxConfigBackups+3; i++ {
		writeCodexTestFile(t, filepath.Join(dir, "config.toml.bak."+strconv.Itoa(i)), "backup\n")
	}

	if err := UpdateConfig(path, Settings{
		Provider: "modelserver",
		Model:    "gpt-5.5",
		BaseURL:  "https://code.ai.cs.ac.cn/v1",
		EnvKey:   "OPENAI_API_KEY",
		WireAPI:  "responses",
	}); err != nil {
		t.Fatal(err)
	}

	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) != maxConfigBackups {
		t.Fatalf("backup count=%d want %d: %v", len(matches), maxConfigBackups, matches)
	}
	for _, old := range []string{"1", "2", "3", "4"} {
		if _, err := os.Stat(path + ".bak." + old); !os.IsNotExist(err) {
			t.Fatalf("old backup %s was not pruned, stat err=%v", old, err)
		}
	}
}

func TestUpdateMCPServerAddsDriverAndKeepsModelConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := UpdateConfig(path, Settings{
		Provider: "modelserver",
		Model:    "gpt-5.5",
		BaseURL:  "https://code.ai.cs.ac.cn/v1",
		EnvKey:   "OPENAI_API_KEY",
		WireAPI:  "responses",
	}); err != nil {
		t.Fatal(err)
	}

	if err := UpdateMCPServer(path, "driver", MCPServer{
		Command:           `C:\Users\61414\AppData\Local\Programs\agentserver-app\driver-agent.exe`,
		Args:              []string{"serve-mcp", "--config", `C:\Users\61414\.config\multi-agent\driver.yaml`},
		StartupTimeoutSec: 30,
		ToolTimeoutSec:    120,
		Enabled:           boolPtr(true),
	}); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`[model_providers.modelserver]`,
		`[mcp_servers.driver]`,
		`command = "C:\\Users\\61414\\AppData\\Local\\Programs\\agentserver-app\\driver-agent.exe"`,
		`args = ["serve-mcp", "--config", "C:\\Users\\61414\\.config\\multi-agent\\driver.yaml"]`,
		`startup_timeout_sec = 30`,
		`tool_timeout_sec = 120`,
		`enabled = true`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestUpdateMCPServerPreservesUnknownFieldsInNamedServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, strings.Join([]string{
		`[mcp_servers.driver]`,
		`command = "old"`,
		`args = ["old"]`,
		`custom_tools = ["keep"]`,
		``,
	}, "\n"))

	if err := UpdateMCPServer(path, "driver", MCPServer{
		Command: "agentserver",
		Args:    []string{"serve-driver-mcp"},
	}); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		`command = "agentserver"`,
		`args = ["serve-driver-mcp"]`,
		`custom_tools = ["keep"]`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestRemoveMCPServerRemovesOnlyNamedServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, strings.Join([]string{
		`model_provider = "modelserver"`,
		`model = "gpt-5.5"`,
		``,
		`[mcp_servers.driver]`,
		`command = "C:\\Agentserver\\driver-agent.exe"`,
		`args = ["serve-mcp", "--config", "C:\\Users\\me\\.config\\multi-agent\\driver.yaml"]`,
		`startup_timeout_sec = 30`,
		``,
		`[mcp_servers.other]`,
		`command = "other.exe"`,
		`args = ["serve"]`,
		``,
		`[model_providers.modelserver]`,
		`name = "modelserver"`,
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
		``,
	}, "\n"))

	if err := RemoveMCPServer(path, "driver"); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	s := string(b)
	for _, unwanted := range []string{
		`[mcp_servers.driver]`,
		`C:\\Agentserver\\driver-agent.exe`,
		`multi-agent\\driver.yaml`,
		`startup_timeout_sec = 30`,
	} {
		if strings.Contains(s, unwanted) {
			t.Fatalf("config.toml still contains driver MCP content %q:\n%s", unwanted, s)
		}
	}
	for _, want := range []string{
		`model_provider = "modelserver"`,
		`model = "gpt-5.5"`,
		`[mcp_servers.other]`,
		`command = "other.exe"`,
		`[model_providers.modelserver]`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("config.toml missing preserved content %q:\n%s", want, s)
		}
	}
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) == 0 {
		t.Fatalf("expected backup")
	}
}

func writeCodexTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func TestCurrentModelMissingFileReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	got, err := CurrentModel(filepath.Join(dir, "nope.toml"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != ModelserverSettings().Model {
		t.Errorf("CurrentModel = %q, want default %q", got, ModelserverSettings().Model)
	}
}

func TestCurrentModelReturnsConfiguredValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := SetModel(path, "glm-5.2"); err != nil {
		t.Fatal(err)
	}
	got, err := CurrentModel(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "glm-5.2" {
		t.Errorf("CurrentModel = %q, want glm-5.2", got)
	}
}

// Regression: Windows sometimes returns ERROR_ACCESS_DENIED on rename when
// another process (Codex Desktop polling config.toml) briefly holds a read
// handle. The rename helper retries a few times to ride out the lock.
func TestRenameWithRetrySucceedsAfterTransientFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameWithRetry(src, dst); err != nil {
		t.Fatalf("renameWithRetry on a clean dst should succeed: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("dst missing after rename: %v", err)
	}
}

func TestSetModelRewritesOnlyModelField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := SetModel(path, "glm-5.2[1m]"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `model = "glm-5.2[1m]"`) {
		t.Errorf("model field not set; got:\n%s", b)
	}
	// provider/wire_api must be present (not clobbered)
	if !strings.Contains(string(b), `model_provider = "modelserver"`) {
		t.Errorf("model_provider clobbered; got:\n%s", b)
	}
	if !strings.Contains(string(b), `wire_api = "responses"`) {
		t.Errorf("wire_api clobbered; got:\n%s", b)
	}
}

// Regression: the launcher calls ModelserverProxySettings + UpdateConfig every
// time the desktop app starts. It must NOT clobber the user's set-model choice
// back to the compiled-in default — Codex Desktop would then immediately reset
// glm-5.2 / deepseek-v4-pro to gpt-5.5 on every restart.
func TestUpdateConfig_ProviderOnlyPreservesUserModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// Initial provisioning (first write): default model lands.
	if err := UpdateConfig(path, ModelserverProxySettings(modelproxy.DefaultBaseURL, "tok")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `model = "gpt-5.5"`) {
		t.Fatalf("first-write default missing:\n%s", b)
	}
	// User picks glm-5.2.
	if err := SetModel(path, "glm-5.2"); err != nil {
		t.Fatal(err)
	}
	// Launcher fires again on next desktop start — must preserve glm-5.2.
	if err := UpdateConfig(path, ModelserverProxySettings(modelproxy.DefaultBaseURL, "tok")); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), `model = "glm-5.2"`) {
		t.Fatalf("launcher restart clobbered user model selection:\n%s", b)
	}
	if strings.Contains(string(b), `model = "gpt-5.5"`) {
		t.Fatalf("default model leaked back in:\n%s", b)
	}
}

// Regression (PR #12 review P1): SetModel previously read the config, dropped
// every field except base_url + experimental_bearer_token, then handed the
// stripped Settings to UpdateConfig — which deletes env_key when Settings.EnvKey
// is empty. A valid direct-provider config (env_key = "OPENAI_API_KEY", no
// bearer token) silently became a proxy config (no env_key, legacy bearer
// token), breaking auth on the next Codex start. SetModel must leave the
// existing provider block intact.
func TestSetModelPreservesDirectProviderEnvKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeCodexTestFile(t, path, strings.Join([]string{
		`model = "gpt-4o"`,
		`model_provider = "modelserver"`,
		``,
		`[model_providers.modelserver]`,
		`name = "modelserver"`,
		`base_url = "https://api.openai.com/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
		``,
	}, "\n"))

	if err := SetModel(path, "glm-5.2"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	got := string(body)
	for _, want := range []string{
		`model = "glm-5.2"`,
		`env_key = "OPENAI_API_KEY"`,
		`base_url = "https://api.openai.com/v1"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("SetModel clobbered direct config; missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		`experimental_bearer_token`,
		LegacyLocalProxyAPIKeyValue,
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("SetModel leaked proxy-mode field %q into direct config:\n%s", unwanted, got)
		}
	}
}

// Regression: SetModel on a non-existent file should still produce a working
// proxy-style config seeded with sane defaults (this is what `agentctl
// set-model` on a brand-new headless install hits).
func TestSetModelOnMissingFileSeedsProxyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := SetModel(path, "glm-5.2"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	got := string(body)
	for _, want := range []string{
		`model = "glm-5.2"`,
		`model_provider = "modelserver"`,
		`base_url = "http://127.0.0.1:53452/v1"`,
		`experimental_bearer_token = "agentserver-local-proxy"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in seeded config:\n%s", want, got)
		}
	}
}

func TestSetModelPreservesExistingBearerToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// Linux headless writes a per-user random token into experimental_bearer_token.
	seed := ModelserverProxySettings(modelproxy.DefaultBaseURL, "per-user-random-token")
	if err := UpdateConfig(path, seed); err != nil {
		t.Fatal(err)
	}
	if err := SetModel(path, "deepseek-v4-pro"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	body := string(b)
	if !strings.Contains(body, `model = "deepseek-v4-pro"`) {
		t.Errorf("model not set; got:\n%s", body)
	}
	if !strings.Contains(body, `experimental_bearer_token = "per-user-random-token"`) {
		t.Errorf("per-user bearer token was clobbered; got:\n%s", body)
	}
	if strings.Contains(body, `agentserver-local-proxy`) {
		t.Errorf("legacy token leaked into config; got:\n%s", body)
	}
}

// TestUpdateConfig_ProvisionsGLMCatalog verifies that UpdateConfig writes the
// GLM model catalog next to config.toml and sets `model_catalog_json` to an
// absolute path pointing at it. This is what makes Codex resolve glm-5.2
// metadata (1M context, xhigh reasoning) instead of falling back to degraded
// defaults on a fresh install.
func TestUpdateConfig_ProvisionsGLMCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := UpdateConfig(path, Settings{
		Provider: "modelserver", Model: "gpt-5.5",
		BaseURL: "https://code.ai.cs.ac.cn/v1", EnvKey: "OPENAI_API_KEY",
		WireAPI: "responses",
	}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	wantKey := `model_catalog_json =`
	if !strings.Contains(s, wantKey) {
		t.Errorf("missing %q in config:\n%s", wantKey, s)
	}
	// The catalog file must exist next to config.toml.
	catalogPath := filepath.Join(dir, "glm-catalog.json")
	catalog, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("glm catalog not written: %v", err)
	}
	// The written catalog must match the embedded asset (1M context, glm-5.2).
	if !bytes.Equal(catalog, glmCatalogJSON) {
		t.Errorf("on-disk catalog differs from embedded asset")
	}
	// config.toml must reference the catalog by absolute path.
	if !strings.Contains(s, catalogPath) {
		t.Errorf("config does not reference catalog path %q:\n%s", catalogPath, s)
	}
}

// TestUpdateConfig_CatalogProvisioningIsIdempotent verifies a second
// UpdateConfig does not rewrite an identical catalog file (so routine config
// refreshes do not thrash the file), while still keeping the config key.
func TestUpdateConfig_CatalogProvisioningIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	settings := Settings{
		Provider: "modelserver", Model: "gpt-5.5",
		BaseURL: "https://code.ai.cs.ac.cn/v1", EnvKey: "OPENAI_API_KEY",
		WireAPI: "responses",
	}
	if err := UpdateConfig(path, settings); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(dir, "glm-catalog.json")
	info1, err := os.Stat(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	// Second run: file content is identical, so it should not be rewritten.
	if err := UpdateConfig(path, settings); err != nil {
		t.Fatal(err)
	}
	info2, err := os.Stat(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if info1.ModTime() != info2.ModTime() {
		t.Errorf("catalog file was rewritten on identical re-run (mtime changed): %v -> %v",
			info1.ModTime(), info2.ModTime())
	}
}
