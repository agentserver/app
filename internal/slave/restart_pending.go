package slave

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const pendingRestartReasonAppUpdate = "app_update"

type PendingRestarts struct {
	Reason    string    `json:"reason"`
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	SlaveIDs  []string  `json:"slave_ids"`
}

func WritePendingRestarts(path, version string, slaves []Slave, now func() time.Time) error {
	ids := make([]string, 0, len(slaves))
	for _, sl := range slaves {
		switch sl.Status {
		case StatusRunning, StatusStarting, StatusAuthRequired:
			ids = append(ids, sl.ID)
		}
	}
	if len(ids) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return writePendingRestartFile(path, PendingRestarts{
		Reason:    pendingRestartReasonAppUpdate,
		Version:   version,
		CreatedAt: now().UTC(),
		SlaveIDs:  ids,
	})
}

func ReadPendingRestarts(path string) (PendingRestarts, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return PendingRestarts{}, err
	}
	var pending PendingRestarts
	if err := json.Unmarshal(b, &pending); err != nil {
		return PendingRestarts{}, fmt.Errorf("decode pending restarts: %w", err)
	}
	return pending, nil
}

func RestorePendingRestarts(ctx context.Context, path string, restart func(context.Context, string) error) error {
	pending, err := ReadPendingRestarts(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if restart == nil && hasPendingRestartIDs(pending.SlaveIDs) {
		return fmt.Errorf("restart callback required")
	}

	var errs []error
	failedIDs := make([]string, 0, len(pending.SlaveIDs))
	for _, id := range pending.SlaveIDs {
		if id == "" {
			continue
		}
		if err := restart(ctx, id); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			failedIDs = append(failedIDs, id)
			errs = append(errs, err)
		}
	}
	if len(failedIDs) > 0 {
		pending.SlaveIDs = failedIDs
		if err := writePendingRestartFile(path, pending); err != nil {
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	}
	if err := os.Remove(path); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func hasPendingRestartIDs(ids []string) bool {
	for _, id := range ids {
		if id != "" {
			return true
		}
	}
	return false
}

func writePendingRestartFile(path string, pending PendingRestarts) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := replaceFile(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
