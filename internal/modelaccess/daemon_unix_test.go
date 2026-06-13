//go:build !windows

package modelaccess

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestEnsureDaemonDetachesProxyDaemonOnUnix(t *testing.T) {
	healthy := false
	err := EnsureDaemon(context.Background(), EnsureDaemonOptions{
		ExePath:      "/opt/agentserver/agentserver",
		ProxyBaseURL: "http://127.0.0.1:1",
		HealthCheck: func(context.Context, string) bool {
			return healthy
		},
		StartProcess: func(cmd *exec.Cmd) error {
			if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
				t.Fatalf("SysProcAttr=%#v, want Setsid", cmd.SysProcAttr)
			}
			assertDevNullFile(t, "Stdin", cmd.Stdin)
			assertDevNullFile(t, "Stdout", cmd.Stdout)
			assertDevNullFile(t, "Stderr", cmd.Stderr)
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
	f, ok := got.(*os.File)
	if !ok {
		t.Fatalf("%s=%T, want *os.File", name, got)
	}
	if f.Name() != os.DevNull {
		t.Fatalf("%s file=%q, want %q", name, f.Name(), os.DevNull)
	}
}
