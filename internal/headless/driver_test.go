package headless

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

func TestInstallDriverRegistersAgentAndMCPEntrypoint(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	sec := secrets.New(p.SecretsFile)
	fakeAS := &fakeDriverAgentserver{
		reg: agentserver.AgentRegistration{
			SandboxID:   "sb-1",
			TunnelToken: "tunnel-token",
			ProxyToken:  "sandbox-proxy-token",
			WorkspaceID: "ws-reg",
			ShortID:     "abc123",
		},
		whoami: agentserver.Identity{
			Workspace: agentserver.Workspace{ID: "ws-1", Name: "Workspace One"},
		},
	}
	var out bytes.Buffer

	err := InstallDriver(context.Background(), DriverOptions{
		Paths:        p,
		Package:      testDriverPackage(temp),
		Secrets:      sec,
		ComputerName: "host",
		AS:           fakeAS,
		ASOAuth:      oauth.Config{Endpoint: "https://agent.test"},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			return oauth.DeviceCodeChallenge{
				DeviceCode:              "device",
				UserCode:                "AS-CODE",
				VerificationURIComplete: "https://agent.test/device?user_code=AS-CODE",
			}, nil
		},
		PollToken: func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error) {
			return oauth.Token{AccessToken: "oauth-token"}, nil
		},
		Stdout: &out,
		QR:     driverMarkerQR("fake QR"),
	})
	if err != nil {
		t.Fatalf("InstallDriver: %v", err)
	}

	if fakeAS.registerName != "host-星池指挥官" {
		t.Fatalf("register name=%q, want host-星池指挥官", fakeAS.registerName)
	}
	if fakeAS.registerType != "custom" {
		t.Fatalf("register type=%q, want custom", fakeAS.registerType)
	}
	if !strings.Contains(out.String(), "AS-CODE") || !strings.Contains(out.String(), "fake QR") {
		t.Fatalf("output missing device code or QR marker:\n%s", out.String())
	}
	if got, _ := sec.Get("agentserver_ws_api_key"); got != "sandbox-proxy-token" {
		t.Fatalf("agentserver_ws_api_key=%q", got)
	}
	if got, _ := sec.Get("agentserver_tunnel_token"); got != "tunnel-token" {
		t.Fatalf("agentserver_tunnel_token=%q", got)
	}

	st, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Agentserver.WorkspaceID != "ws-1" || st.Agentserver.WorkspaceName != "Workspace One" {
		t.Fatalf("workspace state=%+v", st.Agentserver)
	}

	config := readTextFile(t, p.CodexConfigFile)
	for _, want := range []string{
		`[mcp_servers.driver]`,
		`command = "` + filepath.ToSlash(testDriverPackage(temp).AgentserverExe) + `"`,
		`args = ["serve-driver-mcp"]`,
		`startup_timeout_sec = 30`,
		`tool_timeout_sec = 120`,
		`enabled = true`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
}

func TestServeDriverMCPWritesSessionConfigWithCurrentWorkdir(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	pkg := testDriverPackage(temp)
	sec := secrets.New(p.SecretsFile)
	if err := sec.Set("agentserver_ws_api_key", "proxy-token"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set("agentserver_tunnel_token", "tunnel-token"); err != nil {
		t.Fatal(err)
	}
	writeDriverState(t, p, state.AgentserverState{
		BaseURL:       "https://agent.test",
		SandboxID:     "sb-1",
		ShortID:       "abc123",
		WorkspaceID:   "ws-1",
		WorkspaceName: "Workspace One",
	})
	sessionDir := filepath.Join(temp, "repo")
	if err := os.Mkdir(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	execCalled := false

	err := ServeDriverMCP(context.Background(), DriverMCPOptions{
		Paths:   p,
		Package: pkg,
		Secrets: sec,
		WorkDir: sessionDir,
		Exec: func(_ context.Context, exe string, args []string) error {
			execCalled = true
			if exe != pkg.DriverAgent {
				t.Fatalf("exe=%q, want %q", exe, pkg.DriverAgent)
			}
			if len(args) != 3 || args[0] != "serve-mcp" || args[1] != "--config" {
				t.Fatalf("args=%v, want serve-mcp --config <path>", args)
			}
			wantPrefix := filepath.Join(p.InstallRoot, "driver-mcp", "driver-")
			if !strings.HasPrefix(args[2], wantPrefix) || filepath.Ext(args[2]) != ".yaml" {
				t.Fatalf("config path=%q, want %s*.yaml", args[2], wantPrefix)
			}
			body := readTextFile(t, args[2])
			wantWorkdir := `workdir: "` + filepath.ToSlash(sessionDir) + `"`
			if !strings.Contains(body, wantWorkdir) {
				t.Fatalf("config missing %q:\n%s", wantWorkdir, body)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ServeDriverMCP: %v", err)
	}
	if !execCalled {
		t.Fatal("Exec was not called")
	}
}

func TestServeDriverMCPRemovesSessionConfigAfterExit(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	pkg := testDriverPackage(temp)
	sec := secrets.New(p.SecretsFile)
	if err := sec.Set("agentserver_ws_api_key", "proxy-token"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set("agentserver_tunnel_token", "tunnel-token"); err != nil {
		t.Fatal(err)
	}
	writeDriverState(t, p, state.AgentserverState{
		BaseURL:     "https://agent.test",
		SandboxID:   "sb-1",
		ShortID:     "abc123",
		WorkspaceID: "ws-1",
	})
	var sessionConfig string

	err := ServeDriverMCP(context.Background(), DriverMCPOptions{
		Paths:   p,
		Package: pkg,
		Secrets: sec,
		WorkDir: temp,
		Exec: func(_ context.Context, _ string, args []string) error {
			sessionConfig = args[2]
			if _, err := os.Stat(sessionConfig); err != nil {
				t.Fatalf("session config missing during Exec: %v", err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ServeDriverMCP: %v", err)
	}
	if _, err := os.Stat(sessionConfig); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session config stat after exit = %v, want not exist", err)
	}
}

func TestSwitchWorkspaceForcesDeviceLogin(t *testing.T) {
	t.Run("empty state", func(t *testing.T) {
		temp := t.TempDir()
		p := testDriverPaths(temp)
		calls := 0

		err := SwitchWorkspace(context.Background(), DriverOptions{
			Paths:        p,
			Package:      testDriverPackage(temp),
			Secrets:      secrets.New(p.SecretsFile),
			ComputerName: "host",
			AS:           fakeRegisteredDriverAS(),
			ASOAuth:      oauth.Config{Endpoint: "https://agent.test"},
			RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
				calls++
				return driverChallenge(), nil
			},
			PollToken: driverPollToken,
			Stdout:    ioDiscard{},
			QR:        driverMarkerQR("fake QR"),
		})
		if err != nil {
			t.Fatalf("SwitchWorkspace: %v", err)
		}
		if calls != 1 {
			t.Fatalf("RequestDeviceCode calls=%d, want 1", calls)
		}
	})

	t.Run("existing registration", func(t *testing.T) {
		temp := t.TempDir()
		p := testDriverPaths(temp)
		sec := secrets.New(p.SecretsFile)
		if err := sec.Set("agentserver_ws_api_key", "proxy-old"); err != nil {
			t.Fatal(err)
		}
		if err := sec.Set("agentserver_tunnel_token", "tunnel-old"); err != nil {
			t.Fatal(err)
		}
		writeDriverState(t, p, state.AgentserverState{
			BaseURL:     "https://agent.old",
			SandboxID:   "sb-old",
			ShortID:     "old123",
			WorkspaceID: "ws-old",
		})
		calls := 0

		err := SwitchWorkspace(context.Background(), DriverOptions{
			Paths:        p,
			Package:      testDriverPackage(temp),
			Secrets:      sec,
			ComputerName: "host",
			AS:           fakeRegisteredDriverAS(),
			ASOAuth:      oauth.Config{Endpoint: "https://agent.test"},
			RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
				calls++
				return driverChallenge(), nil
			},
			PollToken: driverPollToken,
			Stdout:    ioDiscard{},
			QR:        driverMarkerQR("fake QR"),
		})
		if err != nil {
			t.Fatalf("SwitchWorkspace: %v", err)
		}
		if calls != 1 {
			t.Fatalf("RequestDeviceCode calls=%d, want forced login", calls)
		}
	})
}

func TestInstallDriverSkipsDeviceFlowWhenAlreadyRegistered(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	sec := secrets.New(p.SecretsFile)
	if err := sec.Set("agentserver_ws_api_key", "proxy-token"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set("agentserver_tunnel_token", "tunnel-token"); err != nil {
		t.Fatal(err)
	}
	writeDriverState(t, p, state.AgentserverState{
		BaseURL:     "https://agent.test",
		SandboxID:   "sb-1",
		ShortID:     "abc123",
		WorkspaceID: "ws-1",
	})

	err := InstallDriver(context.Background(), DriverOptions{
		Paths:   p,
		Package: testDriverPackage(temp),
		Secrets: sec,
		AS:      fakeRegisteredDriverAS(),
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			t.Fatal("RequestDeviceCode called for existing registration")
			return oauth.DeviceCodeChallenge{}, nil
		},
		PollToken: driverPollToken,
		Stdout:    ioDiscard{},
	})
	if err != nil {
		t.Fatalf("InstallDriver: %v", err)
	}
	config := readTextFile(t, p.CodexConfigFile)
	if !strings.Contains(config, `[mcp_servers.driver]`) {
		t.Fatalf("Codex MCP config was not written:\n%s", config)
	}
}

func TestInstallDriverWritesPersistentConfigAndSupport(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	sec := secrets.New(p.SecretsFile)
	pkg := testDriverPackage(temp)
	writeTestTarGz(t, filepath.Join(pkg.PackageDir, "driver-skills.tar.gz"), map[string]string{
		"skills/demo/SKILL.md": "demo skill",
	})
	writeTestTarGz(t, filepath.Join(pkg.PackageDir, "driver-superpower-skills.tar.gz"), map[string]string{
		"skills/super/SKILL.md": "super skill",
	})
	writeTestTarGz(t, filepath.Join(pkg.PackageDir, "driver-codex-prompts.tar.gz"), map[string]string{
		"prompts-codex/AGENTS.md": "driver prompt",
	})

	err := InstallDriver(context.Background(), DriverOptions{
		Paths:        p,
		Package:      pkg,
		Secrets:      sec,
		ComputerName: "host",
		AS:           fakeRegisteredDriverAS(),
		ASOAuth:      oauth.Config{Endpoint: "https://agent.test"},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			return driverChallenge(), nil
		},
		PollToken: driverPollToken,
		Stdout:    ioDiscard{},
		QR:        driverMarkerQR("fake QR"),
	})
	if err != nil {
		t.Fatalf("InstallDriver: %v", err)
	}

	driverConfig := readTextFile(t, filepath.Join(p.UserHome, ".config", "multi-agent", "driver.yaml"))
	for _, want := range []string{
		`url: "https://agent.test"`,
		`proxy_token: "proxy-new"`,
		`workdir: "` + filepath.ToSlash(p.UserHome) + `"`,
	} {
		if !strings.Contains(driverConfig, want) {
			t.Fatalf("persistent driver config missing %q:\n%s", want, driverConfig)
		}
	}
	for _, path := range []string{
		filepath.Join(p.UserHome, ".agents", "skills", "demo", "SKILL.md"),
		filepath.Join(p.UserHome, ".codex", "skills", "super", "SKILL.md"),
		filepath.Join(p.UserHome, ".codex", "AGENTS.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected installed support file %s: %v", path, err)
		}
	}
}

func TestInstallDriverRepairsExistingDriverState(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	sec := secrets.New(p.SecretsFile)
	if err := sec.Set("agentserver_ws_api_key", "proxy-token"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set("agentserver_tunnel_token", "tunnel-token"); err != nil {
		t.Fatal(err)
	}
	writeDriverState(t, p, state.AgentserverState{
		SandboxID:   "sb-1",
		WorkspaceID: "ws-1",
	})
	fakeAS := &fakeDriverAgentserver{
		whoami: agentserver.Identity{
			Workspace: agentserver.Workspace{ID: "ws-1", Name: "Workspace One"},
		},
	}

	err := InstallDriver(context.Background(), DriverOptions{
		Paths:   p,
		Package: testDriverPackage(temp),
		Secrets: sec,
		AS:      fakeAS,
		ASOAuth: oauth.Config{Endpoint: "https://agent.test"},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			t.Fatal("RequestDeviceCode called for existing registration")
			return oauth.DeviceCodeChallenge{}, nil
		},
		PollToken: driverPollToken,
		Stdout:    ioDiscard{},
	})
	if err != nil {
		t.Fatalf("InstallDriver: %v", err)
	}

	st, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Agentserver.BaseURL != "https://agent.test" {
		t.Fatalf("BaseURL=%q, want https://agent.test", st.Agentserver.BaseURL)
	}
	if st.Agentserver.ShortID != "sb-1" {
		t.Fatalf("ShortID=%q, want sandbox fallback", st.Agentserver.ShortID)
	}
	if st.Agentserver.WorkspaceName != "Workspace One" {
		t.Fatalf("WorkspaceName=%q, want Workspace One", st.Agentserver.WorkspaceName)
	}
	if st.Agentserver.WorkspaceAPIKeySuffix != "oken" {
		t.Fatalf("WorkspaceAPIKeySuffix=%q, want oken", st.Agentserver.WorkspaceAPIKeySuffix)
	}
	if fakeAS.whoamiToken != "proxy-token" {
		t.Fatalf("Whoami token=%q, want proxy-token", fakeAS.whoamiToken)
	}
}

func TestInstallDriverRepairsMissingWorkspaceIDWithoutDeviceFlow(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	sec := secrets.New(p.SecretsFile)
	if err := sec.Set("agentserver_ws_api_key", "proxy-token"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set("agentserver_tunnel_token", "tunnel-token"); err != nil {
		t.Fatal(err)
	}
	writeDriverState(t, p, state.AgentserverState{
		BaseURL:   "https://agent.test",
		SandboxID: "sb-1",
		ShortID:   "abc123",
	})
	fakeAS := &fakeDriverAgentserver{
		whoami: agentserver.Identity{
			Workspace: agentserver.Workspace{ID: "ws-1", Name: "Workspace One"},
		},
	}

	err := InstallDriver(context.Background(), DriverOptions{
		Paths:   p,
		Package: testDriverPackage(temp),
		Secrets: sec,
		AS:      fakeAS,
		ASOAuth: oauth.Config{Endpoint: "https://agent.test"},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			t.Fatal("RequestDeviceCode called for repairable existing registration")
			return oauth.DeviceCodeChallenge{}, nil
		},
		PollToken: driverPollToken,
		Stdout:    ioDiscard{},
	})
	if err != nil {
		t.Fatalf("InstallDriver: %v", err)
	}

	st, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Agentserver.WorkspaceID != "ws-1" || st.Agentserver.WorkspaceName != "Workspace One" {
		t.Fatalf("workspace state=%+v", st.Agentserver)
	}
}

func TestInstallDriverRepairsStaleWorkspaceFromCurrentSecrets(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	sec := secrets.New(p.SecretsFile)
	if err := sec.Set("agentserver_ws_api_key", "proxy-new"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set("agentserver_tunnel_token", "tunnel-new"); err != nil {
		t.Fatal(err)
	}
	writeDriverState(t, p, state.AgentserverState{
		BaseURL:               "https://agent.test",
		SandboxID:             "sb-1",
		ShortID:               "abc123",
		WorkspaceID:           "ws-old",
		WorkspaceName:         "Old Workspace",
		WorkspaceAPIKeySuffix: "old!",
	})
	fakeAS := &fakeDriverAgentserver{
		whoami: agentserver.Identity{
			Workspace: agentserver.Workspace{ID: "ws-new", Name: "New Workspace"},
		},
	}

	err := InstallDriver(context.Background(), DriverOptions{
		Paths:   p,
		Package: testDriverPackage(temp),
		Secrets: sec,
		AS:      fakeAS,
		ASOAuth: oauth.Config{Endpoint: "https://agent.test"},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			t.Fatal("RequestDeviceCode called for repairable existing registration")
			return oauth.DeviceCodeChallenge{}, nil
		},
		PollToken: driverPollToken,
		Stdout:    ioDiscard{},
	})
	if err != nil {
		t.Fatalf("InstallDriver: %v", err)
	}

	st, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Agentserver.WorkspaceID != "ws-new" || st.Agentserver.WorkspaceName != "New Workspace" {
		t.Fatalf("workspace state=%+v", st.Agentserver)
	}
	if st.Agentserver.WorkspaceAPIKeySuffix != "-new" {
		t.Fatalf("WorkspaceAPIKeySuffix=%q, want -new", st.Agentserver.WorkspaceAPIKeySuffix)
	}
	driverConfig := readTextFile(t, filepath.Join(p.UserHome, ".config", "multi-agent", "driver.yaml"))
	if !strings.Contains(driverConfig, `workspace_id: "ws-new"`) {
		t.Fatalf("driver config kept stale workspace:\n%s", driverConfig)
	}
}

func TestInstallDriverRepairsStaleBaseURL(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	sec := secrets.New(p.SecretsFile)
	if err := sec.Set("agentserver_ws_api_key", "proxy-token"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set("agentserver_tunnel_token", "tunnel-token"); err != nil {
		t.Fatal(err)
	}
	writeDriverState(t, p, state.AgentserverState{
		BaseURL:     "https://agent.old",
		SandboxID:   "sb-1",
		ShortID:     "abc123",
		WorkspaceID: "ws-1",
	})

	err := InstallDriver(context.Background(), DriverOptions{
		Paths:   p,
		Package: testDriverPackage(temp),
		Secrets: sec,
		AS:      fakeRegisteredDriverAS(),
		ASOAuth: oauth.Config{Endpoint: "https://agent.test"},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			t.Fatal("RequestDeviceCode called for repairable existing registration")
			return oauth.DeviceCodeChallenge{}, nil
		},
		PollToken: driverPollToken,
		Stdout:    ioDiscard{},
	})
	if err != nil {
		t.Fatalf("InstallDriver: %v", err)
	}

	st, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Agentserver.BaseURL != "https://agent.test" {
		t.Fatalf("BaseURL=%q, want https://agent.test", st.Agentserver.BaseURL)
	}
	driverConfig := readTextFile(t, filepath.Join(p.UserHome, ".config", "multi-agent", "driver.yaml"))
	if strings.Contains(driverConfig, "https://agent.old") || !strings.Contains(driverConfig, `url: "https://agent.test"`) {
		t.Fatalf("driver config kept stale base URL:\n%s", driverConfig)
	}
}

func TestSwitchWorkspaceBypassesBrokenExistingSecretReads(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	sec := &writeableBrokenReadSecrets{
		err:    errors.New("old secrets unreadable"),
		values: map[string]string{},
	}
	writeDriverState(t, p, state.AgentserverState{
		BaseURL:     "https://agent.old",
		SandboxID:   "sb-old",
		ShortID:     "old123",
		WorkspaceID: "ws-old",
	})
	calls := 0

	err := SwitchWorkspace(context.Background(), DriverOptions{
		Paths:        p,
		Package:      testDriverPackage(temp),
		Secrets:      sec,
		ComputerName: "host",
		AS:           fakeRegisteredDriverAS(),
		ASOAuth:      oauth.Config{Endpoint: "https://agent.test"},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			calls++
			return driverChallenge(), nil
		},
		PollToken: driverPollToken,
		Stdout:    ioDiscard{},
		QR:        driverMarkerQR("fake QR"),
	})
	if err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	}
	if calls != 1 {
		t.Fatalf("RequestDeviceCode calls=%d, want 1", calls)
	}
	if got := sec.values["agentserver_ws_api_key"]; got != "proxy-new" {
		t.Fatalf("stored proxy token=%q, want proxy-new", got)
	}
}

func TestInstallDriverErrorsIfSecretsNil(t *testing.T) {
	err := InstallDriver(context.Background(), DriverOptions{
		Paths:   testDriverPaths(t.TempDir()),
		Package: testDriverPackage(t.TempDir()),
	})
	if err == nil {
		t.Fatal("InstallDriver succeeded with nil Secrets")
	}
	if !strings.Contains(err.Error(), "secrets store required") {
		t.Fatalf("error=%v, want secrets store required", err)
	}
}

func TestInstallDriverStoresWorkspaceFallbackFromAccessTokenClaim(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	sec := secrets.New(p.SecretsFile)
	fakeAS := &fakeDriverAgentserver{
		reg: agentserver.AgentRegistration{
			SandboxID:   "sb-1",
			TunnelToken: "tunnel-token",
			ProxyToken:  "sandbox-proxy-token",
			WorkspaceID: "ws-reg",
			ShortID:     "abc123",
		},
		whoamiErr: errors.New("whoami unavailable"),
	}

	err := InstallDriver(context.Background(), DriverOptions{
		Paths:        p,
		Package:      testDriverPackage(temp),
		Secrets:      sec,
		ComputerName: "host",
		AS:           fakeAS,
		ASOAuth:      oauth.Config{Endpoint: "https://agent.test"},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			return driverChallenge(), nil
		},
		PollToken: func(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error) {
			return oauth.Token{AccessToken: workspaceToken("ws-claim", "Claim Workspace")}, nil
		},
		Stdout: ioDiscard{},
		QR:     driverMarkerQR("fake QR"),
	})
	if err != nil {
		t.Fatalf("InstallDriver: %v", err)
	}

	st, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Agentserver.WorkspaceID != "ws-claim" || st.Agentserver.WorkspaceName != "Claim Workspace" {
		t.Fatalf("workspace state=%+v", st.Agentserver)
	}
}

func TestInstallDriverUsesSandboxIDWhenRegistrationOmitsShortID(t *testing.T) {
	temp := t.TempDir()
	p := testDriverPaths(temp)
	sec := secrets.New(p.SecretsFile)
	fakeAS := &fakeDriverAgentserver{
		reg: agentserver.AgentRegistration{
			SandboxID:   "sb-1",
			TunnelToken: "tunnel-token",
			ProxyToken:  "sandbox-proxy-token",
			WorkspaceID: "ws-reg",
		},
		whoami: agentserver.Identity{
			Workspace: agentserver.Workspace{ID: "ws-1", Name: "Workspace One"},
		},
	}

	err := InstallDriver(context.Background(), DriverOptions{
		Paths:        p,
		Package:      testDriverPackage(temp),
		Secrets:      sec,
		ComputerName: "host",
		AS:           fakeAS,
		ASOAuth:      oauth.Config{Endpoint: "https://agent.test"},
		RequestDeviceCode: func(context.Context, oauth.Config) (oauth.DeviceCodeChallenge, error) {
			return driverChallenge(), nil
		},
		PollToken: driverPollToken,
		Stdout:    ioDiscard{},
		QR:        driverMarkerQR("fake QR"),
	})
	if err != nil {
		t.Fatalf("InstallDriver: %v", err)
	}

	st, err := state.NewStore(p.StateFile).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Agentserver.ShortID != "sb-1" {
		t.Fatalf("ShortID=%q, want sandbox fallback", st.Agentserver.ShortID)
	}
}

type fakeDriverAgentserver struct {
	reg          agentserver.AgentRegistration
	whoami       agentserver.Identity
	whoamiErr    error
	whoamiToken  string
	registerName string
	registerType string
}

func (f *fakeDriverAgentserver) RegisterAgent(_ context.Context, _ string, name, typ string) (agentserver.AgentRegistration, error) {
	f.registerName = name
	f.registerType = typ
	return f.reg, nil
}

func (f *fakeDriverAgentserver) Whoami(_ context.Context, token string) (agentserver.Identity, error) {
	f.whoamiToken = token
	if f.whoamiErr != nil {
		return agentserver.Identity{}, f.whoamiErr
	}
	return f.whoami, nil
}

func fakeRegisteredDriverAS() *fakeDriverAgentserver {
	return &fakeDriverAgentserver{
		reg: agentserver.AgentRegistration{
			SandboxID:   "sb-new",
			TunnelToken: "tunnel-new",
			ProxyToken:  "proxy-new",
			WorkspaceID: "ws-new",
			ShortID:     "new123",
		},
		whoami: agentserver.Identity{
			Workspace: agentserver.Workspace{ID: "ws-new", Name: "New Workspace"},
		},
	}
}

func testDriverPaths(root string) paths.Paths {
	home := filepath.Join(root, "home")
	installRoot := filepath.Join(home, ".agentserver-app")
	return paths.Paths{
		UserHome:        home,
		InstallRoot:     installRoot,
		StateFile:       filepath.Join(installRoot, "state.json"),
		SecretsFile:     filepath.Join(installRoot, "secrets.json"),
		CodexConfigFile: filepath.Join(home, ".codex", "config.toml"),
	}
}

func testDriverPackage(root string) Package {
	packageDir := filepath.Join(root, "pkg")
	return Package{
		AgentserverExe: filepath.Join(packageDir, exeName("agentserver")),
		PackageDir:     packageDir,
		DriverAgent:    filepath.Join(packageDir, exeName("driver-agent")),
	}
}

func writeDriverState(t *testing.T, p paths.Paths, as state.AgentserverState) {
	t.Helper()
	if err := state.NewStore(p.StateFile).Update(func(s *state.State) error {
		s.Agentserver = as
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func driverChallenge() oauth.DeviceCodeChallenge {
	return oauth.DeviceCodeChallenge{
		DeviceCode:              "device",
		UserCode:                "AS-CODE",
		VerificationURIComplete: "https://agent.test/device?user_code=AS-CODE",
	}
}

func driverPollToken(context.Context, oauth.Config, oauth.DeviceCodeChallenge) (oauth.Token, error) {
	return oauth.Token{AccessToken: "oauth-token"}, nil
}

func driverMarkerQR(marker string) func(interface{ Write([]byte) (int, error) }, string) {
	return func(w interface{ Write([]byte) (int, error) }, _ string) {
		_, _ = w.Write([]byte(marker + "\n"))
	}
}

func workspaceToken(id, name string) string {
	payload, err := json.Marshal(map[string]string{
		"workspace_id":   id,
		"workspace_name": name,
	})
	if err != nil {
		panic(err)
	}
	return "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"
}

type writeableBrokenReadSecrets struct {
	err    error
	values map[string]string
}

func (s *writeableBrokenReadSecrets) Get(key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", s.err
}

func (s *writeableBrokenReadSecrets) Set(key, value string) error {
	s.values[key] = value
	return nil
}

func (s *writeableBrokenReadSecrets) Delete(key string) error {
	delete(s.values, key)
	return nil
}

func writeTestTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
}
