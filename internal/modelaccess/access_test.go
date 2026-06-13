package modelaccess

import (
	"context"
	"errors"
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

func TestProxySettingsWrittenInProxyMode(t *testing.T) {
	tmp := t.TempDir()
	sec := secrets.New(filepath.Join(tmp, "secrets.json"))
	mustSetSecret(t, sec, tokenrefresh.AccessTokenKey, "existing-access")
	mustSetSecret(t, sec, tokenrefresh.RefreshTokenKey, "existing-refresh")
	var setEnvKey, setEnvValue, persistEnvKey, persistEnvValue string

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
		StartDaemon: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
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
