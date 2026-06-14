package modelaccess

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

const proxyRefreshBefore = 30 * time.Minute

type Mode string

const (
	ModeDirectAPIKey Mode = "direct_api_key"
	ModeLocalProxy   Mode = "local_proxy"
)

type Result struct {
	Mode Mode
}

type EnsureOptions struct {
	CodexConfigPath string
	Secrets         secrets.Store
	LocalProxyToken string

	DeviceConfig   oauth.Config
	AuthCodeConfig oauth.AuthCodeConfig

	Env        func(string) string
	SetEnv     func(string, string) error
	PersistEnv func(string, string) error
	Now        func() time.Time

	RequestDeviceCode func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error)
	PrintChallenge    func(string, oauth.DeviceCodeChallenge)
	PollToken         func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error)
	Refresh           func(context.Context, oauth.AuthCodeConfig, string) (oauth.Token, error)
	StartDaemon       func(context.Context) error
}

func Ensure(ctx context.Context, opts EnsureOptions) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	opts = defaultEnsureOptions(opts)

	if opts.Env(tokenrefresh.OpenAIAPIKeyEnv) != "" {
		if err := codex.UpdateConfig(opts.CodexConfigPath, codex.ModelserverSettings()); err != nil {
			return Result{}, err
		}
		return Result{Mode: ModeDirectAPIKey}, nil
	}

	if opts.Secrets == nil {
		return Result{}, tokenrefresh.ErrNoSecrets
	}
	if opts.LocalProxyToken == "" {
		return Result{}, errors.New("modelaccess: local proxy token required")
	}
	if err := ensureProxyCredentials(ctx, opts); err != nil {
		return Result{}, err
	}
	if opts.StartDaemon != nil {
		if err := opts.StartDaemon(ctx); err != nil {
			return Result{}, err
		}
	}
	if err := codex.UpdateConfig(opts.CodexConfigPath, codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, opts.LocalProxyToken)); err != nil {
		return Result{}, err
	}
	return Result{Mode: ModeLocalProxy}, nil
}

func ensureProxyCredentials(ctx context.Context, opts EnsureOptions) error {
	reauth, err := opts.Secrets.Get(tokenrefresh.ReauthRequiredKey)
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return err
	}
	if reauth == "true" {
		return runDeviceLogin(ctx, opts)
	}

	refreshToken, err := opts.Secrets.Get(tokenrefresh.RefreshTokenKey)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return runDeviceLogin(ctx, opts)
		}
		return err
	}
	if refreshToken == "" {
		return runDeviceLogin(ctx, opts)
	}

	accessToken, err := opts.Secrets.Get(tokenrefresh.AccessTokenKey)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			err := refreshOnce(ctx, opts)
			if tokenrefresh.ReauthRequired(err) {
				return runDeviceLogin(ctx, opts)
			}
			return err
		}
		return err
	}
	if accessToken == "" {
		err := refreshOnce(ctx, opts)
		if tokenrefresh.ReauthRequired(err) {
			return runDeviceLogin(ctx, opts)
		}
		return err
	}
	if !accessTokenNeedsRefresh(opts.Secrets, opts.Now()) {
		return nil
	}
	err = refreshOnce(ctx, opts)
	if tokenrefresh.ReauthRequired(err) {
		return runDeviceLogin(ctx, opts)
	}
	return err
}

func accessTokenNeedsRefresh(sec secrets.Store, now time.Time) bool {
	raw, err := sec.Get(tokenrefresh.AccessTokenExpiresAtKey)
	if err != nil || raw == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return false
	}
	return !expiresAt.After(now.Add(proxyRefreshBefore))
}

func refreshOnce(ctx context.Context, opts EnsureOptions) error {
	_, err := tokenrefresh.RefreshOnce(ctx, tokenrefresh.Options{
		Secrets: opts.Secrets,
		OAuth:   opts.AuthCodeConfig,
		Refresh: opts.Refresh,
		Now:     opts.Now,
	})
	return err
}

func runDeviceLogin(ctx context.Context, opts EnsureOptions) error {
	ch, err := opts.RequestDeviceCode(ctx, opts.DeviceConfig)
	if err != nil {
		return err
	}
	if opts.PrintChallenge != nil {
		opts.PrintChallenge("Code 登录", ch)
	}
	tok, err := opts.PollToken(ctx, opts.DeviceConfig, ch)
	if err != nil {
		return err
	}
	_, err = tokenrefresh.StoreToken(opts.Secrets, tok, opts.Now(), "")
	return err
}

func defaultEnsureOptions(opts EnsureOptions) EnsureOptions {
	if opts.DeviceConfig.ClientID == "" {
		opts.DeviceConfig = modelserver.DeviceConfig("")
	}
	if opts.AuthCodeConfig.ClientID == "" {
		opts.AuthCodeConfig = modelserver.OAuthConfig()
	}
	if opts.Env == nil {
		opts.Env = os.Getenv
	}
	if opts.SetEnv == nil {
		opts.SetEnv = os.Setenv
	}
	if opts.PersistEnv == nil {
		opts.PersistEnv = env.PersistUserEnv
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.RequestDeviceCode == nil {
		opts.RequestDeviceCode = oauth.RequestDeviceCode
	}
	if opts.PollToken == nil {
		opts.PollToken = oauth.PollToken
	}
	if opts.Refresh == nil {
		opts.Refresh = oauth.RefreshToken
	}
	return opts
}
