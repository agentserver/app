package uninstall

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/agentserver/agentserver-pkg/internal/branding"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/shortcut"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

type Options struct {
	Paths   paths.Paths
	Secrets secrets.Store
	Out     io.Writer

	DeleteEnv func(string) error
	RemoveAll func(string) error
}

func Run(opts Options) error {
	var err error
	if opts.Paths.InstallRoot == "" {
		opts.Paths, err = paths.Default()
		if err != nil {
			return err
		}
	}
	if opts.Secrets == nil {
		opts.Secrets = secrets.New(opts.Paths.SecretsFile)
	}
	if opts.DeleteEnv == nil {
		opts.DeleteEnv = env.DeleteUserEnv
	}
	if opts.RemoveAll == nil {
		opts.RemoveAll = os.RemoveAll
	}

	var errs []error
	removeShortcut := func(name string) {
		if err := shortcut.UninstallAll(shortcut.ContextMenuInput{
			RegistryKeySuffix: "AgentserverVscode",
		}, name); err != nil {
			errs = append(errs, err)
		}
	}
	removeShortcut(branding.DisplayName)
	removeShortcut(branding.ProductID)

	for _, key := range []string{
		tokenrefresh.AccessTokenKey,
		tokenrefresh.RefreshTokenKey,
		tokenrefresh.AccessTokenExpiresAtKey,
		"agentserver_ws_api_key",
	} {
		if err := opts.Secrets.Delete(key); err != nil {
			errs = append(errs, fmt.Errorf("delete secret %s: %w", key, err))
		}
	}

	if err := opts.DeleteEnv(tokenrefresh.OpenAIAPIKeyEnv); err != nil {
		errs = append(errs, err)
	}
	if opts.Paths.InstallRoot != "" {
		if err := opts.RemoveAll(opts.Paths.InstallRoot); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", opts.Paths.InstallRoot, err))
		}
	}
	if opts.Paths.LocalAppDataRoot != "" {
		if err := opts.RemoveAll(opts.Paths.LocalAppDataRoot); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", opts.Paths.LocalAppDataRoot, err))
		}
	}
	if err := removeUninstallRegistry(branding.ProductID); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
