package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

func main() {
	_ = flag.Bool("daemon", false, "run token refresh loop")
	flag.Parse()
	if err := run(); err != nil &&
		!errors.Is(err, tokenrefresh.ErrNoRefreshToken) &&
		!errors.Is(err, tokenrefresh.ErrDaemonAlreadyRunning) {
		log.Fatalf("token-refresher: %v", err)
	}
}

func run() error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	return runWithDeps(context.Background(), runDeps{
		Secrets:  secrets.New(p.SecretsFile),
		OAuth:    modelserver.OAuthConfig(),
		LockPath: filepath.Join(p.InstallRoot, "token-refresher.lock"),
		Logf:     log.Printf,
	})
}

type runDeps struct {
	Secrets              secrets.Store
	OAuth                oauth.AuthCodeConfig
	ProxyAddr            string
	ProxyUpstreamBaseURL string
	LockPath             string
	Refresh              func(context.Context, oauth.AuthCodeConfig, string) (oauth.Token, error)
	Logf                 func(string, ...any)
}

func runWithDeps(ctx context.Context, deps runDeps) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if deps.Logf == nil {
		deps.Logf = log.Printf
	}
	if deps.LockPath != "" {
		lock, err := tokenrefresh.AcquireDaemonLock(deps.LockPath)
		if err != nil {
			return err
		}
		defer lock.Close()
	}

	proxyErr := make(chan error, 1)
	go func() {
		proxyErr <- modelproxy.ListenAndServe(ctx, modelproxy.ServerOptions{
			Addr:            deps.ProxyAddr,
			Secrets:         deps.Secrets,
			UpstreamBaseURL: deps.ProxyUpstreamBaseURL,
		})
	}()

	refreshErr := make(chan error, 1)
	go func() {
		refreshErr <- tokenrefresh.Run(ctx, tokenrefresh.Options{
			Secrets: deps.Secrets,
			OAuth:   deps.OAuth,
			Refresh: deps.Refresh,
			Logf:    deps.Logf,
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
				deps.Logf("token refresh disabled: %v", err)
				refreshErr = nil
				continue
			}
			return err
		case <-ctx.Done():
			return nil
		}
	}
}
