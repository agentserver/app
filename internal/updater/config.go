package updater

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// UpgradeConfig holds the env-only tunables for the upgrade sources.
// Default (zero-value except GitHubRepo + GitHubPolicy) leaves GitHub
// disabled — Service.effectiveSources falls back to the compat CDN
// shortcut.
type UpgradeConfig struct {
	GitHubEnabled bool
	GitHubRepo    string
	GitHubPolicy  SourcePolicy
}

// validRepoSlug matches "owner/repo" where each segment is 1..100 chars
// of GitHub-allowed characters. Defeats path-traversal shenanigans
// (`../etc/passwd` etc.) that would produce a suspicious API request URL.
var validRepoSlug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}/[A-Za-z0-9._-]{1,100}$`)

// LoadUpgradeConfig reads env vars via the provided getter. Pass
// os.Getenv in production; tests pass a fake. Invalid values are
// silently ignored (fall back to default) so a malformed env var
// never disables the whole feature.
func LoadUpgradeConfig(env func(string) string) UpgradeConfig {
	cfg := UpgradeConfig{
		GitHubRepo:   "agentserver/app",
		GitHubPolicy: DefaultSourcePolicy(),
	}
	if strings.EqualFold(env("UPGRADE_GITHUB_ENABLED"), "true") {
		cfg.GitHubEnabled = true
	}
	if v := env("UPGRADE_GITHUB_REPO"); v != "" && validRepoSlug.MatchString(v) {
		cfg.GitHubRepo = v
	}
	if v := env("UPGRADE_GITHUB_MANIFEST_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.GitHubPolicy.ManifestTimeout = d
		}
	}
	if v := env("UPGRADE_GITHUB_FIRST_BYTE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.GitHubPolicy.FirstByteTimeout = d
		}
	}
	if v := env("UPGRADE_GITHUB_MIN_SPEED_BPS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			cfg.GitHubPolicy.MinSpeedBytesPerSec = n
		}
	}
	if v := env("UPGRADE_GITHUB_SPEED_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.GitHubPolicy.SpeedWindow = d
		}
	}
	return cfg
}

// BuildSources returns nil when GitHub is disabled — Service's compat
// shortcut then builds [cdnSource] lazily from ManifestURL + Client.
// When enabled, returns [githubSource, cdnSource] with the CDN source
// built from DefaultManifestURL and http.DefaultClient using the
// full DefaultSourcePolicy (unlike compat mode which uses zero policy).
func BuildSources(cfg UpgradeConfig) []Source {
	if !cfg.GitHubEnabled {
		return nil
	}
	return []Source{
		NewGitHubSource(cfg.GitHubRepo, "", http.DefaultClient, cfg.GitHubPolicy),
		NewCDNSource(DefaultManifestURL, http.DefaultClient, DefaultSourcePolicy()),
	}
}
