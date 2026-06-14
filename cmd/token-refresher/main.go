package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/modelaccess"
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
	proxyToken, err := modelaccess.EnsureLocalProxyToken(modelaccess.DefaultLocalProxyTokenPath(p.InstallRoot))
	if err != nil {
		return err
	}
	return runWithDeps(context.Background(), runDeps{
		Secrets:         secrets.New(p.SecretsFile),
		OAuth:           modelserver.OAuthConfig(),
		LocalProxyToken: proxyToken,
		LockPath:        filepath.Join(p.InstallRoot, "token-refresher.lock"),
		Logf:            log.Printf,
	})
}

type runDeps struct {
	Secrets              secrets.Store
	OAuth                oauth.AuthCodeConfig
	LocalProxyToken      string
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
	return modelaccess.RunDaemon(ctx, modelaccess.DaemonOptions{
		Secrets:              deps.Secrets,
		OAuth:                deps.OAuth,
		LocalProxyToken:      deps.LocalProxyToken,
		ProxyAddr:            deps.ProxyAddr,
		ProxyUpstreamBaseURL: deps.ProxyUpstreamBaseURL,
		LockPath:             deps.LockPath,
		Refresh:              deps.Refresh,
		Logf:                 deps.Logf,
	})
}
