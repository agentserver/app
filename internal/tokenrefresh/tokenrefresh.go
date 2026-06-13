package tokenrefresh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/process"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
)

const (
	AccessTokenKey          = "modelserver_api_key"
	RefreshTokenKey         = "modelserver_refresh_token"
	AccessTokenExpiresAtKey = "modelserver_access_token_expires_at"
	ReauthRequiredKey       = "modelserver_reauth_required"
	RefreshErrorKey         = "modelserver_refresh_error"
	RefreshErrorAtKey       = "modelserver_refresh_error_at"

	OpenAIAPIKeyEnv = "OPENAI_API_KEY"
)

var (
	ErrNoSecrets      = errors.New("tokenrefresh: secrets store required")
	ErrNoRefreshToken = errors.New("tokenrefresh: refresh token missing")
)

type Options struct {
	Secrets secrets.Store
	OAuth   oauth.AuthCodeConfig

	Refresh func(context.Context, oauth.AuthCodeConfig, string) (oauth.Token, error)

	Now           func() time.Time
	PersistEnv    func(string, string) error
	SetProcessEnv func(string, string) error
	Sleep         func(context.Context, time.Duration) error
	Logf          func(string, ...any)

	RefreshBefore time.Duration
	RetryInterval time.Duration
}

func NextDelay(now, expiresAt time.Time, refreshBefore, retryInterval time.Duration, refreshErr error) time.Duration {
	if retryInterval <= 0 {
		retryInterval = 5 * time.Minute
	}
	if refreshErr != nil {
		return retryInterval
	}
	if refreshBefore <= 0 {
		refreshBefore = 30 * time.Minute
	}
	if expiresAt.IsZero() {
		return 0
	}
	delay := expiresAt.Add(-refreshBefore).Sub(now)
	if delay < 0 {
		return 0
	}
	return delay
}

func RefreshOnce(ctx context.Context, opts Options) (time.Time, error) {
	opts = withDefaults(opts)
	if opts.Secrets == nil {
		return time.Time{}, ErrNoSecrets
	}
	rt, err := opts.Secrets.Get(RefreshTokenKey)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return time.Time{}, ErrNoRefreshToken
		}
		return time.Time{}, err
	}
	tok, err := opts.Refresh(ctx, opts.OAuth, rt)
	if err != nil {
		if ReauthRequired(err) {
			_ = MarkReauthRequired(opts.Secrets, err, opts.Now())
		}
		return time.Time{}, err
	}
	expiresAt, err := StoreToken(opts.Secrets, tok, opts.Now(), rt)
	if err != nil {
		return time.Time{}, err
	}
	return expiresAt, nil
}

func ReauthRequired(err error) bool {
	return errors.Is(err, ErrNoRefreshToken) || errors.Is(err, oauth.ErrInvalidGrant)
}

func MarkReauthRequired(sec secrets.Store, err error, now time.Time) error {
	if sec == nil {
		return ErrNoSecrets
	}
	if err := sec.Set(ReauthRequiredKey, "true"); err != nil {
		return err
	}
	if err != nil {
		if setErr := sec.Set(RefreshErrorKey, err.Error()); setErr != nil {
			return setErr
		}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return sec.Set(RefreshErrorAtKey, now.UTC().Format(time.RFC3339))
}

func ClearReauthRequired(sec secrets.Store) error {
	if sec == nil {
		return ErrNoSecrets
	}
	var errs []error
	for _, key := range []string{ReauthRequiredKey, RefreshErrorKey, RefreshErrorAtKey} {
		if err := sec.Delete(key); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func StoreToken(sec secrets.Store, tok oauth.Token, now time.Time, previousRefreshToken string) (time.Time, error) {
	if sec == nil {
		return time.Time{}, ErrNoSecrets
	}
	if tok.AccessToken == "" {
		return time.Time{}, errors.New("tokenrefresh: access token missing")
	}
	if tok.ExpiresIn <= 0 {
		return time.Time{}, errors.New("tokenrefresh: expires_in missing")
	}
	refreshToken := tok.RefreshToken
	if refreshToken == "" {
		refreshToken = previousRefreshToken
	}
	expiresAt := now.Add(time.Duration(tok.ExpiresIn) * time.Second).UTC()
	if err := sec.Set(AccessTokenKey, tok.AccessToken); err != nil {
		return time.Time{}, err
	}
	if refreshToken != "" {
		if err := sec.Set(RefreshTokenKey, refreshToken); err != nil {
			return time.Time{}, err
		}
	}
	if err := sec.Set(AccessTokenExpiresAtKey, expiresAt.Format(time.RFC3339)); err != nil {
		return time.Time{}, err
	}
	if err := ClearReauthRequired(sec); err != nil {
		return time.Time{}, err
	}
	return expiresAt, nil
}

func Run(ctx context.Context, opts Options) error {
	opts = withDefaults(opts)
	if opts.Secrets == nil {
		return ErrNoSecrets
	}
	if _, err := opts.Secrets.Get(RefreshTokenKey); err != nil {
		return ErrNoRefreshToken
	}

	var lastErr error
	for {
		expiresAt := loadExpiresAt(opts.Secrets)
		delay := NextDelay(opts.Now(), expiresAt, opts.RefreshBefore, opts.RetryInterval, lastErr)
		if delay > 0 {
			if err := opts.Sleep(ctx, delay); err != nil {
				return err
			}
		}
		_, err := RefreshOnce(ctx, opts)
		if err != nil {
			if errors.Is(err, ErrNoRefreshToken) {
				return err
			}
			lastErr = err
			if opts.Logf != nil {
				opts.Logf("token refresh failed: %v", err)
			}
			continue
		}
		lastErr = nil
	}
}

func StartDaemon(exePath string) error {
	if exePath == "" {
		return nil
	}
	if _, err := os.Stat(exePath); err != nil {
		return err
	}
	lockPath, err := DefaultDaemonLockPath()
	if err == nil {
		if lock, err := AcquireDaemonLock(lockPath); err != nil {
			if errors.Is(err, ErrDaemonAlreadyRunning) {
				return nil
			}
			return err
		} else {
			_ = lock.Close()
		}
	}
	cmd := exec.Command(exePath, "--daemon")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	process.HideWindow(cmd)
	return cmd.Start()
}

func loadExpiresAt(sec secrets.Store) time.Time {
	raw, err := sec.Get(AccessTokenExpiresAtKey)
	if err != nil || raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func withDefaults(opts Options) Options {
	if opts.Refresh == nil {
		opts.Refresh = oauth.RefreshToken
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.PersistEnv == nil {
		opts.PersistEnv = env.PersistUserEnv
	}
	if opts.SetProcessEnv == nil {
		opts.SetProcessEnv = os.Setenv
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepContext
	}
	if opts.RefreshBefore <= 0 {
		opts.RefreshBefore = 30 * time.Minute
	}
	if opts.RetryInterval <= 0 {
		opts.RetryInterval = 5 * time.Minute
	}
	return opts
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func FormatError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
