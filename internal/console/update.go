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

var ErrUpdateInstallInProgress = errors.New("console: update install already in progress")

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
	if !c.updateInstallMu.TryLock() {
		return c.currentUpdateStateForInstallConflict(), ErrUpdateInstallInProgress
	}
	defer c.updateInstallMu.Unlock()

	// Copy the service so this request can compose callbacks without mutating
	// shared dependencies; State, Client, and other pointer fields stay shared.
	updates := *c.d.Updates
	wrotePendingRestarts := false
	priorBeforeInstallerStart := updates.BeforeInstallerStart
	if priorBeforeInstallerStart != nil || (c.d.Slaves != nil && c.d.PendingSlaveRestartsPath != "") {
		updates.BeforeInstallerStart = func(ctx context.Context, manifest updater.Manifest, installerPath string) error {
			if priorBeforeInstallerStart != nil {
				if err := priorBeforeInstallerStart(ctx, manifest, installerPath); err != nil {
					return err
				}
			}
			if c.d.Slaves == nil || c.d.PendingSlaveRestartsPath == "" {
				return nil
			}
			_, slaves, err := c.d.Slaves.List(ctx)
			if err != nil {
				return fmt.Errorf("list slaves before update: %w", err)
			}
			if err := slave.WritePendingRestarts(c.d.PendingSlaveRestartsPath, manifest.Version, slaves, c.d.Now); err != nil {
				return fmt.Errorf("record pending slave restarts: %w", err)
			}
			wrotePendingRestarts = true
			return nil
		}
	}
	priorStartInstaller := updates.StartInstaller
	if c.d.PendingSlaveRestartsPath != "" {
		updates.StartInstaller = func(ctx context.Context, installerPath string) error {
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

func (c *Controller) currentUpdateStateForInstallConflict() updater.State {
	state := updater.State{Status: updater.StatusDownloading}
	if c.d.Updates == nil {
		return state
	}
	state.CurrentVersion = c.d.Updates.CurrentVersion
	if c.d.Updates.State == nil {
		return state
	}
	current, err := c.d.Updates.State.Load()
	if err != nil {
		return state
	}
	if c.d.Updates.CurrentVersion == "" {
		return current
	}
	return updater.NormalizeStateForCurrentVersion(current, c.d.Updates.CurrentVersion)
}
