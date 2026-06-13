package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/headless"
	"github.com/agentserver/agentserver-pkg/internal/modelaccess"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/terminalauth"
)

type app struct {
	ensureAccess    func(context.Context) error
	runSlave        func(context.Context) error
	installDriver   func(context.Context) error
	switchWorkspace func(context.Context) error
	serveDriverMCP  func(context.Context) error
	runDaemon       func(context.Context) error
}

func main() {
	if err := newApp().run(context.Background(), os.Args[1:]); err != nil {
		log.Fatalf("agentserver: %v", err)
	}
}

func newApp() app {
	p, err := paths.Default()
	if err != nil {
		return app{
			ensureAccess: func(context.Context) error { return err },
			runDaemon:    func(context.Context) error { return err },
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return app{
			ensureAccess: func(context.Context) error { return err },
			runDaemon:    func(context.Context) error { return err },
		}
	}
	pkg := headless.PackagePaths(exe)
	sec := secrets.New(p.SecretsFile)
	cachedCodex := ""

	return app{
		ensureAccess: func(ctx context.Context) error {
			if _, err := modelaccess.Ensure(ctx, modelaccess.EnsureOptions{
				CodexConfigPath: p.CodexConfigFile,
				Secrets:         sec,
				PrintChallenge: func(title string, ch oauth.DeviceCodeChallenge) {
					terminalauth.PrintChallenge(os.Stdout, title, ch, terminalauth.DefaultQR)
				},
				StartDaemon: func(ctx context.Context) error {
					return modelaccess.EnsureDaemon(ctx, modelaccess.EnsureDaemonOptions{
						ExePath:      pkg.AgentserverExe,
						ProxyBaseURL: "http://127.0.0.1:53452",
					})
				},
			}); err != nil {
				return err
			}
			codexRuntime, err := headless.ResolveCodex(ctx, headless.CodexResolveOptions{
				Paths:   p,
				Package: pkg,
			})
			if err != nil {
				return err
			}
			cachedCodex = codexRuntime.Path
			return nil
		},
		runSlave: func(ctx context.Context) error {
			wd, _ := os.Getwd()
			return headless.RunSlave(ctx, headless.SlaveOptions{
				Paths:      p,
				Package:    pkg,
				WorkDir:    wd,
				NamePrompt: promptName,
				Stdout:     os.Stdout,
				QR:         terminalauth.DefaultQR,
				CodexBin:   cachedCodex,
			})
		},
		installDriver: func(ctx context.Context) error {
			return headless.InstallDriver(ctx, headless.DriverOptions{
				Paths:   p,
				Package: pkg,
				Secrets: sec,
				Stdout:  os.Stdout,
				QR:      terminalauth.DefaultQR,
			})
		},
		switchWorkspace: func(ctx context.Context) error {
			return headless.SwitchWorkspace(ctx, headless.DriverOptions{
				Paths:   p,
				Package: pkg,
				Secrets: sec,
				Stdout:  os.Stdout,
				QR:      terminalauth.DefaultQR,
			})
		},
		serveDriverMCP: func(ctx context.Context) error {
			wd, _ := os.Getwd()
			return headless.ServeDriverMCP(ctx, headless.DriverMCPOptions{
				Paths:   p,
				Package: pkg,
				Secrets: sec,
				WorkDir: wd,
			})
		},
		runDaemon: func(ctx context.Context) error {
			return modelaccess.RunDaemon(ctx, modelaccess.DaemonOptions{
				Secrets:  sec,
				OAuth:    modelserver.OAuthConfig(),
				LockPath: filepath.Join(p.InstallRoot, "token-refresher.lock"),
			})
		},
	}
}

func (a app) run(ctx context.Context, args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	if cmd == "model-proxy-daemon" {
		return a.runDaemon(ctx)
	}

	if a.ensureAccess != nil {
		if err := a.ensureAccess(ctx); err != nil {
			return err
		}
	}
	switch cmd {
	case "":
		return a.runSlave(ctx)
	case "install-driver":
		return a.installDriver(ctx)
	case "switch-workspace":
		return a.switchWorkspace(ctx)
	case "serve-driver-mcp":
		return a.serveDriverMCP(ctx)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func promptName(defaultName string) (string, error) {
	fmt.Fprintf(os.Stdout, "Slave name [%s]: ", defaultName)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		if !errors.Is(err, io.EOF) {
			return "", err
		}
		if line == "" {
			return defaultName, nil
		}
	}
	name := strings.TrimSpace(line)
	if name == "" {
		return defaultName, nil
	}
	return name, nil
}
