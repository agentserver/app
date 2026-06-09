package tokenrefresh

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
)

type memSecrets struct {
	values map[string]string
}

func newMemSecrets() *memSecrets {
	return &memSecrets{values: map[string]string{}}
}

func (m *memSecrets) Get(key string) (string, error) {
	v, ok := m.values[key]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}

func (m *memSecrets) Set(key, value string) error {
	m.values[key] = value
	return nil
}

func (m *memSecrets) Delete(key string) error {
	delete(m.values, key)
	return nil
}

func TestNextDelayRefreshesThirtyMinutesBeforeExpiry(t *testing.T) {
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)

	got := NextDelay(now, expiresAt, 30*time.Minute, 5*time.Minute, nil)
	if got != 30*time.Minute {
		t.Fatalf("NextDelay = %s, want 30m", got)
	}
}

func TestNextDelayRefreshesImmediatelyInsideThirtyMinuteWindow(t *testing.T) {
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(29 * time.Minute)

	got := NextDelay(now, expiresAt, 30*time.Minute, 5*time.Minute, nil)
	if got != 0 {
		t.Fatalf("NextDelay = %s, want immediate refresh", got)
	}
}

func TestNextDelayRetriesFiveMinutesAfterRefreshFailure(t *testing.T) {
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)

	got := NextDelay(now, expiresAt, 30*time.Minute, 5*time.Minute, errors.New("refresh failed"))
	if got != 5*time.Minute {
		t.Fatalf("NextDelay after failure = %s, want 5m", got)
	}
}

func TestRefreshOnceStoresAccessRefreshExpiryWithoutChangingCodexEnv(t *testing.T) {
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	sec := newMemSecrets()
	if err := sec.Set(RefreshTokenKey, "rtok-1"); err != nil {
		t.Fatal(err)
	}

	var gotRefresh string
	persistCalled := false
	processEnvCalled := false
	expiresAt, err := RefreshOnce(context.Background(), Options{
		Secrets: sec,
		OAuth:   oauth.AuthCodeConfig{ClientID: "client-x"},
		Now:     func() time.Time { return now },
		Refresh: func(ctx context.Context, cfg oauth.AuthCodeConfig, refreshToken string) (oauth.Token, error) {
			gotRefresh = refreshToken
			return oauth.Token{
				AccessToken:  "at-2",
				RefreshToken: "rtok-2",
				ExpiresIn:    3600,
			}, nil
		},
		PersistEnv: func(key, value string) error {
			persistCalled = true
			return nil
		},
		SetProcessEnv: func(key, value string) error {
			processEnvCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if gotRefresh != "rtok-1" {
		t.Fatalf("refresh token passed = %q, want rtok-1", gotRefresh)
	}
	if expiresAt != now.Add(time.Hour) {
		t.Fatalf("expiresAt = %s, want %s", expiresAt, now.Add(time.Hour))
	}
	if got, _ := sec.Get(AccessTokenKey); got != "at-2" {
		t.Fatalf("access token secret = %q, want at-2", got)
	}
	if got, _ := sec.Get(RefreshTokenKey); got != "rtok-2" {
		t.Fatalf("refresh token secret = %q, want rtok-2", got)
	}
	if got, _ := sec.Get(AccessTokenExpiresAtKey); got != now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("expires_at secret = %q", got)
	}
	if persistCalled {
		t.Fatal("RefreshOnce should not persist the short-lived access token into user env")
	}
	if processEnvCalled {
		t.Fatal("RefreshOnce should not write the short-lived access token into process env")
	}
}

func TestRefreshOncePreservesRefreshTokenWhenServerOmitsReplacement(t *testing.T) {
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	sec := newMemSecrets()
	if err := sec.Set(RefreshTokenKey, "rtok-original"); err != nil {
		t.Fatal(err)
	}

	_, err := RefreshOnce(context.Background(), Options{
		Secrets: sec,
		Now:     func() time.Time { return now },
		Refresh: func(ctx context.Context, cfg oauth.AuthCodeConfig, refreshToken string) (oauth.Token, error) {
			return oauth.Token{AccessToken: "at-2", ExpiresIn: 3600}, nil
		},
		PersistEnv:    func(string, string) error { return nil },
		SetProcessEnv: func(string, string) error { return nil },
	})
	if err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if got, _ := sec.Get(RefreshTokenKey); got != "rtok-original" {
		t.Fatalf("refresh token secret = %q, want preserved rtok-original", got)
	}
}

func TestRefreshOnceMarksReauthRequiredOnInvalidGrant(t *testing.T) {
	sec := newMemSecrets()
	if err := sec.Set(RefreshTokenKey, "rtok-expired"); err != nil {
		t.Fatal(err)
	}

	_, err := RefreshOnce(context.Background(), Options{
		Secrets: sec,
		Refresh: func(ctx context.Context, cfg oauth.AuthCodeConfig, refreshToken string) (oauth.Token, error) {
			return oauth.Token{}, oauth.ErrInvalidGrant
		},
		PersistEnv:    func(string, string) error { return nil },
		SetProcessEnv: func(string, string) error { return nil },
	})
	if !errors.Is(err, oauth.ErrInvalidGrant) {
		t.Fatalf("err=%v, want ErrInvalidGrant", err)
	}
	if got, _ := sec.Get(ReauthRequiredKey); got != "true" {
		t.Fatalf("reauth flag=%q, want true", got)
	}
	if got, _ := sec.Get(RefreshErrorKey); got == "" {
		t.Fatal("refresh error should be stored")
	}
}

func TestRefreshOnceClearsReauthRequiredAfterSuccess(t *testing.T) {
	now := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	sec := newMemSecrets()
	for key, value := range map[string]string{
		RefreshTokenKey:   "rtok-original",
		ReauthRequiredKey: "true",
		RefreshErrorKey:   "token refresh: invalid_grant",
		RefreshErrorAtKey: now.Add(-time.Hour).Format(time.RFC3339),
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}

	_, err := RefreshOnce(context.Background(), Options{
		Secrets: sec,
		Now:     func() time.Time { return now },
		Refresh: func(ctx context.Context, cfg oauth.AuthCodeConfig, refreshToken string) (oauth.Token, error) {
			return oauth.Token{AccessToken: "at-new", RefreshToken: "rtok-new", ExpiresIn: 3600}, nil
		},
		PersistEnv:    func(string, string) error { return nil },
		SetProcessEnv: func(string, string) error { return nil },
	})
	if err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	for _, key := range []string{ReauthRequiredKey, RefreshErrorKey, RefreshErrorAtKey} {
		if got, err := sec.Get(key); err == nil {
			t.Fatalf("%s=%q, want deleted", key, got)
		}
	}
}

func TestStoreTokenClearsReauthRequiredAfterRelogin(t *testing.T) {
	now := time.Date(2026, 6, 8, 11, 0, 0, 0, time.UTC)
	sec := newMemSecrets()
	for key, value := range map[string]string{
		ReauthRequiredKey: "true",
		RefreshErrorKey:   "token refresh: invalid_grant",
		RefreshErrorAtKey: now.Add(-time.Hour).Format(time.RFC3339),
	} {
		if err := sec.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := StoreToken(sec, oauth.Token{
		AccessToken:  "at-relogin",
		RefreshToken: "rt-relogin",
		ExpiresIn:    3600,
	}, now, ""); err != nil {
		t.Fatalf("StoreToken: %v", err)
	}

	for _, key := range []string{ReauthRequiredKey, RefreshErrorKey, RefreshErrorAtKey} {
		if got, err := sec.Get(key); err == nil {
			t.Fatalf("%s=%q, want deleted", key, got)
		}
	}
}
