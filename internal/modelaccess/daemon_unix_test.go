//go:build !windows

package modelaccess

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnsureDaemonDetachesProxyDaemonOnUnix(t *testing.T) {
	healthy := false
	logPath := filepath.Join(t.TempDir(), "logs", "model-proxy-daemon.log")
	err := EnsureDaemon(context.Background(), EnsureDaemonOptions{
		ExePath:      "/opt/agentserver/agentserver",
		ProxyBaseURL: "http://127.0.0.1:1",
		LogPath:      logPath,
		HealthCheck: func(context.Context, string) bool {
			return healthy
		},
		StartProcess: func(cmd *exec.Cmd) error {
			if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
				t.Fatalf("SysProcAttr=%#v, want Setsid", cmd.SysProcAttr)
			}
			assertDevNullFile(t, "Stdin", cmd.Stdin)
			assertNamedFile(t, "Stdout", cmd.Stdout, logPath)
			assertNamedFile(t, "Stderr", cmd.Stderr, logPath)
			healthy = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureDaemon returned %v", err)
	}
}

func assertDevNullFile(t *testing.T, name string, got any) {
	t.Helper()
	assertNamedFile(t, name, got, os.DevNull)
}

func assertNamedFile(t *testing.T, name string, got any, want string) {
	t.Helper()
	f, ok := got.(*os.File)
	if !ok {
		t.Fatalf("%s=%T, want *os.File", name, got)
	}
	if f.Name() != want {
		t.Fatalf("%s file=%q, want %q", name, f.Name(), want)
	}
}
