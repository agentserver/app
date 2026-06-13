package headless

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
	output := newForegroundOutput(stdout)
	runner := opts.Runner
	if runner == nil {
		runner = execSlaveRunner{output: output}
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

	seenAuthURLs := map[string]struct{}{}
	return runner.Run(ctx, SlaveProcessRequest{
		Exe:        opts.Package.SlaveAgent,
		ConfigPath: sl.ConfigPath,
		WorkDir:    filepath.Dir(sl.ConfigPath),
		AuthURL: func(url string) {
			url = sanitizeForegroundAuthURL(url)
			if url == "" {
				return
			}
			output.WithLock(func(w io.Writer) {
				if _, ok := seenAuthURLs[url]; ok {
					return
				}
				seenAuthURLs[url] = struct{}{}
				terminalauth.PrintURL(w, "Agentserver 登录", url, "", opts.QR)
			})
		},
	})
}

type execSlaveRunner struct {
	stdout io.Writer
	output *foregroundOutput
}

var foregroundHTTPURLPattern = regexp.MustCompile(`(?i)https?://\S+`)

func (r execSlaveRunner) Run(ctx context.Context, req SlaveProcessRequest) error {
	cmd := exec.Command(req.Exe, req.ConfigPath)
	cmd.Dir = req.WorkDir
	configureSlaveProcess(cmd)

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

	output := r.foregroundOutput()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		copyForegroundSlaveOutput(stdout, output, req.AuthURL)
	}()
	go func() {
		defer wg.Done()
		copyForegroundSlaveOutput(stderr, output, req.AuthURL)
	}()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err = <-done:
	case <-ctx.Done():
		terminateSlaveProcess(cmd)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			killSlaveProcess(cmd)
			<-done
		}
		wg.Wait()
		return ctx.Err()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("slave-agent exited: %w", err)
	}
	return nil
}

func (r execSlaveRunner) foregroundOutput() *foregroundOutput {
	if r.output != nil {
		return r.output
	}
	writer := r.stdout
	if writer == nil {
		writer = os.Stdout
	}
	return newForegroundOutput(writer)
}

const maxForegroundAuthDetectionBytes = 1 << 20

func copyForegroundSlaveOutput(r io.Reader, w io.Writer, authURL func(string)) {
	reader := bufio.NewReaderSize(r, 64*1024)
	line := make([]byte, 0, 4096)
	flushLine := func() {
		if authURL != nil {
			for _, match := range foregroundHTTPURLPattern.FindAll(line, -1) {
				if url := sanitizeForegroundAuthURL(string(match)); url != "" {
					authURL(url)
					break
				}
			}
		}
		line = line[:0]
	}
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			_, _ = w.Write(fragment)
			remaining := maxForegroundAuthDetectionBytes - len(line)
			if remaining > 0 {
				if len(fragment) > remaining {
					line = append(line, fragment[:remaining]...)
				} else {
					line = append(line, fragment...)
				}
			}
		}
		switch {
		case err == nil:
			flushLine()
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) > 0 {
				flushLine()
			}
			return
		default:
			_, _ = fmt.Fprintf(w, "slave log read error: %v\n", err)
			return
		}
	}
}

func sanitizeForegroundAuthURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, match := range foregroundHTTPURLPattern.FindAllString(raw, -1) {
		if isForegroundAuthURL(match) {
			return match
		}
	}
	return ""
}

func isForegroundAuthURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return false
	}
	if !strings.EqualFold(parsed.Hostname(), "agent.cs.ac.cn") {
		return false
	}
	path := strings.ToLower(parsed.EscapedPath())
	query := strings.ToLower(parsed.RawQuery)
	for _, marker := range []string{"device", "user_code", "user-code", "verification"} {
		if strings.Contains(path, marker) || strings.Contains(query, marker) {
			return true
		}
	}
	return false
}

type foregroundOutput struct {
	mu sync.Mutex
	w  io.Writer
}

func newForegroundOutput(w io.Writer) *foregroundOutput {
	return &foregroundOutput{w: w}
}

func (o *foregroundOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.w.Write(p)
}

func (o *foregroundOutput) WithLock(fn func(io.Writer)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fn(o.w)
}
