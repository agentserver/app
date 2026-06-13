package headless

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/terminalauth"
)

func TestRunSlaveCreatesStableRegistryEntryAndWritesConfig(t *testing.T) {
	temp := t.TempDir()
	repo := filepath.Join(temp, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	runner := &fakeSlaveProcessRunner{authURL: authURL}
	var out bytes.Buffer

	err := RunSlave(context.Background(), SlaveOptions{
		Paths:        testSlavePaths(temp),
		Package:      Package{SlaveAgent: filepath.Join(temp, "pkg", "slave-agent")},
		WorkDir:      repo,
		ComputerName: "host",
		NamePrompt: func(defaultName string) (string, error) {
			if defaultName != "repo" {
				t.Fatalf("defaultName=%q, want repo", defaultName)
			}
			return "worker", nil
		},
		Runner:   runner,
		Stdout:   &out,
		QR:       markerQR("fake QR marker"),
		CodexBin: filepath.Join(temp, "codex"),
	})
	if err != nil {
		t.Fatalf("RunSlave: %v", err)
	}

	configBody, err := os.ReadFile(runner.request.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	config := string(configBody)
	if !strings.Contains(config, "display_name: host-worker") {
		t.Fatalf("config missing display name:\n%s", config)
	}
	if !strings.Contains(config, fmt.Sprintf("workdir: %s", repo)) {
		t.Fatalf("config missing workdir %q:\n%s", repo, config)
	}
	if runner.request.Exe != filepath.Join(temp, "pkg", "slave-agent") {
		t.Fatalf("Exe=%q", runner.request.Exe)
	}
	if runner.request.WorkDir != filepath.Dir(runner.request.ConfigPath) {
		t.Fatalf("WorkDir=%q, want config dir %q", runner.request.WorkDir, filepath.Dir(runner.request.ConfigPath))
	}

	gotOutput := out.String()
	if !strings.Contains(gotOutput, "Agentserver 登录") {
		t.Fatalf("output missing title:\n%s", gotOutput)
	}
	if !strings.Contains(gotOutput, authURL) {
		t.Fatalf("output missing auth URL:\n%s", gotOutput)
	}
	if !strings.Contains(gotOutput, "fake QR marker") {
		t.Fatalf("output missing QR marker:\n%s", gotOutput)
	}
}

func TestRunSlaveReusesExistingEntryWithoutPrompt(t *testing.T) {
	temp := t.TempDir()
	repo := filepath.Join(temp, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	p := testSlavePaths(temp)
	promptCalls := 0
	firstRunner := &fakeSlaveProcessRunner{}

	if err := RunSlave(context.Background(), SlaveOptions{
		Paths:        p,
		Package:      Package{SlaveAgent: filepath.Join(temp, "pkg", "slave-agent")},
		WorkDir:      repo,
		ComputerName: "host",
		NamePrompt: func(string) (string, error) {
			promptCalls++
			return "worker", nil
		},
		Runner: firstRunner,
		Stdout: ioDiscard{},
	}); err != nil {
		t.Fatalf("first RunSlave: %v", err)
	}
	if promptCalls != 1 {
		t.Fatalf("promptCalls after first run=%d, want 1", promptCalls)
	}

	secondRunner := &fakeSlaveProcessRunner{}
	if err := RunSlave(context.Background(), SlaveOptions{
		Paths:        p,
		Package:      Package{SlaveAgent: filepath.Join(temp, "pkg", "slave-agent")},
		WorkDir:      repo,
		ComputerName: "host",
		NamePrompt: func(string) (string, error) {
			t.Fatal("NamePrompt called for existing folder")
			return "", nil
		},
		Runner: secondRunner,
		Stdout: ioDiscard{},
	}); err != nil {
		t.Fatalf("second RunSlave: %v", err)
	}

	if firstRunner.request.ConfigPath == "" {
		t.Fatal("first runner config path empty")
	}
	if secondRunner.request.ConfigPath != firstRunner.request.ConfigPath {
		t.Fatalf("second config path=%q, want reused %q", secondRunner.request.ConfigPath, firstRunner.request.ConfigPath)
	}
	if promptCalls != 1 {
		t.Fatalf("promptCalls after second run=%d, want 1", promptCalls)
	}
}

func TestRunSlaveStopsChildOnContextCancel(t *testing.T) {
	temp := t.TempDir()
	repo := filepath.Join(temp, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeSlaveProcessRunner{waitForCancel: true}
	started := make(chan struct{})
	runner.onRun = func() {
		close(started)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunSlave(ctx, SlaveOptions{
			Paths:        testSlavePaths(temp),
			Package:      Package{SlaveAgent: filepath.Join(temp, "pkg", "slave-agent")},
			WorkDir:      repo,
			ComputerName: "host",
			NamePrompt:   func(string) (string, error) { return "worker", nil },
			Runner:       runner,
			Stdout:       ioDiscard{},
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("RunSlave did not enter runner")
	}
	cancel()

	var err error
	select {
	case err = <-errCh:
	case <-time.After(time.Second):
		t.Fatal("RunSlave did not stop after context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunSlave error=%v, want context.Canceled", err)
	}
	if !runner.stopped {
		t.Fatal("runner did not observe cancellation")
	}
}

type fakeSlaveProcessRunner struct {
	authURL       string
	waitForCancel bool
	request       SlaveProcessRequest
	stopped       bool
	onRun         func()
}

func (f *fakeSlaveProcessRunner) Run(ctx context.Context, req SlaveProcessRequest) error {
	f.request = req
	if f.onRun != nil {
		f.onRun()
	}
	if f.authURL != "" {
		req.AuthURL(f.authURL)
	}
	if f.waitForCancel {
		<-ctx.Done()
		f.stopped = ctx.Err() != nil
		return ctx.Err()
	}
	return nil
}

func testSlavePaths(root string) paths.Paths {
	state := filepath.Join(root, "state")
	return paths.Paths{
		MachineFile: filepath.Join(state, "machine.json"),
		SlavesFile:  filepath.Join(state, "slaves.json"),
		SlavesDir:   filepath.Join(state, "slaves"),
	}
}

func markerQR(marker string) terminalauth.QRWriter {
	return func(w interface{ Write([]byte) (int, error) }, _ string) {
		_, _ = w.Write([]byte(marker + "\n"))
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
