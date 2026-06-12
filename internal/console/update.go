package console

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/updater"
)

var errUpdaterUnavailable = errors.New("console: updater unavailable")

func (c *Controller) UpdateState(context.Context) (updater.State, error) {
	if c.d.Updates == nil || c.d.Updates.State == nil {
		return updater.State{}, errUpdaterUnavailable
	}
	return c.d.Updates.State.Load()
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
	if c.d.Slaves != nil && c.d.PendingSlaveRestartsPath != "" {
		_, slaves, err := c.d.Slaves.List(ctx)
		if err != nil {
			return updater.State{}, fmt.Errorf("list slaves before update: %w", err)
		}
		if err := slave.WritePendingRestarts(c.d.PendingSlaveRestartsPath, m.Version, slaves, c.d.Now); err != nil {
			return updater.State{}, fmt.Errorf("record pending slave restarts: %w", err)
		}
	}
	return c.d.Updates.DownloadAndStart(ctx, m)
}
