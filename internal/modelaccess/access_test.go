package modelaccess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

func TestEnsurePrefersLongLivedAPIKey(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	var daemonStarted bool

	result, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(tmp, "codex", "config.toml"),
		Secrets:         sec,
		Env: func(key string) string {
			if key == tokenrefresh.OpenAIAPIKeyEnv {
				return "sk-test"
			}
			return ""
		},
		StartDaemon: func(context.Context) error {
			daemonStarted = true
			return nil
		},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			t.Fatal("RequestDeviceCode called in direct API key mode")
			return oauth.DeviceCodeChallenge{}, nil
		},
		PollToken: func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error) {
			t.Fatal("PollToken called in direct API key mode")
			return oauth.Token{}, nil
		},
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if result.Mode != ModeDirectAPIKey {
		t.Fatalf("Mode = %q, want %q", result.Mode, ModeDirectAPIKey)
	}
	if daemonStarted {
		t.Fatal("StartDaemon called in direct API key mode")
	}
	assertConfigContains(t, filepath.Join(tmp, "codex", "config.toml"),
		`model_provider = "modelserver"`,
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
	)
}

func TestEnsureDirectAPIKeyModeDoesNotRequireSecrets(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "codex", "config.toml")

	result, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: configPath,
		Env: func(key string) string {
			if key == tokenrefresh.OpenAIAPIKeyEnv {
				return "sk-test"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if result.Mode != ModeDirectAPIKey {
		t.Fatalf("Mode = %q, want %q", result.Mode, ModeDirectAPIKey)
	}
	assertConfigContains(t, configPath,
		`model_provider = "modelserver"`,
		`base_url = "https://code.ai.cs.ac.cn/v1"`,
		`env_key = "OPENAI_API_KEY"`,
	)
}

func TestEnsureProxyModeRunsDeviceLoginWhenRefreshMissing(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	challenge := oauth.DeviceCodeChallenge{
		DeviceCode:      "device-code",
		UserCode:        "ABCD-EFGH",
		VerificationURI: "https://example.test/device",
		ExpiresIn:       600,
		Interval:        1,
	}
	var requestedDeviceCode, polledToken, printedChallenge, daemonStarted bool
	var printedTitle string

	result, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(tmp, "codex", "config.toml"),
		Secrets:         sec,
		Env:             func(string) string { return "" },
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			requestedDeviceCode = true
			return challenge, nil
		},
		PrintChallenge: func(title string, ch oauth.DeviceCodeChallenge) {
			printedChallenge = true
			printedTitle = title
			if ch.DeviceCode != challenge.DeviceCode {
				t.Fatalf("printed challenge DeviceCode = %q, want %q", ch.DeviceCode, challenge.DeviceCode)
			}
		},
		PollToken: func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error) {
			polledToken = true
			return oauth.Token{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600}, nil
		},
		StartDaemon: func(context.Context) error {
			daemonStarted = true
			return nil
		},
		Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if result.Mode != ModeLocalProxy {
		t.Fatalf("Mode = %q, want %q", result.Mode, ModeLocalProxy)
	}
	if !requestedDeviceCode || !printedChallenge || !polledToken || !daemonStarted {
		t.Fatalf("calls: request=%v print=%v poll=%v daemon=%v", requestedDeviceCode, printedChallenge, polledToken, daemonStarted)
	}
	if printedTitle != "Code 登录" {
		t.Fatalf("PrintChallenge title = %q, want %q", printedTitle, "Code 登录")
	}
	assertSecret(t, sec, tokenrefresh.AccessTokenKey, "access")
	assertSecret(t, sec, tokenrefresh.RefreshTokenKey, "refresh")
}

func TestEnsureProxyModeUsesExistingRefreshTokenWithoutPrompt(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.AccessTokenKey, "existing-access")
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	mustSetSecret(t, sec, tokenrefresh.AccessTokenExpiresAtKey, fixedNow().Add(time.Hour).Format(time.RFC3339))
	var daemonStarted bool

	result, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(tmp, "codex", "config.toml"),
		Secrets:         sec,
		Env:             func(string) string { return "" },
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			t.Fatal("RequestDeviceCode called")
			return oauth.DeviceCodeChallenge{}, nil
		},
		PollToken: func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error) {
			t.Fatal("PollToken called")
			return oauth.Token{}, nil
		},
		StartDaemon: func(context.Context) error {
			daemonStarted = true
			return nil
		},
		Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if result.Mode != ModeLocalProxy {
		t.Fatalf("Mode = %q, want %q", result.Mode, ModeLocalProxy)
	}
	if !daemonStarted {
		t.Fatal("StartDaemon was not called")
	}
	assertSecret(t, sec, tokenrefresh.AccessTokenKey, "existing-access")
	assertSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
}

func TestEnsureProxyModeRefreshesExpiredAccessTokenBeforeReturning(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.AccessTokenKey, "expired-access")
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	mustSetSecret(t, sec, tokenrefresh.AccessTokenExpiresAtKey, fixedNow().Add(-time.Minute).Format(time.RFC3339))
	var refreshed, daemonStarted bool

	result, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(tmp, "codex", "config.toml"),
		Secrets:         sec,
		Env:             func(string) string { return "" },
		Refresh: func(_ context.Context, _ oauth.AuthCodeConfig, refreshToken string) (oauth.Token, error) {
			refreshed = true
			if refreshToken != "existing-refresh" {
				t.Fatalf("refresh token = %q, want existing-refresh", refreshToken)
			}
			return oauth.Token{AccessToken: "fresh-access", ExpiresIn: 3600}, nil
		},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			t.Fatal("RequestDeviceCode called")
			return oauth.DeviceCodeChallenge{}, nil
		},
		StartDaemon: func(context.Context) error {
			daemonStarted = true
			return nil
		},
		Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if result.Mode != ModeLocalProxy {
		t.Fatalf("Mode = %q, want %q", result.Mode, ModeLocalProxy)
	}
	if !refreshed || !daemonStarted {
		t.Fatalf("calls: refresh=%v daemon=%v", refreshed, daemonStarted)
	}
	assertSecret(t, sec, tokenrefresh.AccessTokenKey, "fresh-access")
}

func TestEnsureProxyModeRunsDeviceLoginWhenExpiredAccessRefreshNeedsReauth(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.AccessTokenKey, "expired-access")
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	mustSetSecret(t, sec, tokenrefresh.AccessTokenExpiresAtKey, fixedNow().Add(-time.Minute).Format(time.RFC3339))
	var refreshed, requestedDeviceCode, daemonStarted bool

	_, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(tmp, "codex", "config.toml"),
		Secrets:         sec,
		Env:             func(string) string { return "" },
		Refresh: func(_ context.Context, _ oauth.AuthCodeConfig, refreshToken string) (oauth.Token, error) {
			refreshed = true
			return oauth.Token{}, fmt.Errorf("refresh failed: %w", oauth.ErrInvalidGrant)
		},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			requestedDeviceCode = true
			return oauth.DeviceCodeChallenge{DeviceCode: "device", UserCode: "code", ExpiresIn: 600}, nil
		},
		PrintChallenge: func(string, oauth.DeviceCodeChallenge) {},
		PollToken: func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error) {
			return oauth.Token{AccessToken: "device-access", RefreshToken: "device-refresh", ExpiresIn: 3600}, nil
		},
		StartDaemon: func(context.Context) error {
			daemonStarted = true
			return nil
		},
		Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if !refreshed || !requestedDeviceCode || !daemonStarted {
		t.Fatalf("calls: refresh=%v request=%v daemon=%v", refreshed, requestedDeviceCode, daemonStarted)
	}
	assertSecret(t, sec, tokenrefresh.AccessTokenKey, "device-access")
}

func TestEnsureProxyModeRefreshesWhenAccessTokenMissing(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	var refreshed, daemonStarted bool

	result, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(tmp, "codex", "config.toml"),
		Secrets:         sec,
		Env:             func(string) string { return "" },
		Refresh: func(_ context.Context, _ oauth.AuthCodeConfig, refreshToken string) (oauth.Token, error) {
			refreshed = true
			if refreshToken != "existing-refresh" {
				t.Fatalf("refresh token = %q, want %q", refreshToken, "existing-refresh")
			}
			return oauth.Token{
				AccessToken: "refreshed-access",
				ExpiresIn:   3600,
			}, nil
		},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			t.Fatal("RequestDeviceCode called")
			return oauth.DeviceCodeChallenge{}, nil
		},
		StartDaemon: func(context.Context) error {
			daemonStarted = true
			return nil
		},
		Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if result.Mode != ModeLocalProxy {
		t.Fatalf("Mode = %q, want %q", result.Mode, ModeLocalProxy)
	}
	if !refreshed || !daemonStarted {
		t.Fatalf("calls: refresh=%v daemon=%v", refreshed, daemonStarted)
	}
	assertSecret(t, sec, tokenrefresh.AccessTokenKey, "refreshed-access")
	assertSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
}

func TestEnsureProxyModeRunsDeviceLoginWhenRefreshNeedsReauth(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	var refreshed, requestedDeviceCode, polledToken, daemonStarted bool

	result, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(tmp, "codex", "config.toml"),
		Secrets:         sec,
		Env:             func(string) string { return "" },
		Refresh: func(_ context.Context, _ oauth.AuthCodeConfig, refreshToken string) (oauth.Token, error) {
			refreshed = true
			if refreshToken != "existing-refresh" {
				t.Fatalf("refresh token = %q, want %q", refreshToken, "existing-refresh")
			}
			return oauth.Token{}, fmt.Errorf("refresh failed: %w", oauth.ErrInvalidGrant)
		},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			requestedDeviceCode = true
			return oauth.DeviceCodeChallenge{DeviceCode: "device", UserCode: "code", ExpiresIn: 600}, nil
		},
		PrintChallenge: func(string, oauth.DeviceCodeChallenge) {},
		PollToken: func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error) {
			polledToken = true
			return oauth.Token{AccessToken: "device-access", RefreshToken: "device-refresh", ExpiresIn: 3600}, nil
		},
		StartDaemon: func(context.Context) error {
			daemonStarted = true
			return nil
		},
		Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if result.Mode != ModeLocalProxy {
		t.Fatalf("Mode = %q, want %q", result.Mode, ModeLocalProxy)
	}
	if !refreshed || !requestedDeviceCode || !polledToken || !daemonStarted {
		t.Fatalf("calls: refresh=%v request=%v poll=%v daemon=%v", refreshed, requestedDeviceCode, polledToken, daemonStarted)
	}
	assertSecret(t, sec, tokenrefresh.AccessTokenKey, "device-access")
	assertSecret(t, sec, tokenrefresh.RefreshTokenKey, "device-refresh")
	if _, err := sec.Get(tokenrefresh.ReauthRequiredKey); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("ReauthRequiredKey error = %v, want ErrNotFound", err)
	}
}

func TestEnsureProxyModePropagatesRefreshErrorWithoutPrompt(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	refreshErr := errors.New("refresh unavailable")
	var refreshed bool

	_, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(tmp, "codex", "config.toml"),
		Secrets:         sec,
		Env:             func(string) string { return "" },
		Refresh: func(_ context.Context, _ oauth.AuthCodeConfig, refreshToken string) (oauth.Token, error) {
			refreshed = true
			if refreshToken != "existing-refresh" {
				t.Fatalf("refresh token = %q, want %q", refreshToken, "existing-refresh")
			}
			return oauth.Token{}, refreshErr
		},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			t.Fatal("RequestDeviceCode called after non-reauth refresh error")
			return oauth.DeviceCodeChallenge{}, nil
		},
		PollToken: func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error) {
			t.Fatal("PollToken called after non-reauth refresh error")
			return oauth.Token{}, nil
		},
		StartDaemon: func(context.Context) error {
			t.Fatal("StartDaemon called after refresh error")
			return nil
		},
		Now: fixedNow,
	})
	if !errors.Is(err, refreshErr) {
		t.Fatalf("Ensure() error = %v, want %v", err, refreshErr)
	}
	if !refreshed {
		t.Fatal("Refresh was not called")
	}
	if _, err := sec.Get(tokenrefresh.AccessTokenKey); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("AccessTokenKey error = %v, want ErrNotFound", err)
	}
}

func TestEnsureRerunsLoginWhenReauthFlagSet(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.AccessTokenKey, "stale-access")
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "stale-refresh")
	mustSetSecret(t, sec, tokenrefresh.ReauthRequiredKey, "true")
	var requestedDeviceCode, daemonStarted bool

	_, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(tmp, "codex", "config.toml"),
		Secrets:         sec,
		Env:             func(string) string { return "" },
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			requestedDeviceCode = true
			return oauth.DeviceCodeChallenge{DeviceCode: "device", UserCode: "code", ExpiresIn: 600}, nil
		},
		PrintChallenge: func(string, oauth.DeviceCodeChallenge) {},
		PollToken: func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error) {
			return oauth.Token{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresIn: 3600}, nil
		},
		StartDaemon: func(context.Context) error {
			daemonStarted = true
			return nil
		},
		Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if !requestedDeviceCode || !daemonStarted {
		t.Fatalf("calls: request=%v daemon=%v", requestedDeviceCode, daemonStarted)
	}
	assertSecret(t, sec, tokenrefresh.AccessTokenKey, "new-access")
	assertSecret(t, sec, tokenrefresh.RefreshTokenKey, "new-refresh")
	if _, err := sec.Get(tokenrefresh.ReauthRequiredKey); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("ReauthRequiredKey error = %v, want ErrNotFound", err)
	}
}

func TestEnsureRequiresSecrets(t *testing.T) {
	_, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(t.TempDir(), "codex", "config.toml"),
		Env:             func(string) string { return "" },
	})
	if !errors.Is(err, tokenrefresh.ErrNoSecrets) {
		t.Fatalf("Ensure() error = %v, want %v", err, tokenrefresh.ErrNoSecrets)
	}
}

func TestEnsureProxyModeDoesNotWriteProxyConfigWhenSetEnvFails(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "codex", "config.toml")
	writeDirectConfig(t, configPath)
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.AccessTokenKey, "existing-access")
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	mustSetSecret(t, sec, tokenrefresh.AccessTokenExpiresAtKey, fixedNow().Add(time.Hour).Format(time.RFC3339))
	setEnvErr := errors.New("set env failed")

	_, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: configPath,
		Secrets:         sec,
		Env:             func(string) string { return "" },
		SetEnv: func(string, string) error {
			return setEnvErr
		},
		PersistEnv: func(string, string) error {
			t.Fatal("PersistEnv called after SetEnv failure")
			return nil
		},
		StartDaemon: func(context.Context) error {
			t.Fatal("StartDaemon called after SetEnv failure")
			return nil
		},
		Now: fixedNow,
	})
	if !errors.Is(err, setEnvErr) {
		t.Fatalf("Ensure() error = %v, want %v", err, setEnvErr)
	}
	assertConfigNotContains(t, configPath, `base_url = "`+modelproxy.DefaultBaseURL+`"`)
}

func TestEnsureProxyModeDoesNotWriteProxyConfigWhenPersistEnvFails(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "codex", "config.toml")
	writeDirectConfig(t, configPath)
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.AccessTokenKey, "existing-access")
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	mustSetSecret(t, sec, tokenrefresh.AccessTokenExpiresAtKey, fixedNow().Add(time.Hour).Format(time.RFC3339))
	persistErr := errors.New("persist env failed")

	_, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: configPath,
		Secrets:         sec,
		Env:             func(string) string { return "" },
		SetEnv:          func(string, string) error { return nil },
		PersistEnv: func(string, string) error {
			return persistErr
		},
		StartDaemon: func(context.Context) error {
			t.Fatal("StartDaemon called after PersistEnv failure")
			return nil
		},
		Now: fixedNow,
	})
	if !errors.Is(err, persistErr) {
		t.Fatalf("Ensure() error = %v, want %v", err, persistErr)
	}
	assertConfigNotContains(t, configPath, `base_url = "`+modelproxy.DefaultBaseURL+`"`)
}

func TestEnsureProxyModeDoesNotWriteProxyConfigWhenStartDaemonFails(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "codex", "config.toml")
	writeDirectConfig(t, configPath)
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.AccessTokenKey, "existing-access")
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	mustSetSecret(t, sec, tokenrefresh.AccessTokenExpiresAtKey, fixedNow().Add(time.Hour).Format(time.RFC3339))
	daemonErr := errors.New("daemon failed")

	_, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: configPath,
		Secrets:         sec,
		Env:             func(string) string { return "" },
		SetEnv:          func(string, string) error { return nil },
		PersistEnv:      func(string, string) error { return nil },
		StartDaemon: func(context.Context) error {
			return daemonErr
		},
		Now: fixedNow,
	})
	if !errors.Is(err, daemonErr) {
		t.Fatalf("Ensure() error = %v, want %v", err, daemonErr)
	}
	assertConfigNotContains(t, configPath, `base_url = "`+modelproxy.DefaultBaseURL+`"`)
}

func TestProxySettingsWrittenInProxyMode(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.AccessTokenKey, "existing-access")
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	mustSetSecret(t, sec, tokenrefresh.AccessTokenExpiresAtKey, fixedNow().Add(time.Hour).Format(time.RFC3339))
	var setEnvKey, setEnvValue, persistEnvKey, persistEnvValue string
	var daemonStarted bool

	_, err := Ensure(context.Background(), EnsureOptions{
		CodexConfigPath: filepath.Join(tmp, "codex", "config.toml"),
		Secrets:         sec,
		Env:             func(string) string { return "" },
		SetEnv: func(key, value string) error {
			setEnvKey, setEnvValue = key, value
			return nil
		},
		PersistEnv: func(key, value string) error {
			persistEnvKey, persistEnvValue = key, value
			return nil
		},
		StartDaemon: func(context.Context) error {
			daemonStarted = true
			return nil
		},
		Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if !daemonStarted {
		t.Fatal("StartDaemon was not called")
	}
	assertConfigContains(t, filepath.Join(tmp, "codex", "config.toml"),
		`base_url = "`+modelproxy.DefaultBaseURL+`"`,
		`env_key = "`+codex.LocalProxyAPIKeyEnv+`"`,
	)
	if setEnvKey != codex.LocalProxyAPIKeyEnv || setEnvValue != codex.LocalProxyAPIKeyValue {
		t.Fatalf("SetEnv = (%q, %q), want (%q, %q)", setEnvKey, setEnvValue, codex.LocalProxyAPIKeyEnv, codex.LocalProxyAPIKeyValue)
	}
	if persistEnvKey != codex.LocalProxyAPIKeyEnv || persistEnvValue != codex.LocalProxyAPIKeyValue {
		t.Fatalf("PersistEnv = (%q, %q), want (%q, %q)", persistEnvKey, persistEnvValue, codex.LocalProxyAPIKeyEnv, codex.LocalProxyAPIKeyValue)
	}
}

func writeDirectConfig(t *testing.T, path string) {
	t.Helper()
	if err := codex.UpdateConfig(path, codex.ModelserverSettings()); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 6, 13, 1, 2, 3, 0, time.UTC)
}

func mustSetSecret(t *testing.T, sec secrets.Store, key, value string) {
	t.Helper()
	if err := sec.Set(key, value); err != nil {
		t.Fatalf("Set(%q) error = %v", key, err)
	}
}

func assertSecret(t *testing.T, sec secrets.Store, key, want string) {
	t.Helper()
	got, err := sec.Get(key)
	if err != nil {
		t.Fatalf("Get(%q) error = %v", key, err)
	}
	if got != want {
		t.Fatalf("Get(%q) = %q, want %q", key, got, want)
	}
}

func assertConfigContains(t *testing.T, path string, wants ...string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	text := string(b)
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
}

func assertConfigNotContains(t *testing.T, path, unwanted string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	text := string(b)
	if strings.Contains(text, unwanted) {
		t.Fatalf("config contains %q:\n%s", unwanted, text)
	}
}
