package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultCommanderURL       = "https://loom.nj.cs.ac.cn:10062/commander"
	DriverDaemonUnavailable   = "driver_unavailable"
	DriverDaemonStartFailed   = "daemon_start_failed"
	DriverDaemonStopFailed    = "daemon_stop_failed"
	DriverDaemonStateInvalid  = "state_invalid"
	DriverDaemonStatusUnknown = "driver_status_unknown"
	DriverDaemonPersistFailed = "state_persist_failed"
)

var ErrDriverDaemonStateInvalid = errors.New("console: driver daemon state invalid")

type DriverDaemonRuntime interface {
	Running(context.Context, []DriverProcessRecord) (bool, error)
	Start(context.Context) ([]DriverProcessRecord, error)
	Stop(context.Context, []DriverProcessRecord) error
}

type DriverDaemonState struct {
	Enabled          bool   `json:"enabled"`
	Running          bool   `json:"running"`
	CommanderURL     string `json:"commander_url"`
	LastErrorCode    string `json:"last_error_code,omitempty"`
	LastErrorMessage string `json:"last_error_message,omitempty"`
}

type DriverDaemonPersistedState struct {
	Enabled          bool                  `json:"enabled"`
	UpdatedAt        string                `json:"updated_at,omitempty"`
	LastErrorCode    string                `json:"last_error_code,omitempty"`
	LastErrorMessage string                `json:"last_error_message,omitempty"`
	Processes        []DriverProcessRecord `json:"processes,omitempty"`
}

type DriverProcessRecord struct {
	PID       int      `json:"pid,omitempty"`
	Exe       string   `json:"exe,omitempty"`
	Args      []string `json:"args,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
}

type DriverDaemonStore struct {
	path string
}

func NewDriverDaemonStore(path string) *DriverDaemonStore {
	return &DriverDaemonStore{path: path}
}

func (s *DriverDaemonStore) Load() (DriverDaemonPersistedState, error) {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return DriverDaemonPersistedState{Enabled: false}, os.ErrNotExist
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return DriverDaemonPersistedState{Enabled: true}, nil
	}
	if err != nil {
		return DriverDaemonPersistedState{
			Enabled:          false,
			LastErrorCode:    DriverDaemonStateInvalid,
			LastErrorMessage: safeDriverDaemonMessage(DriverDaemonStateInvalid),
		}, fmt.Errorf("%w: read state", ErrDriverDaemonStateInvalid)
	}
	var st DriverDaemonPersistedState
	if err := json.Unmarshal(b, &st); err != nil {
		return DriverDaemonPersistedState{
			Enabled:          false,
			LastErrorCode:    DriverDaemonStateInvalid,
			LastErrorMessage: safeDriverDaemonMessage(DriverDaemonStateInvalid),
		}, fmt.Errorf("%w: parse state", ErrDriverDaemonStateInvalid)
	}
	return st, nil
}

func (s *DriverDaemonStore) Save(st DriverDaemonPersistedState) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return errors.New("console: driver daemon state path required")
	}
	if st.UpdatedAt == "" {
		st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "."+filepath.Base(s.path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (c *Controller) DriverDaemonState(ctx context.Context) (DriverDaemonState, error) {
	st, loadErr := c.loadDriverDaemonState()
	if c.d.DriverDaemonRuntime == nil {
		return driverDaemonView(st, false), nil
	}
	running, err := c.d.DriverDaemonRuntime.Running(ctx, st.Processes)
	if err != nil {
		st.LastErrorCode = DriverDaemonStatusUnknown
		st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonStatusUnknown)
		return driverDaemonView(st, false), nil
	}
	if loadErr != nil && errors.Is(loadErr, ErrDriverDaemonStateInvalid) {
		return driverDaemonView(st, false), nil
	}
	return driverDaemonView(st, running), nil
}

func (c *Controller) SetDriverDaemonEnabled(ctx context.Context, enabled bool) (DriverDaemonState, error) {
	c.driverDaemonMu.Lock()
	defer c.driverDaemonMu.Unlock()

	st, err := c.loadDriverDaemonState()
	if err != nil && !errors.Is(err, ErrDriverDaemonStateInvalid) {
		st = DriverDaemonPersistedState{Enabled: false}
	}
	if c.d.DriverDaemonStore == nil {
		st.LastErrorCode = DriverDaemonUnavailable
		st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonUnavailable)
		return driverDaemonView(st, false), nil
	}
	if c.d.DriverDaemonRuntime == nil {
		st.Enabled = false
		st.LastErrorCode = DriverDaemonUnavailable
		st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonUnavailable)
		_ = c.d.DriverDaemonStore.Save(st)
		return driverDaemonView(st, false), nil
	}

	if !enabled {
		st.Enabled = false
		st.LastErrorCode = ""
		st.LastErrorMessage = ""
		if err := c.d.DriverDaemonStore.Save(st); err != nil {
			st.LastErrorCode = DriverDaemonPersistFailed
			st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonPersistFailed)
			return driverDaemonView(st, false), nil
		}
		if err := c.d.DriverDaemonRuntime.Stop(ctx, st.Processes); err != nil {
			st.LastErrorCode = DriverDaemonStopFailed
			st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonStopFailed)
			_ = c.d.DriverDaemonStore.Save(st)
			return driverDaemonView(st, false), nil
		}
		st.Processes = nil
		if err := c.d.DriverDaemonStore.Save(st); err != nil {
			st.LastErrorCode = DriverDaemonPersistFailed
			st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonPersistFailed)
			return driverDaemonView(st, false), nil
		}
		return driverDaemonView(st, false), nil
	}

	records, err := c.d.DriverDaemonRuntime.Start(ctx)
	if err != nil {
		st.Enabled = false
		st.Processes = nil
		st.LastErrorCode = DriverDaemonStartFailed
		st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonStartFailed)
		_ = c.d.DriverDaemonStore.Save(st)
		return driverDaemonView(st, false), nil
	}
	st.Processes = records
	running, err := c.d.DriverDaemonRuntime.Running(ctx, st.Processes)
	if err != nil {
		_ = c.d.DriverDaemonRuntime.Stop(ctx, st.Processes)
		st.Enabled = false
		st.LastErrorCode = DriverDaemonStatusUnknown
		st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonStatusUnknown)
		_ = c.d.DriverDaemonStore.Save(st)
		return driverDaemonView(st, false), nil
	}
	if !running {
		_ = c.d.DriverDaemonRuntime.Stop(ctx, st.Processes)
		st.Enabled = false
		st.LastErrorCode = DriverDaemonUnavailable
		st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonUnavailable)
		_ = c.d.DriverDaemonStore.Save(st)
		return driverDaemonView(st, false), nil
	}
	st.Enabled = true
	st.LastErrorCode = ""
	st.LastErrorMessage = ""
	if err := c.d.DriverDaemonStore.Save(st); err != nil {
		st.Enabled = false
		st.LastErrorCode = DriverDaemonPersistFailed
		st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonPersistFailed)
		return driverDaemonView(st, false), nil
	}
	return driverDaemonView(st, running), nil
}

func (c *Controller) loadDriverDaemonState() (DriverDaemonPersistedState, error) {
	if c.d.DriverDaemonStore == nil {
		return DriverDaemonPersistedState{
			Enabled:          false,
			LastErrorCode:    DriverDaemonUnavailable,
			LastErrorMessage: safeDriverDaemonMessage(DriverDaemonUnavailable),
		}, nil
	}
	st, err := c.d.DriverDaemonStore.Load()
	if err != nil {
		if errors.Is(err, ErrDriverDaemonStateInvalid) {
			return st, err
		}
		st.Enabled = false
		st.LastErrorCode = DriverDaemonUnavailable
		st.LastErrorMessage = safeDriverDaemonMessage(DriverDaemonUnavailable)
		return st, err
	}
	return st, nil
}

func driverDaemonView(st DriverDaemonPersistedState, running bool) DriverDaemonState {
	return DriverDaemonState{
		Enabled:          st.Enabled,
		Running:          running,
		CommanderURL:     DefaultCommanderURL,
		LastErrorCode:    st.LastErrorCode,
		LastErrorMessage: st.LastErrorMessage,
	}
}

func safeDriverDaemonMessage(code string) string {
	switch code {
	case DriverDaemonStartFailed:
		return "远程控制启动失败。"
	case DriverDaemonStopFailed:
		return "远程控制关闭失败。"
	case DriverDaemonStateInvalid:
		return "远程控制状态文件无效，请重新设置开关。"
	case DriverDaemonStatusUnknown:
		return "远程控制状态暂不可确认。"
	case DriverDaemonPersistFailed:
		return "远程控制状态保存失败。"
	default:
		return "远程控制暂不可用。"
	}
}
