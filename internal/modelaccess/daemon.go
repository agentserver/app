package modelaccess

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/process"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

var ErrProxyUnavailable = errors.New("modelaccess: local model proxy unavailable")

type DaemonOptions struct {
	Secrets              secrets.Store
	OAuth                oauth.AuthCodeConfig
	ProxyAddr            string
	ProxyUpstreamBaseURL string
	LockPath             string
	Refresh              func(context.Context, oauth.AuthCodeConfig, string) (oauth.Token, error)
	Logf                 func(string, ...any)
}

func RunDaemon(ctx context.Context, opts DaemonOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Secrets == nil {
		return tokenrefresh.ErrNoSecrets
	}
	if opts.OAuth.ClientID == "" {
		opts.OAuth = modelserver.OAuthConfig()
	}
	if opts.LockPath != "" {
		lock, err := tokenrefresh.AcquireDaemonLock(opts.LockPath)
		if err != nil {
			return err
		}
		defer lock.Close()
	}

	proxyErr := make(chan error, 1)
	go func() {
		proxyErr <- modelproxy.ListenAndServe(ctx, modelproxy.ServerOptions{
			Addr:            opts.ProxyAddr,
			Secrets:         opts.Secrets,
			UpstreamBaseURL: opts.ProxyUpstreamBaseURL,
		})
	}()

	refreshErr := make(chan error, 1)
	go func() {
		refreshErr <- tokenrefresh.Run(ctx, tokenrefresh.Options{
			Secrets: opts.Secrets,
			OAuth:   opts.OAuth,
			Refresh: opts.Refresh,
			Logf:    opts.Logf,
		})
	}()

	for {
		select {
		case err := <-proxyErr:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case err := <-refreshErr:
			if err == nil || errors.Is(err, context.Canceled) {
				return nil
			}
			if errors.Is(err, tokenrefresh.ErrNoRefreshToken) {
				if opts.Logf != nil {
					opts.Logf("token refresh disabled: %v", err)
				}
				refreshErr = nil
				continue
			}
			return err
		case <-ctx.Done():
			return nil
		}
	}
}

type EnsureDaemonOptions struct {
	ExePath      string
	ProxyBaseURL string
	HealthCheck  func(context.Context, string) bool
	StartProcess func(*exec.Cmd) error
}

func EnsureDaemon(ctx context.Context, opts EnsureDaemonOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	baseURL := opts.ProxyBaseURL
	if baseURL == "" {
		baseURL = "http://" + modelproxy.DefaultListenAddr
	}
	healthCheck := opts.HealthCheck
	if healthCheck == nil {
		healthCheck = ProxyHealthy
	}
	if healthCheck(ctx, baseURL) {
		return nil
	}
	cmd := exec.CommandContext(ctx, opts.ExePath, "model-proxy-daemon")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	process.HideWindow(cmd)
	startProcess := opts.StartProcess
	if startProcess == nil {
		startProcess = func(cmd *exec.Cmd) error { return cmd.Start() }
	}
	if err := startProcess(cmd); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "address already in use") {
			return errors.Join(ErrProxyUnavailable, err)
		}
		return err
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if healthCheck(ctx, baseURL) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return ErrProxyUnavailable
}

func ProxyHealthy(ctx context.Context, baseURL string) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	baseURL = strings.TrimRight(baseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+modelproxy.HealthPath, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}

func DefaultLockPath(installRoot string) string {
	return filepath.Join(installRoot, "token-refresher.lock")
}
