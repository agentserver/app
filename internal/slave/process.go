package slave

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type ManagerDeps struct {
	Machines  *MachineStore
	Registry  *Registry
	Runner    Runner
	SlaveExe  string
	ServerURL string
	CodexBin  string
}

type Manager struct {
	Machines *MachineStore
	Registry *Registry
	d        ManagerDeps
}

type Runner interface {
	Start(context.Context, StartRequest) (StartResult, error)
	Stop(context.Context, int) error
}

type processTracker interface {
	IsTracked(int) bool
}

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

func NewManager(d ManagerDeps) *Manager {
	if d.Runner == nil {
		d.Runner = execRunner{}
	}
	return &Manager{Machines: d.Machines, Registry: d.Registry, d: d}
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
	return machine, slaves, nil
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
	return m.start(ctx, sl)
}

func (m *Manager) Restart(ctx context.Context, id string) (Slave, error) {
	if err := m.requireDeps(false, true, true); err != nil {
		return Slave{}, err
	}
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return Slave{}, err
	}
	if reconciled, ok, err := m.reconcileUntrackedProcess(sl.ID, sl); err != nil {
		return Slave{}, err
	} else if ok {
		return reconciled, nil
	}
	if sl.PID != 0 {
		if err := m.d.Runner.Stop(ctx, sl.PID); err != nil {
			return Slave{}, err
		}
	}
	return m.start(ctx, sl)
}

func (m *Manager) Pause(ctx context.Context, id string) (Slave, error) {
	if err := m.requireDeps(false, true, true); err != nil {
		return Slave{}, err
	}
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return Slave{}, err
	}
	if reconciled, ok, err := m.reconcileUntrackedProcess(sl.ID, sl); err != nil {
		return Slave{}, err
	} else if ok {
		return reconciled, nil
	}
	if sl.PID != 0 {
		if err := m.d.Runner.Stop(ctx, sl.PID); err != nil {
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
	sl, err := m.d.Registry.Get(id)
	if err != nil {
		return err
	}
	if reconciled, ok, err := m.reconcileUntrackedProcess(sl.ID, sl); err != nil {
		return err
	} else if ok {
		sl = reconciled
	}
	if sl.PID != 0 {
		if err := m.d.Runner.Stop(ctx, sl.PID); err != nil {
			return err
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
	status := StatusStarting
	if res.AuthURL != "" {
		status = StatusAuthRequired
	}
	updated, err := m.d.Registry.Update(sl.ID, func(s *Slave) error {
		s.Status = status
		s.PID = res.PID
		s.AuthURL = res.AuthURL
		s.LastError = ""
		return nil
	})
	if err != nil {
		if res.PID != 0 {
			err = errors.Join(err, m.d.Runner.Stop(ctx, res.PID))
		}
		return Slave{}, err
	}
	m.monitor(sl.ID, sl.ConfigPath, res)
	return updated, nil
}

func (m *Manager) reconcileUntrackedProcess(id string, sl Slave) (Slave, bool, error) {
	if sl.PID == 0 {
		return sl, false, nil
	}
	tracker, ok := m.d.Runner.(processTracker)
	if !ok || tracker.IsTracked(sl.PID) {
		return sl, false, nil
	}
	pid := sl.PID
	updated, err := m.d.Registry.Update(id, func(s *Slave) error {
		if s.PID != pid {
			return errStaleProcessEvent
		}
		s.Status = StatusError
		s.PID = 0
		s.AuthURL = ""
		s.LastError = fmt.Sprintf("%v: %d", ErrProcessNotTracked, pid)
		return nil
	})
	if err != nil {
		return Slave{}, false, err
	}
	return updated, true, nil
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
		for authURLs != nil || exit != nil || readiness != nil {
			select {
			case url, ok := <-authURLs:
				if !ok {
					authURLs = nil
					continue
				}
				m.recordAuthURL(id, res.PID, url)
			case err, ok := <-exit:
				if !ok {
					exit = nil
					continue
				}
				m.recordProcessExit(id, res.PID, err)
				exit = nil
			case <-readiness:
				if m.recordReady(id, res.PID, configPath) {
					readiness = nil
				}
			}
		}
	}()
}

func (m *Manager) recordAuthURL(id string, pid int, url string) {
	if url == "" {
		return
	}
	_, _ = m.d.Registry.Update(id, func(s *Slave) error {
		if s.PID != pid || s.Status == StatusPaused || s.Status == StatusRunning {
			return errStaleProcessEvent
		}
		if s.Status != StatusStarting && s.Status != StatusAuthRequired {
			return errStaleProcessEvent
		}
		s.Status = StatusAuthRequired
		s.AuthURL = url
		s.LastError = ""
		return nil
	})
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

type execRunner struct{}

var startupTimeout = 3 * time.Second
var execStopWaitTimeout = 2 * time.Second
var readinessPollInterval = 100 * time.Millisecond

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
	if pid == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tracked, ok := lookupExecProcess(pid)
	if !ok {
		return fmt.Errorf("%w: %d", ErrProcessNotTracked, pid)
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

func (execRunner) IsTracked(pid int) bool {
	if pid == 0 {
		return false
	}
	_, ok := lookupExecProcess(pid)
	return ok
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
}
