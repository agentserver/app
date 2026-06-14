package headless

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/slave"
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
			if defaultName != "星池指挥官" {
				t.Fatalf("defaultName=%q, want 星池指挥官", defaultName)
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

func TestRunSlaveDefaultsNewEntryToCommanderName(t *testing.T) {
	temp := t.TempDir()
	repo := filepath.Join(temp, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeSlaveProcessRunner{}

	err := RunSlave(context.Background(), SlaveOptions{
		Paths:        testSlavePaths(temp),
		Package:      Package{SlaveAgent: filepath.Join(temp, "pkg", "slave-agent")},
		WorkDir:      repo,
		ComputerName: "host",
		NamePrompt: func(defaultName string) (string, error) {
			if defaultName != "星池指挥官" {
				t.Fatalf("defaultName=%q, want 星池指挥官", defaultName)
			}
			return "", nil
		},
		Runner: runner,
		Stdout: ioDiscard{},
	})
	if err != nil {
		t.Fatalf("RunSlave: %v", err)
	}

	configBody, err := os.ReadFile(runner.request.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	config := string(configBody)
	if !strings.Contains(config, "display_name: host-星池指挥官") {
		t.Fatalf("config missing default commander display name:\n%s", config)
	}
}

func TestRunSlaveDefaultsSecondNewEntryToFolderName(t *testing.T) {
	temp := t.TempDir()
	repoA := filepath.Join(temp, "repo-a")
	repoB := filepath.Join(temp, "repo-b")
	if err := os.Mkdir(repoA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(repoB, 0o755); err != nil {
		t.Fatal(err)
	}
	p := testSlavePaths(temp)

	firstRunner := &fakeSlaveProcessRunner{}
	if err := RunSlave(context.Background(), SlaveOptions{
		Paths:        p,
		Package:      Package{SlaveAgent: filepath.Join(temp, "pkg", "slave-agent")},
		WorkDir:      repoA,
		ComputerName: "host",
		NamePrompt: func(defaultName string) (string, error) {
			if defaultName != "星池指挥官" {
				t.Fatalf("first defaultName=%q, want 星池指挥官", defaultName)
			}
			return "", nil
		},
		Runner: firstRunner,
		Stdout: ioDiscard{},
	}); err != nil {
		t.Fatalf("first RunSlave: %v", err)
	}

	secondRunner := &fakeSlaveProcessRunner{}
	if err := RunSlave(context.Background(), SlaveOptions{
		Paths:        p,
		Package:      Package{SlaveAgent: filepath.Join(temp, "pkg", "slave-agent")},
		WorkDir:      repoB,
		ComputerName: "host",
		NamePrompt: func(defaultName string) (string, error) {
			if defaultName != "repo-b" {
				t.Fatalf("second defaultName=%q, want repo-b", defaultName)
			}
			return "", nil
		},
		Runner: secondRunner,
		Stdout: ioDiscard{},
	}); err != nil {
		t.Fatalf("second RunSlave: %v", err)
	}

	configBody, err := os.ReadFile(secondRunner.request.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	config := string(configBody)
	if !strings.Contains(config, "display_name: host-repo-b") {
		t.Fatalf("config missing second default display name:\n%s", config)
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

func TestRunSlaveDoesNotStartWhenExistingSlavePIDIsLive(t *testing.T) {
	temp := t.TempDir()
	repo := filepath.Join(temp, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	p := testSlavePaths(temp)
	machine, err := slave.NewMachineStore(p.MachineFile).Ensure("host")
	if err != nil {
		t.Fatal(err)
	}
	reg := slave.NewRegistry(p.SlavesFile, p.SlavesDir)
	existing, created, err := reg.EnsureForFolder(machine, slave.CreateInput{Folder: repo, Name: "worker"})
	if err != nil {
		t.Fatalf("EnsureForFolder: %v", err)
	}
	if !created {
		t.Fatal("expected setup to create slave")
	}
	if _, err := reg.Update(existing.ID, func(sl *slave.Slave) error {
		sl.Status = slave.StatusRunning
		sl.PID = os.Getpid()
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	runnerCalled := false
	err = RunSlave(context.Background(), SlaveOptions{
		Paths:        p,
		Package:      Package{SlaveAgent: filepath.Join(temp, "pkg", "slave-agent")},
		WorkDir:      repo,
		ComputerName: "host",
		NamePrompt: func(string) (string, error) {
			t.Fatal("NamePrompt called for existing folder")
			return "", nil
		},
		Runner: &fakeSlaveProcessRunner{onRun: func() {
			runnerCalled = true
		}},
		Stdout: ioDiscard{},
	})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("RunSlave error=%v, want already running", err)
	}
	if runnerCalled {
		t.Fatal("runner was called for existing live slave")
	}
}

func TestRunSlavePrintsDuplicateAuthURLOnce(t *testing.T) {
	temp := t.TempDir()
	repo := filepath.Join(temp, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	runner := &fakeSlaveProcessRunner{authURLs: []string{authURL, authURL}}
	var out bytes.Buffer

	err := RunSlave(context.Background(), SlaveOptions{
		Paths:        testSlavePaths(temp),
		Package:      Package{SlaveAgent: filepath.Join(temp, "pkg", "slave-agent")},
		WorkDir:      repo,
		ComputerName: "host",
		NamePrompt:   func(string) (string, error) { return "worker", nil },
		Runner:       runner,
		Stdout:       &out,
		QR:           markerQR("fake QR marker"),
	})
	if err != nil {
		t.Fatalf("RunSlave: %v", err)
	}

	gotOutput := out.String()
	if got := strings.Count(gotOutput, authURL); got != 1 {
		t.Fatalf("auth URL printed %d times, want 1:\n%s", got, gotOutput)
	}
	if got := strings.Count(gotOutput, "fake QR marker"); got != 1 {
		t.Fatalf("QR marker printed %d times, want 1:\n%s", got, gotOutput)
	}
}

func TestRunSlaveIgnoresNonAgentserverAuthURLs(t *testing.T) {
	temp := t.TempDir()
	repo := filepath.Join(temp, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeSlaveProcessRunner{authURLs: []string{
		"https://example.com/help",
		"https://example.com/device-docs",
		"ftp://agent.cs.ac.cn/device?user_code=ABCD",
		"https://agent.cs.ac.cn/help",
	}}
	var out bytes.Buffer

	err := RunSlave(context.Background(), SlaveOptions{
		Paths:        testSlavePaths(temp),
		Package:      Package{SlaveAgent: filepath.Join(temp, "pkg", "slave-agent")},
		WorkDir:      repo,
		ComputerName: "host",
		NamePrompt:   func(string) (string, error) { return "worker", nil },
		Runner:       runner,
		Stdout:       &out,
		QR:           markerQR("fake QR marker"),
	})
	if err != nil {
		t.Fatalf("RunSlave: %v", err)
	}

	gotOutput := out.String()
	if strings.Contains(gotOutput, "Agentserver 登录") {
		t.Fatalf("output printed auth title for unrelated URLs:\n%s", gotOutput)
	}
	if strings.Contains(gotOutput, "fake QR marker") {
		t.Fatalf("output printed QR for unrelated URLs:\n%s", gotOutput)
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

func TestExecSlaveRunnerForwardsOutputAuthURLAndCancels(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test requires /bin/sh")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found")
	}
	temp := t.TempDir()
	ready := filepath.Join(temp, "ready")
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	script := filepath.Join(temp, "slave-agent.sh")
	if err := os.WriteFile(script, []byte(fmt.Sprintf(`#!/bin/sh
echo stdout-line
echo stderr-line >&2
echo %s
echo %s
touch %s
sleep 30
`, authURL, authURL, ready)), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	var authURLs []string
	errCh := make(chan error, 1)
	go func() {
		errCh <- execSlaveRunner{stdout: &out}.Run(ctx, SlaveProcessRequest{
			Exe:        sh,
			ConfigPath: script,
			WorkDir:    temp,
			AuthURL: func(url string) {
				authURLs = append(authURLs, url)
			},
		})
	}()

	waitForFile(t, ready, time.Second)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error=%v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop after context cancellation")
	}

	gotOutput := out.String()
	if !strings.Contains(gotOutput, "stdout-line") {
		t.Fatalf("output missing stdout:\n%s", gotOutput)
	}
	if !strings.Contains(gotOutput, "stderr-line") {
		t.Fatalf("output missing stderr:\n%s", gotOutput)
	}
	if len(authURLs) == 0 {
		t.Fatalf("auth callback not observed; output:\n%s", gotOutput)
	}
}

func TestExecSlaveRunnerSerializesAuthCallbacks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test requires /bin/sh")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found")
	}
	temp := t.TempDir()
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	script := filepath.Join(temp, "slave-agent.sh")
	if err := os.WriteFile(script, []byte(fmt.Sprintf(`#!/bin/sh
for i in 1 2 3 4 5; do
  echo %s
  echo %s >&2
done
`, authURL, authURL)), 0o755); err != nil {
		t.Fatal(err)
	}

	var (
		mu       sync.Mutex
		inAuth   bool
		authURLs []string
	)
	err = execSlaveRunner{stdout: ioDiscard{}}.Run(context.Background(), SlaveProcessRequest{
		Exe:        sh,
		ConfigPath: script,
		WorkDir:    temp,
		AuthURL: func(url string) {
			mu.Lock()
			if inAuth {
				mu.Unlock()
				t.Errorf("AuthURL called concurrently")
				return
			}
			inAuth = true
			mu.Unlock()

			time.Sleep(2 * time.Millisecond)
			authURLs = append(authURLs, url)

			mu.Lock()
			inAuth = false
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(authURLs) == 0 {
		t.Fatal("auth callback not observed")
	}
}

func TestCopyForegroundSlaveOutputDrainsLongLinesAndDetectsLaterAuthURL(t *testing.T) {
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	input := strings.Repeat("x", 1<<20+1) + "\n" + authURL + "\n"
	var out bytes.Buffer
	var authURLs []string

	copyForegroundSlaveOutput(strings.NewReader(input), &out, func(url string) {
		authURLs = append(authURLs, url)
	})

	gotOutput := out.String()
	if !strings.Contains(gotOutput, strings.Repeat("x", 128)) {
		t.Fatalf("output missing long line content")
	}
	if !strings.Contains(gotOutput, authURL) {
		t.Fatalf("output missing auth URL after long line")
	}
	if len(authURLs) != 1 || authURLs[0] != authURL {
		t.Fatalf("authURLs=%v, want [%q]", authURLs, authURL)
	}
}

func TestSanitizeForegroundAuthURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "trims trailing period",
			raw:  "open https://agent.cs.ac.cn/device?user_code=ABCD.",
			want: "https://agent.cs.ac.cn/device?user_code=ABCD",
		},
		{
			name: "trims trailing closing paren",
			raw:  "https://agent.cs.ac.cn/verification?code=ABCD)",
			want: "https://agent.cs.ac.cn/verification?code=ABCD",
		},
		{
			name: "rejects marker in unrelated query",
			raw:  "https://agent.cs.ac.cn/help?topic=device",
			want: "",
		},
		{
			name: "rejects marker in unrelated path segment",
			raw:  "https://agent.cs.ac.cn/device-docs",
			want: "",
		},
		{
			name: "rejects unrelated path segment with auth query",
			raw:  "https://agent.cs.ac.cn/device-docs?user_code=ABCD",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeForegroundAuthURL(tt.raw); got != tt.want {
				t.Fatalf("sanitizeForegroundAuthURL(%q)=%q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCopyForegroundSlaveOutputIgnoresNonAgentserverAuthURLs(t *testing.T) {
	authURL := "https://agent.cs.ac.cn/device?user_code=ABCD"
	input := strings.Join([]string{
		"https://example.com/help",
		"https://example.com/device-docs",
		"https://agent.cs.ac.cn/help?topic=device",
		"https://agent.cs.ac.cn/device-docs",
		authURL,
	}, "\n")
	var out bytes.Buffer
	var authURLs []string

	copyForegroundSlaveOutput(strings.NewReader(input), &out, func(url string) {
		authURLs = append(authURLs, url)
	})

	if gotOutput := out.String(); !strings.Contains(gotOutput, authURL) {
		t.Fatalf("output missing accepted auth URL:\n%s", gotOutput)
	}
	if len(authURLs) != 1 || authURLs[0] != authURL {
		t.Fatalf("authURLs=%v, want [%q]", authURLs, authURL)
	}
}

type fakeSlaveProcessRunner struct {
	authURL       string
	authURLs      []string
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
	for _, authURL := range f.authURLs {
		req.AuthURL(authURL)
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

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
