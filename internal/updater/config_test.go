package updater

import (
	"testing"
	"time"
)

func makeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadUpgradeConfigDefaults(t *testing.T) {
	cfg := LoadUpgradeConfig(makeEnv(nil))
	if !cfg.GitHubEnabled {
		t.Fatal("default GitHubEnabled must be true")
	}
	if cfg.GitHubRepo != "agentserver/app" {
		t.Fatalf("default repo=%q", cfg.GitHubRepo)
	}
	if cfg.GitHubPolicy.ManifestTimeout != 5*time.Second {
		t.Fatalf("default ManifestTimeout=%v", cfg.GitHubPolicy.ManifestTimeout)
	}
}

func TestLoadUpgradeConfigOverrides(t *testing.T) {
	cfg := LoadUpgradeConfig(makeEnv(map[string]string{
		"UPGRADE_GITHUB_ENABLED":            "true",
		"UPGRADE_GITHUB_REPO":               "acme/tool",
		"UPGRADE_GITHUB_MANIFEST_TIMEOUT":   "2s",
		"UPGRADE_GITHUB_FIRST_BYTE_TIMEOUT": "3s",
		"UPGRADE_GITHUB_MIN_SPEED_BPS":      "200000",
		"UPGRADE_GITHUB_SPEED_WINDOW":       "7s",
	}))
	if !cfg.GitHubEnabled {
		t.Fatal("expected enabled")
	}
	if cfg.GitHubRepo != "acme/tool" {
		t.Fatalf("repo=%q", cfg.GitHubRepo)
	}
	if cfg.GitHubPolicy.ManifestTimeout != 2*time.Second {
		t.Fatalf("ManifestTimeout=%v", cfg.GitHubPolicy.ManifestTimeout)
	}
	if cfg.GitHubPolicy.MinSpeedBytesPerSec != 200000 {
		t.Fatalf("MinSpeedBytesPerSec=%d", cfg.GitHubPolicy.MinSpeedBytesPerSec)
	}
	if cfg.GitHubPolicy.SpeedWindow != 7*time.Second {
		t.Fatalf("SpeedWindow=%v", cfg.GitHubPolicy.SpeedWindow)
	}
}

func TestBuildSourcesDefaultReturnsGitHubThenCDN(t *testing.T) {
	got := BuildSources(LoadUpgradeConfig(makeEnv(nil)))
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Name() != "github" || got[1].Name() != "cdn" {
		t.Fatalf("order=[%s,%s]", got[0].Name(), got[1].Name())
	}
}

func TestBuildSourcesCanDisableGitHubViaEnv(t *testing.T) {
	got := BuildSources(LoadUpgradeConfig(makeEnv(map[string]string{
		"UPGRADE_GITHUB_ENABLED": "false",
	})))
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestLoadUpgradeConfigRejectsMaliciousRepoSlug(t *testing.T) {
	cases := []string{
		"../../etc/passwd",
		"owner//repo",
		"owner",
		"owner/repo/extra",
		"/leading-slash/repo",
		"owner/repo with space",
		"owner/repo\nrepo",
	}
	for _, bad := range cases {
		cfg := LoadUpgradeConfig(makeEnv(map[string]string{
			"UPGRADE_GITHUB_REPO": bad,
		}))
		if cfg.GitHubRepo != "agentserver/app" {
			t.Errorf("bad slug %q accepted as %q; want default fallback", bad, cfg.GitHubRepo)
		}
	}
}

func TestLoadUpgradeConfigAcceptsValidRepoSlugs(t *testing.T) {
	cases := []string{"owner/repo", "a.b/c-d", "org_1/x_2.3"}
	for _, ok := range cases {
		cfg := LoadUpgradeConfig(makeEnv(map[string]string{
			"UPGRADE_GITHUB_REPO": ok,
		}))
		if cfg.GitHubRepo != ok {
			t.Errorf("valid slug %q rejected", ok)
		}
	}
}
