package main

import (
	"context"
	"strings"
	"testing"
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
	app := app{}
	if err := app.run(context.Background(), []string{"unknown"}); err == nil {
		t.Fatal("expected unknown command error")
	}
}
