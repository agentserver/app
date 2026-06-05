package main

import (
	"context"
	"errors"
	"flag"
	"log"

	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

func main() {
	_ = flag.Bool("daemon", false, "run token refresh loop")
	flag.Parse()
	if err := run(); err != nil && !errors.Is(err, tokenrefresh.ErrNoRefreshToken) {
		log.Fatalf("token-refresher: %v", err)
	}
}

func run() error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	return tokenrefresh.Run(context.Background(), tokenrefresh.Options{
		Secrets: secrets.New(p.SecretsFile),
		OAuth:   modelserver.OAuthConfig(),
		Logf:    log.Printf,
	})
}
