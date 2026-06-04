// launcher is the user-facing entrypoint (desktop shortcut). It either:
//   - if first run: spawn onboarding-server + open browser
//   - else: exec VS Code with our user-data-dir
//
// Folder argument (right-click handler) is delegated to cmd/open-folder.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/browser"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/ui"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("launcher: %v", err)
	}
}

func run() error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.InstallRoot, 0o755); err != nil {
		return err
	}
	store := state.NewStore(p.StateFile)
	s, err := store.Load()
	if err != nil {
		return err
	}

	if s.Onboarding.Status == state.StatusComplete && s.VSCode.Path != "" {
		// Just exec VS Code with our user-data-dir (empty workspace).
		return execVSCode(s.VSCode.Path, p, "")
	}

	// Otherwise: serve onboarding UI.
	return serveOnboarding(p, store)
}

func serveOnboarding(p paths.Paths, store *state.Store) error {
	sec := secrets.New(p.SecretsFile)

	// modelserver: authorization_code + PKCE, public client registered by
	// ops on 2026-06-03 (see docs/ops/modelserver-oauth-client-registration.md).
	// 8 fixed callback ports because ops registered explicit redirect_uris
	// rather than wildcard 127.0.0.1.
	msOAuth := oauth.AuthCodeConfig{
		Endpoint:     "https://codeapi.cs.ac.cn",
		AuthPath:     "/oauth2/auth",
		TokenPath:    "/oauth2/token",
		ClientID:     "5321f7e6-3d79-4ac9-a742-04809dbf9025",
		Scope:        "project:inference offline_access",
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{53428, 53429, 53430, 53431, 53432, 53433, 53434, 53435},
		// LoginTimeout: 0 → defaults to 10 * time.Minute in StartListening
	}
	// agentserver: device-code flow at /api/oauth2/device/auth, proxied
	// to Hydra. The CLI client `agentserver-agent-cli` is pre-registered
	// by the Helm chart with grant=device_code, public (no secret),
	// scopes=openid profile agent:register.
	asOAuth := oauth.Config{
		Endpoint:  "https://agent.cs.ac.cn",
		AuthPath:  "/api/oauth2/device/auth",
		TokenPath: "/api/oauth2/token",
		ClientID:  "agentserver-agent-cli",
		Scope:     "openid profile agent:register",
	}

	installDir, err := os.Executable()
	if err != nil {
		return err
	}
	installDir = osDir(installDir)

	deps := ui.Deps{
		State:             store,
		Secrets:           sec,
		// codeapi.cs.ac.cn is the admin API host (returns JSON). code.cs.ac.cn
		// is the dashboard SPA — any path there returns the SPA index HTML,
		// which causes the modelserver client's JSON decoder to fail with
		// "invalid character '<' looking for beginning of value". This is the
		// SAME host PKCE uses (msOAuth.Endpoint above).
		MS:                modelserver.New("https://codeapi.cs.ac.cn"),
		AS:                agentserver.New("https://agent.cs.ac.cn"),
		MSOAuth:           msOAuth,
		ASOAuth:           asOAuth,
		OpenBrowser:       func(url string) { _ = browser.Open(url) },
		CodexConfigPath:   p.CodexConfigFile,
		VSCodeUserDataDir: p.VSCodeUserDataDir,
		VSCodeExtDir:      p.VSCodeExtDir,
		EmbeddedVSIXPath:  joinExe(installDir, "agentserver-vscode.vsix"),
		CodexAbsPath:      p.CodexExePath,
		LauncherExePath:   joinExe(installDir, "launcher.exe"),
		OpenFolderExePath: joinExe(installDir, "open-folder.exe"),
		IconPath:          joinExe(installDir, "icon.ico"),
	}

	orch := ui.NewRealOrchestrator(deps)
	handler := ui.NewServer(orch)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/", ln.Addr())
	fmt.Println("onboarding URL:", url)
	go func() { _ = browser.Open(url) }()
	return http.Serve(ln, handler)
}

func execVSCode(codeExe string, p paths.Paths, folder string) error {
	args := []string{
		"--user-data-dir", p.VSCodeUserDataDir,
		"--extensions-dir", p.VSCodeExtDir,
	}
	if folder != "" {
		args = append(args, folder)
	}
	cmd := exec.Command(codeExe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

// osDir returns the directory of an executable path.
func osDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}

func joinExe(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + string(os.PathSeparator) + name
}

// keep context import live for future use
var _ = context.Background
