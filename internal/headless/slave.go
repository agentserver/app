package headless

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/terminalauth"
)

type NamePrompt func(defaultName string) (string, error)

type SlaveOptions struct {
	Paths        paths.Paths
	Package      Package
	WorkDir      string
	ComputerName string
	NamePrompt   NamePrompt
	Runner       SlaveProcessRunner
	Stdout       io.Writer
	QR           terminalauth.QRWriter
	CodexBin     string
}

type SlaveProcessRequest struct {
	Exe        string
	ConfigPath string
	WorkDir    string
	AuthURL    func(string)
}

type SlaveProcessRunner interface {
	Run(context.Context, SlaveProcessRequest) error
}

func RunSlave(ctx context.Context, opts SlaveOptions) error {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	runner := opts.Runner
	if runner == nil {
		runner = execSlaveRunner{stdout: stdout}
	}
	computerName := strings.TrimSpace(opts.ComputerName)
	if computerName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("hostname: %w", err)
		}
		computerName = hostname
	}

	canonicalFolder, err := slave.CanonicalFolder(opts.WorkDir)
	if err != nil {
		return err
	}
	machine, err := slave.NewMachineStore(opts.Paths.MachineFile).Ensure(computerName)
	if err != nil {
		return err
	}
	registry := slave.NewRegistry(opts.Paths.SlavesFile, opts.Paths.SlavesDir)

	name := ""
	if existing, ok, err := registry.FindByFolder(canonicalFolder); err != nil {
		return err
	} else if ok {
		name = existing.Name
	} else {
		name = filepath.Base(canonicalFolder)
		if opts.NamePrompt != nil {
			prompted, err := opts.NamePrompt(name)
			if err != nil {
				return err
			}
			if strings.TrimSpace(prompted) != "" {
				name = prompted
			}
		}
	}

	sl, _, err := registry.EnsureForFolder(machine, slave.CreateInput{Folder: canonicalFolder, Name: name})
	if err != nil {
		return err
	}
	if err := slave.WriteConfig(sl, machine, slave.ConfigInput{CodexBin: opts.CodexBin}); err != nil {
		return err
	}

	return runner.Run(ctx, SlaveProcessRequest{
		Exe:        opts.Package.SlaveAgent,
		ConfigPath: sl.ConfigPath,
		WorkDir:    filepath.Dir(sl.ConfigPath),
		AuthURL: func(url string) {
			terminalauth.PrintURL(stdout, "Agentserver 登录", url, "", opts.QR)
		},
	})
}

type execSlaveRunner struct {
	stdout io.Writer
}

var foregroundAuthURLPattern = regexp.MustCompile(`(?i)https?://\S*(?:device|user[_-]code|verification)\S*`)

func (r execSlaveRunner) Run(ctx context.Context, req SlaveProcessRequest) error {
	cmd := exec.CommandContext(ctx, req.Exe, req.ConfigPath)
	cmd.Dir = req.WorkDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("slave stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("slave stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start slave-agent: %w", err)
	}

	writer := r.stdout
	if writer == nil {
		writer = os.Stdout
	}
	writer = &foregroundLockedWriter{w: writer}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		copyForegroundSlaveOutput(stdout, writer, req.AuthURL)
	}()
	go func() {
		defer wg.Done()
		copyForegroundSlaveOutput(stderr, writer, req.AuthURL)
	}()

	err = cmd.Wait()
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("slave-agent exited: %w", err)
	}
	return nil
}

func copyForegroundSlaveOutput(r io.Reader, w io.Writer, authURL func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(w, line)
		if authURL != nil && foregroundAuthURLPattern.MatchString(line) {
			authURL(foregroundAuthURLPattern.FindString(line))
		}
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(w, "slave log scan error: %v\n", err)
	}
}

type foregroundLockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *foregroundLockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}
