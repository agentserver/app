package slave

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/process"
	"gopkg.in/yaml.v3"
)

type ManagerDeps struct {
	Machines        *MachineStore
	Registry        *Registry
	Runner          Runner
	SlaveExe        string
	ServerURL       string
	CodexBin        string
	LocalProxyToken string
	OpenAuthURL     func(string)
}

type Manager struct {
	Machines    *MachineStore
	Registry    *Registry
	d           ManagerDeps
	slaveLockMu sync.Mutex
	slaveLocks  map[string]*sync.Mutex
}

type Runner interface {
	Start(context.Context, StartRequest) (StartResult, error)
	Stop(context.Context, int) error
}

type processInspector interface {
	InspectProcess(pid int, exe string) (processInspection, error)
}

type verifiedProcessStopper interface {
	StopProcess(context.Context, int, string) error
}

type processInspection int

const (
	processUnknown processInspection = iota
	processMissing
	processMismatch
	processMatch
)

type StartRequest struct {
	Exe        string
	ConfigPath string
	LogPath    string
	WorkDir    string
}

type StartResult struct {
	PID      int
	AuthURL  string
	AuthURLs <-chan string
	Exit     <-chan error
}

var ErrProcessNotTracked = errors.New("slave process not tracked")
var ErrProcessNotRunning = errors.New("slave process not running")
var ErrRemoteIdentityUnavailable = errors.New("slave remote identity unavailable")

type RemoteIdentity struct {
	ServerURL   string
	WorkspaceID string
	SandboxID   string
}

func NewManager(d ManagerDeps) *Manager {
	if d.Runner == nil {
		d.Runner = execRunner{}
	}
	return &Manager{Machines: d.Machines, Registry: d.Registry, d: d, slaveLocks: map[string]*sync.Mutex{}}
}

func StopProcess(ctx context.Context, pid int, expectedExe string) error {
	return (execRunner{}).StopProcess(ctx, pid, expectedExe)
}

func (m *Manager) List(context.Context) (Machine, []Slave, error) {
	if err := m.requireDeps(true, true, false); err != nil {
		return Machine{}, nil, err
	}
	machine, err := m.d.Machines.Load()
	if err != nil {
		return Machine{}, nil, err
	}
	slaves, err := m.d.Registry.List()
	if err != nil {
		return Machine{}, nil, err
	}
	slaves, err = m.reconcileDeadProcesses(slaves)
	if err != nil {
		return Machine{}, nil, err
	}
	return machine, slaves, nil
}

func (m *Manager) RemoteIdentity(_ context.Context, id string) (RemoteIdentity, error) {
	if err := m.requireDeps(false, true, false); err != nil {
		return RemoteIdentity{}, err
	}
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return RemoteIdentity{}, err
	}
	return readRemoteIdentity(sl.ConfigPath, m.d.ServerURL)
}

func (m *Manager) CreateAndStart(ctx context.Context, in CreateInput) (Slave, error) {
	if err := m.requireDeps(true, true, true); err != nil {
		return Slave{}, err
	}
	machine, err := m.d.Machines.Load()
	if err != nil {
		return Slave{}, err
	}
	sl, err := m.d.Registry.Create(machine, in)
	if err != nil {
		return Slave{}, err
	}
	if err := WriteConfig(sl, machine, ConfigInput{
		ServerURL: m.d.ServerURL,
		CodexBin:  m.d.CodexBin,
	}); err != nil {
		_, _ = m.d.Registry.Update(sl.ID, func(s *Slave) error {
			s.Status = StatusError
			s.PID = 0
			s.AuthURL = ""
			s.LastError = err.Error()
			return nil
		})
		return Slave{}, err
	}
	if err := m.configureCodex(sl); err != nil {
		_, _ = m.d.Registry.Update(sl.ID, func(s *Slave) error {
			s.Status = StatusError
			s.PID = 0
			s.AuthURL = ""
			s.LastError = err.Error()
			return nil
		})
		return Slave{}, err
	}
	return m.start(ctx, sl)
}

func (m *Manager) refreshConfig(sl Slave) error {
	if m.d.Machines == nil {
		return nil
	}
	machine, err := m.d.Machines.Load()
	if err != nil {
		return fmt.Errorf("load machine identity: %w", err)
	}
	if err := WriteConfig(sl, machine, ConfigInput{
		ServerURL: m.d.ServerURL,
		CodexBin:  m.d.CodexBin,
	}); err != nil {
		return err
	}
	return m.configureCodex(sl)
}

func (m *Manager) configureCodex(sl Slave) error {
	if strings.TrimSpace(m.d.LocalProxyToken) == "" || strings.TrimSpace(sl.Folder) == "" {
		return nil
	}
	configPath := filepath.Join(slaveCodexHome(sl.Folder), "config.toml")
	if err := codex.UpdateConfig(configPath, codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, m.d.LocalProxyToken)); err != nil {
		return fmt.Errorf("configure slave codex proxy: %w", err)
	}
	return nil
}

func (m *Manager) RefreshConfigs(ctx context.Context) error {
	if err := m.requireDeps(true, true, false); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	slaves, err := m.d.Registry.List()
	if err != nil {
		return err
	}
	var errs []error
	for _, sl := range slaves {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.refreshConfig(sl); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sl.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) Restart(ctx context.Context, id string) (Slave, error) {
	if err := m.requireDeps(false, true, true); err != nil {
		return Slave{}, err
	}
	unlock := m.lockSlave(id)
	defer unlock()
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return Slave{}, err
	}
	if reconciled, ok, err := m.reconcileDeadProcess(sl.ID, sl); err != nil {
		return Slave{}, err
	} else if ok {
		sl = reconciled
	}
	if sl.PID != 0 {
		if err := m.stopProcess(ctx, sl.PID); err != nil {
			if errors.Is(err, ErrProcessNotRunning) {
				reconciled, recErr := m.recordProcessNotRunning(sl.ID, sl.PID)
				if recErr != nil {
					return Slave{}, recErr
				}
				sl = reconciled
			} else {
				return Slave{}, err
			}
		}
	}
	if err := m.refreshConfig(sl); err != nil {
		return Slave{}, err
	}
	return m.start(ctx, sl)
}

func (m *Manager) Pause(ctx context.Context, id string) (Slave, error) {
	if err := m.requireDeps(false, true, true); err != nil {
		return Slave{}, err
	}
	unlock := m.lockSlave(id)
	defer unlock()
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return Slave{}, err
	}
	if reconciled, ok, err := m.reconcileDeadProcess(sl.ID, sl); err != nil {
		return Slave{}, err
	} else if ok {
		return reconciled, nil
	}
	if sl.PID != 0 {
		if err := m.stopProcess(ctx, sl.PID); err != nil {
			if errors.Is(err, ErrProcessNotRunning) {
				return m.recordProcessNotRunning(sl.ID, sl.PID)
			}
			return Slave{}, err
		}
	}
	return m.d.Registry.Update(id, func(s *Slave) error {
		s.Status = StatusPaused
		s.PID = 0
		s.AuthURL = ""
		s.LastError = ""
		return nil
	})
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := m.requireDeps(false, true, true); err != nil {
		return err
	}
	unlock := m.lockSlave(id)
	defer unlock()
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return err
	}
	if reconciled, ok, err := m.reconcileDeadProcess(sl.ID, sl); err != nil {
		return err
	} else if ok {
		sl = reconciled
	}
	if sl.PID != 0 {
		if err := m.stopProcess(ctx, sl.PID); err != nil {
			if errors.Is(err, ErrProcessNotRunning) {
				reconciled, recErr := m.recordProcessNotRunning(sl.ID, sl.PID)
				if recErr != nil {
					return recErr
				}
				sl = reconciled
			} else {
				return err
			}
		}
	}
	storageDir, err := m.d.Registry.storageDir(sl.ID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(storageDir); err != nil {
		_, updateErr := m.d.Registry.Update(id, func(s *Slave) error {
			s.Status = StatusError
			s.PID = 0
			s.AuthURL = ""
			s.LastError = err.Error()
			return nil
		})
		return errors.Join(err, updateErr)
	}
	_, err = m.d.Registry.Delete(id)
	return err
}

func (m *Manager) start(ctx context.Context, sl Slave) (Slave, error) {
	recordStartError := func(err error) (Slave, error) {
		_, _ = m.d.Registry.Update(sl.ID, func(s *Slave) error {
			s.Status = StatusError
			s.PID = 0
			s.AuthURL = ""
			s.LastError = err.Error()
			return nil
		})
		return Slave{}, err
	}
	if err := m.requireDeps(false, true, true); err != nil {
		return recordStartError(err)
	}
	if m.d.SlaveExe == "" {
		return recordStartError(fmt.Errorf("slave-agent.exe path required"))
	}
	if _, err := os.Stat(m.d.SlaveExe); err != nil && !errors.Is(err, os.ErrNotExist) {
		return recordStartError(fmt.Errorf("stat slave executable: %w", err))
	}
	res, err := m.d.Runner.Start(ctx, StartRequest{
		Exe:        m.d.SlaveExe,
		ConfigPath: sl.ConfigPath,
		LogPath:    sl.LogPath,
		WorkDir:    filepath.Dir(sl.ConfigPath),
	})
	if err != nil {
		return recordStartError(err)
	}
	authURL := sanitizeAuthURL(res.AuthURL)
	status := StatusStarting
	if authURL != "" {
		status = StatusAuthRequired
	}
	updated, err := m.d.Registry.Update(sl.ID, func(s *Slave) error {
		s.Status = status
		s.PID = res.PID
		s.AuthURL = authURL
		s.LastError = ""
		return nil
	})
	if err != nil {
		if res.PID != 0 {
			err = errors.Join(err, m.stopProcess(ctx, res.PID))
		}
		return Slave{}, err
	}
	if authURL != "" {
		m.openAuthURL(authURL)
	}
	m.monitor(sl.ID, sl.ConfigPath, res)
	return updated, nil
}

func (m *Manager) reconcileDeadProcesses(slaves []Slave) ([]Slave, error) {
	out := append([]Slave(nil), slaves...)
	for i := range out {
		reconciled, ok, err := m.reconcileDeadProcess(out[i].ID, out[i])
		if err != nil {
			return nil, err
		}
		if ok {
			out[i] = reconciled
		}
	}
	return out, nil
}

func (m *Manager) reconcileDeadProcess(id string, sl Slave) (Slave, bool, error) {
	if sl.PID == 0 {
		return sl, false, nil
	}
	inspector, ok := m.d.Runner.(processInspector)
	if !ok {
		return sl, false, nil
	}
	inspection, err := inspector.InspectProcess(sl.PID, m.d.SlaveExe)
	if err != nil {
		return Slave{}, false, err
	}
	switch inspection {
	case processMatch:
		return sl, false, nil
	case processMissing, processMismatch:
	default:
		return Slave{}, false, fmt.Errorf("inspect slave process %d: unknown process state", sl.PID)
	}
	updated, err := m.recordProcessNotRunning(id, sl.PID)
	if err != nil {
		return Slave{}, false, err
	}
	return updated, true, nil
}

func (m *Manager) recordProcessNotRunning(id string, pid int) (Slave, error) {
	updated, err := m.d.Registry.Update(id, func(s *Slave) error {
		if s.PID != pid {
			return errStaleProcessEvent
		}
		s.Status = StatusError
		s.PID = 0
		s.AuthURL = ""
		s.LastError = fmt.Sprintf("%v: %d", ErrProcessNotRunning, pid)
		return nil
	})
	if err != nil {
		return Slave{}, err
	}
	return updated, nil
}

func (m *Manager) stopProcess(ctx context.Context, pid int) error {
	if stopper, ok := m.d.Runner.(verifiedProcessStopper); ok {
		return stopper.StopProcess(ctx, pid, m.d.SlaveExe)
	}
	return m.d.Runner.Stop(ctx, pid)
}

func (m *Manager) requireDeps(needsMachine, needsRegistry, needsRunner bool) error {
	if m == nil {
		return fmt.Errorf("slave manager required")
	}
	if needsMachine && m.d.Machines == nil {
		return fmt.Errorf("machine store required")
	}
	if needsRegistry && m.d.Registry == nil {
		return fmt.Errorf("slave registry required")
	}
	if needsRunner && m.d.Runner == nil {
		return fmt.Errorf("slave runner required")
	}
	return nil
}

func (m *Manager) lockSlave(id string) func() {
	m.slaveLockMu.Lock()
	if m.slaveLocks == nil {
		m.slaveLocks = map[string]*sync.Mutex{}
	}
	lock := m.slaveLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		m.slaveLocks[id] = lock
	}
	m.slaveLockMu.Unlock()

	lock.Lock()
	return lock.Unlock
}

var errStaleProcessEvent = errors.New("stale slave process event")

func (m *Manager) monitor(id string, configPath string, res StartResult) {
	if res.PID == 0 {
		return
	}
	go func() {
		authURLs := res.AuthURLs
		exit := res.Exit
		var readiness <-chan time.Time
		var ticker *time.Ticker
		if configPath != "" {
			ticker = time.NewTicker(readinessPollInterval)
			defer ticker.Stop()
			readiness = ticker.C
		}
		var startup <-chan time.Time
		var startupTimer *time.Timer
		if configPath != "" && res.AuthURL == "" {
			startupTimer = time.NewTimer(readinessTimeout)
			defer startupTimer.Stop()
			startup = startupTimer.C
		}
		stopStartupTimer := func() {
			startup = nil
			if startupTimer != nil {
				startupTimer.Stop()
			}
		}
		for authURLs != nil || exit != nil || readiness != nil || startup != nil {
			select {
			case url, ok := <-authURLs:
				if !ok {
					authURLs = nil
					continue
				}
				if m.recordAuthURL(id, res.PID, url) {
					stopStartupTimer()
					m.openAuthURL(url)
				}
			case err, ok := <-exit:
				if !ok {
					exit = nil
					continue
				}
				m.recordProcessExit(id, res.PID, err)
				exit = nil
				readiness = nil
				stopStartupTimer()
			case <-readiness:
				if m.recordReady(id, res.PID, configPath) {
					readiness = nil
					stopStartupTimer()
				}
			case <-startup:
				m.recordStartupTimeout(id, res.PID)
				readiness = nil
				stopStartupTimer()
			}
		}
	}()
}

func (m *Manager) recordAuthURL(id string, pid int, url string) bool {
	url = sanitizeAuthURL(url)
	if url == "" {
		return false
	}
	changed := false
	_, err := m.d.Registry.Update(id, func(s *Slave) error {
		if s.PID != pid || s.Status == StatusPaused || s.Status == StatusRunning {
			return errStaleProcessEvent
		}
		if s.Status != StatusStarting && s.Status != StatusAuthRequired {
			return errStaleProcessEvent
		}
		if s.AuthURL == url {
			return nil
		}
		s.Status = StatusAuthRequired
		s.AuthURL = url
		s.LastError = ""
		changed = true
		return nil
	})
	return err == nil && changed
}

func (m *Manager) recordStartupTimeout(id string, pid int) {
	_, _ = m.d.Registry.Update(id, func(s *Slave) error {
		if s.PID != pid || s.Status != StatusStarting {
			return errStaleProcessEvent
		}
		s.Status = StatusError
		s.PID = 0
		s.AuthURL = ""
		s.LastError = fmt.Sprintf("slave startup timeout after %s", readinessTimeout)
		return nil
	})
}

func (m *Manager) openAuthURL(url string) {
	url = sanitizeAuthURL(url)
	if url == "" || m.d.OpenAuthURL == nil {
		return
	}
	go m.d.OpenAuthURL(url)
}

func sanitizeAuthURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return raw
	default:
		return ""
	}
}

func (m *Manager) recordReady(id string, pid int, configPath string) bool {
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return true
	}
	if sl.PID != pid || sl.Status == StatusPaused || sl.Status == StatusRunning || sl.Status == StatusError {
		return true
	}
	if sl.Status != StatusStarting && sl.Status != StatusAuthRequired {
		return true
	}
	ready, err := configHasCredentials(configPath)
	if err != nil || !ready {
		return false
	}
	_, err = m.d.Registry.Update(id, func(s *Slave) error {
		if s.PID != pid || s.Status == StatusPaused || s.Status == StatusRunning || s.Status == StatusError {
			return errStaleProcessEvent
		}
		if s.Status != StatusStarting && s.Status != StatusAuthRequired {
			return errStaleProcessEvent
		}
		s.Status = StatusRunning
		s.AuthURL = ""
		s.LastError = ""
		return nil
	})
	return err == nil || errors.Is(err, errStaleProcessEvent) || errors.Is(err, os.ErrNotExist)
}

func (m *Manager) recordProcessExit(id string, pid int, err error) {
	lastError := "slave process exited"
	if err != nil {
		lastError = fmt.Sprintf("slave process exited: %v", err)
	}
	_, _ = m.d.Registry.Update(id, func(s *Slave) error {
		if s.PID != pid || s.Status == StatusPaused {
			return errStaleProcessEvent
		}
		s.Status = StatusError
		s.PID = 0
		s.AuthURL = ""
		s.LastError = lastError
		return nil
	})
}

func configHasCredentials(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var cfg struct {
		Credentials loomCredentials `yaml:"credentials"`
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return false, err
	}
	c := cfg.Credentials
	return strings.TrimSpace(c.SandboxID) != "" &&
		strings.TrimSpace(c.TunnelToken) != "" &&
		strings.TrimSpace(c.ProxyToken) != "" &&
		strings.TrimSpace(c.WorkspaceID) != "" &&
		strings.TrimSpace(c.ShortID) != "", nil
}

func readRemoteIdentity(path, fallbackServerURL string) (RemoteIdentity, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return RemoteIdentity{}, ErrRemoteIdentityUnavailable
	}
	if err != nil {
		return RemoteIdentity{}, err
	}
	var cfg struct {
		Server      loomServer      `yaml:"server"`
		Credentials loomCredentials `yaml:"credentials"`
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return RemoteIdentity{}, err
	}
	sandboxID := strings.TrimSpace(cfg.Credentials.SandboxID)
	if sandboxID == "" {
		return RemoteIdentity{}, ErrRemoteIdentityUnavailable
	}
	serverURL := strings.TrimSpace(cfg.Server.URL)
	if serverURL == "" {
		serverURL = strings.TrimSpace(fallbackServerURL)
	}
	if serverURL == "" {
		serverURL = DefaultServerURL
	}
	return RemoteIdentity{
		ServerURL:   serverURL,
		WorkspaceID: strings.TrimSpace(cfg.Credentials.WorkspaceID),
		SandboxID:   sandboxID,
	}, nil
}

type execRunner struct{}

var startupTimeout = 3 * time.Second
var execStopWaitTimeout = 2 * time.Second
var readinessPollInterval = 100 * time.Millisecond
var readinessTimeout = 30 * time.Second

type trackedExecProcess struct {
	proc *os.Process
	done <-chan struct{}
}

var execProcesses = struct {
	sync.Mutex
	byPID map[int]trackedExecProcess
}{byPID: map[int]trackedExecProcess{}}

func (execRunner) Start(ctx context.Context, req StartRequest) (StartResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := os.MkdirAll(filepath.Dir(req.LogPath), 0o755); err != nil {
		return StartResult{}, fmt.Errorf("mkdir slave log dir: %w", err)
	}
	logFile, err := os.OpenFile(req.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return StartResult{}, fmt.Errorf("open slave log: %w", err)
	}
	cmd := exec.Command(req.Exe, req.ConfigPath)
	cmd.Dir = req.WorkDir
	process.HideWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = logFile.Close()
		return StartResult{}, fmt.Errorf("open slave stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = logFile.Close()
		return StartResult{}, fmt.Errorf("open slave stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return StartResult{}, fmt.Errorf("start slave process: %w", err)
	}

	authCh := make(chan string, 8)
	exitCh := make(chan error, 1)
	doneCopy := make(chan struct{}, 2)
	doneWait := make(chan struct{})
	logWriter := &lockedWriter{w: logFile}
	trackExecProcess(cmd.Process.Pid, trackedExecProcess{proc: cmd.Process, done: doneWait})
	go func() {
		copyAndDetectURL(stdout, logWriter, authCh)
		doneCopy <- struct{}{}
	}()
	go func() {
		copyAndDetectURL(stderr, logWriter, authCh)
		doneCopy <- struct{}{}
	}()
	go func() {
		err := cmd.Wait()
		<-doneCopy
		<-doneCopy
		close(authCh)
		_ = logFile.Close()
		untrackExecProcess(cmd.Process.Pid, doneWait)
		exitCh <- err
		close(exitCh)
		close(doneWait)
	}()

	timer := time.NewTimer(startupTimeout)
	defer timer.Stop()
	result := StartResult{PID: cmd.Process.Pid, AuthURLs: authCh, Exit: exitCh}
	for {
		select {
		case url, ok := <-authCh:
			if !ok {
				authCh = nil
				continue
			}
			result.AuthURL = url
			return result, nil
		case <-timer.C:
			select {
			case err, ok := <-exitCh:
				if ok && err != nil {
					return StartResult{}, fmt.Errorf("slave process exited before startup completed: %w", err)
				}
				if ok {
					return StartResult{}, fmt.Errorf("slave process exited before startup completed")
				}
			default:
			}
			return result, nil
		case err, ok := <-exitCh:
			if ok && err != nil {
				return StartResult{}, fmt.Errorf("slave process exited before startup completed: %w", err)
			}
			return StartResult{}, fmt.Errorf("slave process exited before startup completed")
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return StartResult{}, ctx.Err()
		}
	}
}

func (execRunner) Stop(ctx context.Context, pid int) error {
	return (execRunner{}).StopProcess(ctx, pid, "")
}

func (execRunner) StopProcess(ctx context.Context, pid int, expectedExe string) error {
	if pid == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tracked, ok := lookupExecProcess(pid)
	if !ok {
		return terminateUntrackedProcess(ctx, pid, expectedExe, execStopWaitTimeout)
	}
	if err := tracked.proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-tracked.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(execStopWaitTimeout):
		return fmt.Errorf("wait for slave process %d exit: timeout", pid)
	}
}

func (execRunner) InspectProcess(pid int, expectedExe string) (processInspection, error) {
	if pid == 0 {
		return processMissing, nil
	}
	if _, ok := lookupExecProcess(pid); ok {
		return processMatch, nil
	}
	return inspectOSProcess(pid, expectedExe)
}

func waitForProcessExit(ctx context.Context, pid int, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !osProcessExists(pid) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("wait for slave process %d exit: timeout", pid)
		case <-ticker.C:
		}
	}
}

func trackExecProcess(pid int, tracked trackedExecProcess) {
	execProcesses.Lock()
	defer execProcesses.Unlock()
	execProcesses.byPID[pid] = tracked
}

func lookupExecProcess(pid int) (trackedExecProcess, bool) {
	execProcesses.Lock()
	defer execProcesses.Unlock()
	tracked, ok := execProcesses.byPID[pid]
	return tracked, ok
}

func untrackExecProcess(pid int, done <-chan struct{}) {
	execProcesses.Lock()
	defer execProcesses.Unlock()
	tracked, ok := execProcesses.byPID[pid]
	if ok && tracked.done == done {
		delete(execProcesses.byPID, pid)
	}
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

var authURLPattern = regexp.MustCompile(`(?i)https?://\S*(?:device|user[_-]code|verification)\S*`)

func copyAndDetectURL(r io.Reader, w io.Writer, authCh chan<- string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(w, line)
		if authURLPattern.MatchString(line) {
			select {
			case authCh <- authURLPattern.FindString(line):
			default:
			}
		}
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(w, "slave log scan error: %v\n", err)
	}
}
