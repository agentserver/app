package console

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/updater"
)

var errUpdaterUnavailable = errors.New("console: updater unavailable")

func (c *Controller) UpdateState(context.Context) (updater.State, error) {
	if c.d.Updates == nil || c.d.Updates.State == nil {
		return updater.State{}, errUpdaterUnavailable
	}
	state, err := c.d.Updates.State.Load()
	if err != nil {
		return updater.State{}, err
	}
	if c.d.Updates.CurrentVersion == "" {
		return state, nil
	}
	return updater.NormalizeStateForCurrentVersion(state, c.d.Updates.CurrentVersion), nil
}

func (c *Controller) CheckUpdate(ctx context.Context, automatic bool) (updater.State, error) {
	if c.d.Updates == nil {
		return updater.State{}, errUpdaterUnavailable
	}
	return c.d.Updates.Check(ctx, automatic)
}

func (c *Controller) InstallUpdate(ctx context.Context, m updater.Manifest) (updater.State, error) {
	if c.d.Updates == nil {
		return updater.State{}, errUpdaterUnavailable
	}
	updates := *c.d.Updates
	priorStartInstaller := updates.StartInstaller
	if priorStartInstaller != nil || (c.d.Slaves != nil && c.d.PendingSlaveRestartsPath != "") {
		updates.StartInstaller = func(ctx context.Context, installerPath string) error {
			wrotePendingRestarts := false
			if c.d.Slaves != nil && c.d.PendingSlaveRestartsPath != "" {
				_, slaves, err := c.d.Slaves.List(ctx)
				if err != nil {
					return fmt.Errorf("list slaves before update: %w", err)
				}
				if err := slave.WritePendingRestarts(c.d.PendingSlaveRestartsPath, m.Version, slaves, c.d.Now); err != nil {
					return fmt.Errorf("record pending slave restarts: %w", err)
				}
				wrotePendingRestarts = true
			}
			startCtx := ctx
			start := priorStartInstaller
			if start == nil {
				start = updater.StartInstaller
				startCtx = context.Background()
			}
			if err := start(startCtx, installerPath); err != nil {
				if wrotePendingRestarts {
					if removeErr := os.Remove(c.d.PendingSlaveRestartsPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
						return errors.Join(err, fmt.Errorf("remove pending slave restarts after installer start failure: %w", removeErr))
					}
				}
				return err
			}
			return nil
		}
	}
	return updates.DownloadAndStart(ctx, m)
}
