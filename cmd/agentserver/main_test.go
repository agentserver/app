package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCommandEnsuresAccessForUserCommands(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		command func(*app, *int, *int, *testing.T)
	}{
		{
			name: "default",
			args: []string{},
			command: func(app *app, access, calls *int, t *testing.T) {
				app.runSlave = func(context.Context) error {
					if *access != 1 {
						t.Fatalf("runSlave called after ensureAccess calls=%d, want 1", *access)
					}
					*calls = *calls + 1
					return nil
				}
			},
		},
		{
			name: "install-driver",
			args: []string{"install-driver"},
			command: func(app *app, access, calls *int, t *testing.T) {
				app.installDriver = func(context.Context) error {
					if *access != 1 {
						t.Fatalf("installDriver called after ensureAccess calls=%d, want 1", *access)
					}
					*calls = *calls + 1
					return nil
				}
			},
		},
		{
			name: "switch-workspace",
			args: []string{"switch-workspace"},
			command: func(app *app, access, calls *int, t *testing.T) {
				app.switchWorkspace = func(context.Context) error {
					if *access != 1 {
						t.Fatalf("switchWorkspace called after ensureAccess calls=%d, want 1", *access)
					}
					*calls = *calls + 1
					return nil
				}
			},
		},
		{
			name: "serve-driver-mcp",
			args: []string{"serve-driver-mcp"},
			command: func(app *app, access, calls *int, t *testing.T) {
				app.serveDriverMCP = func(context.Context) error {
					if *access != 1 {
						t.Fatalf("serveDriverMCP called after ensureAccess calls=%d, want 1", *access)
					}
					*calls = *calls + 1
					return nil
				}
			},
		},
	} {
		t.Run(strings.ReplaceAll(tc.name, "-", "_"), func(t *testing.T) {
			access := 0
			commandCalls := 0
			app := app{
				ensureAccess: func(context.Context) error { access++; return nil },
			}
			tc.command(&app, &access, &commandCalls, t)
			if err := app.run(context.Background(), tc.args); err != nil {
				t.Fatalf("run: %v", err)
			}
			if access != 1 {
				t.Fatalf("ensureAccess calls=%d, want 1", access)
			}
			if commandCalls != 1 {
				t.Fatalf("command calls=%d, want 1", commandCalls)
			}
		})
	}
}

func TestRunCommandResolvesCodexOnlyForDefaultSlave(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		wantCodex int
	}{
		{name: "default", args: []string{}, wantCodex: 1},
		{name: "install-driver", args: []string{"install-driver"}},
		{name: "switch-workspace", args: []string{"switch-workspace"}},
		{name: "serve-driver-mcp", args: []string{"serve-driver-mcp"}},
	} {
		t.Run(strings.ReplaceAll(tc.name, "-", "_"), func(t *testing.T) {
			codex := 0
			app := app{
				ensureAccess: func(context.Context) error { return nil },
				ensureCodex:  func(context.Context) error { codex++; return nil },
				runSlave:     func(context.Context) error { return nil },
				installDriver: func(context.Context) error {
					return nil
				},
				switchWorkspace: func(context.Context) error {
					return nil
				},
				serveDriverMCP: func(context.Context) error {
					return nil
				},
			}
			if err := app.run(context.Background(), tc.args); err != nil {
				t.Fatalf("run: %v", err)
			}
			if codex != tc.wantCodex {
				t.Fatalf("ensureCodex calls=%d, want %d", codex, tc.wantCodex)
			}
		})
	}
}

func TestRunCommandUsesMCPAccessForServeDriverMCP(t *testing.T) {
	access := 0
	mcpAccess := 0
	app := app{
		ensureAccess:    func(context.Context) error { access++; return nil },
		ensureMCPAccess: func(context.Context) error { mcpAccess++; return nil },
		serveDriverMCP:  func(context.Context) error { return nil },
	}
	if err := app.run(context.Background(), []string{"serve-driver-mcp"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if access != 0 {
		t.Fatalf("ensureAccess calls=%d, want 0", access)
	}
	if mcpAccess != 1 {
		t.Fatalf("ensureMCPAccess calls=%d, want 1", mcpAccess)
	}
}

func TestRunCommandSkipsAccessForDaemon(t *testing.T) {
	access := 0
	daemonCalls := 0
	app := app{
		ensureAccess: func(context.Context) error { access++; return nil },
		runDaemon:    func(context.Context) error { daemonCalls++; return nil },
	}
	if err := app.run(context.Background(), []string{"model-proxy-daemon"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if access != 0 {
		t.Fatalf("ensureAccess calls=%d, want 0", access)
	}
	if daemonCalls != 1 {
		t.Fatalf("runDaemon calls=%d, want 1", daemonCalls)
	}
}

func TestRunCommandRejectsUnknownCommand(t *testing.T) {
	access := 0
	app := app{
		ensureAccess: func(context.Context) error { access++; return nil },
	}
	if err := app.run(context.Background(), []string{"unknown"}); err == nil {
		t.Fatal("expected unknown command error")
	}
	if access != 0 {
		t.Fatalf("ensureAccess calls=%d, want 0", access)
	}
}

func TestCommandContextCancelsOnInterrupt(t *testing.T) {
	ctx, stop := commandContext(context.Background())
	defer stop()

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not canceled after interrupt")
	}
}

func TestPromptNameSkipsPromptWhenInputIsNotTerminal(t *testing.T) {
	var out bytes.Buffer
	got, err := promptNameWithTerminal(strings.NewReader("custom\n"), &out, "default", func() bool {
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "default" {
		t.Fatalf("name=%q, want default", got)
	}
	if out.Len() != 0 {
		t.Fatalf("prompt output=%q, want empty", out.String())
	}
}

func TestPromptNameReadsTerminalInput(t *testing.T) {
	var out bytes.Buffer
	got, err := promptNameWithTerminal(strings.NewReader("custom\n"), &out, "default", func() bool {
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "custom" {
		t.Fatalf("name=%q, want custom", got)
	}
	if !strings.Contains(out.String(), "Slave name [default]: ") {
		t.Fatalf("prompt output=%q", out.String())
	}
}

func TestDaemonLogfAppendsToPrivateLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "model-proxy-daemon.log")
	logf := daemonLogf(path)

	logf("refresh failed: %s", "boom")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "refresh failed: boom") {
		t.Fatalf("log body=%q", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("log mode=%#o, want 0600", got)
	}
}

func TestPromptNameWithIO(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "eof", input: "", want: "default"},
		{name: "blank", input: "\n", want: "default"},
		{name: "trimmed", input: "  custom  \n", want: "custom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := promptNameWithIO(strings.NewReader(tc.input), &out, "default")
			if err != nil {
				t.Fatalf("promptNameWithIO: %v", err)
			}
			if got != tc.want {
				t.Fatalf("name=%q, want %q", got, tc.want)
			}
			if !strings.Contains(out.String(), "Slave name [default]: ") {
				t.Fatalf("prompt output=%q", out.String())
			}
		})
	}
}
