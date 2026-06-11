package uninstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/branding"
	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/env"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/shortcut"
	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

const (
	loomPromptStartMarker     = "<!-- agentserver-app loom driver prompt:start -->"
	loomPromptEndMarker       = "<!-- agentserver-app loom driver prompt:end -->"
	managedSkillsManifestName = ".agentserver-managed-skills.json"
	managedSkillsManifestV1   = 1
)

type Options struct {
	Paths   paths.Paths
	Secrets secrets.Store
	Out     io.Writer
	AppDir  string

	DeleteEnv            func(string) error
	RemoveAll            func(string) error
	StopProcess          func(context.Context, int, string) error
	StopInstallProcesses func(context.Context, string, []string) error
}

func Run(opts Options) error {
	var err error
	if opts.Paths.InstallRoot == "" {
		opts.Paths, err = paths.Default()
		if err != nil {
			return err
		}
	}
	if opts.Secrets == nil {
		opts.Secrets = secrets.New(opts.Paths.SecretsFile)
	}
	if opts.DeleteEnv == nil {
		opts.DeleteEnv = env.DeleteUserEnv
	}
	if opts.RemoveAll == nil {
		opts.RemoveAll = os.RemoveAll
	}
	if opts.StopProcess == nil {
		opts.StopProcess = slave.StopProcess
	}
	if opts.StopInstallProcesses == nil {
		opts.StopInstallProcesses = stopInstallProcesses
	}

	var errs []error
	if err := stopRunningProcesses(context.Background(), opts); err != nil {
		errs = append(errs, err)
	}
	removeShortcut := func(name string) {
		if err := shortcut.UninstallAll(shortcut.ContextMenuInput{
			RegistryKeySuffix: "AgentserverApp",
		}, name); err != nil {
			errs = append(errs, err)
		}
	}
	removeShortcut(branding.DisplayName)
	removeShortcut(branding.ProductID)

	for _, key := range []string{
		tokenrefresh.AccessTokenKey,
		tokenrefresh.RefreshTokenKey,
		tokenrefresh.AccessTokenExpiresAtKey,
		"agentserver_ws_api_key",
		"agentserver_tunnel_token",
	} {
		if err := opts.Secrets.Delete(key); err != nil {
			errs = append(errs, fmt.Errorf("delete secret %s: %w", key, err))
		}
	}

	if err := opts.DeleteEnv(tokenrefresh.OpenAIAPIKeyEnv); err != nil {
		errs = append(errs, err)
	}
	if err := cleanupGlobalDriverSupport(opts.Paths); err != nil {
		errs = append(errs, err)
	}
	if opts.Paths.InstallRoot != "" {
		if err := opts.RemoveAll(opts.Paths.InstallRoot); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", opts.Paths.InstallRoot, err))
		}
	}
	if opts.Paths.LocalAppDataRoot != "" {
		if err := opts.RemoveAll(opts.Paths.LocalAppDataRoot); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", opts.Paths.LocalAppDataRoot, err))
		}
	}
	if err := removeUninstallRegistry(branding.ProductID); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

type managedSkillsManifest struct {
	Version int                         `json:"version"`
	Files   []managedSkillsManifestFile `json:"files"`
}

type managedSkillsManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func cleanupGlobalDriverSupport(p paths.Paths) error {
	codexDir := codexDirFromPaths(p)
	home := homeDirFromPaths(p, codexDir)
	var errs []error
	if codexDir != "" {
		if err := removeManagedPromptBlock(filepath.Join(codexDir, "AGENTS.md")); err != nil {
			errs = append(errs, err)
		}
		if err := cleanupManagedSkillsRoot(filepath.Join(codexDir, "skills")); err != nil {
			errs = append(errs, err)
		}
	}
	codexConfigFile := codexConfigFileFromPaths(p, codexDir)
	if codexConfigFile != "" {
		if err := codex.RemoveMCPServer(codexConfigFile, "driver"); err != nil {
			errs = append(errs, err)
		}
	}
	if home != "" {
		if err := cleanupManagedSkillsRoot(filepath.Join(home, ".agents", "skills")); err != nil {
			errs = append(errs, err)
		}
		if err := removeLoomDriverCredentials(home); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func codexDirFromPaths(p paths.Paths) string {
	if p.CodexDir != "" {
		return p.CodexDir
	}
	if p.CodexConfigFile != "" {
		return filepath.Dir(p.CodexConfigFile)
	}
	if p.UserHome != "" {
		return filepath.Join(p.UserHome, ".codex")
	}
	return ""
}

func homeDirFromPaths(p paths.Paths, codexDir string) string {
	if p.UserHome != "" {
		return p.UserHome
	}
	if codexDir != "" {
		return filepath.Dir(codexDir)
	}
	return ""
}

func codexConfigFileFromPaths(p paths.Paths, codexDir string) string {
	if p.CodexConfigFile != "" {
		return p.CodexConfigFile
	}
	if codexDir != "" {
		return filepath.Join(codexDir, "config.toml")
	}
	return ""
}

func removeLoomDriverCredentials(home string) error {
	loomConfigDir := filepath.Join(home, ".config", "multi-agent")
	var errs []error
	for _, path := range []string{
		filepath.Join(loomConfigDir, "driver.yaml"),
		filepath.Join(loomConfigDir, "observer.token"),
	} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove Loom driver credential %s: %w", path, err))
		}
	}
	pruneEmptyParents(loomConfigDir, filepath.Join(home, ".config"))
	return errors.Join(errs...)
}

func removeManagedPromptBlock(agentsPath string) error {
	body, err := os.ReadFile(agentsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read Codex AGENTS.md: %w", err)
	}
	next, changed, err := removeManagedPromptBlockText(string(body))
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if strings.TrimSpace(next) == "" {
		if err := os.Remove(agentsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove empty Codex AGENTS.md: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(agentsPath, []byte(next), 0o644); err != nil {
		return fmt.Errorf("write Codex AGENTS.md: %w", err)
	}
	return nil
}

func removeManagedPromptBlockText(existing string) (string, bool, error) {
	start := strings.Index(existing, loomPromptStartMarker)
	end := strings.Index(existing, loomPromptEndMarker)
	if start < 0 && end < 0 {
		return existing, false, nil
	}
	if start < 0 || end < 0 || end < start {
		return "", false, fmt.Errorf("existing Codex AGENTS.md has malformed Loom managed block")
	}
	end += len(loomPromptEndMarker)
	return existing[:start] + existing[end:], true, nil
}

func cleanupManagedSkillsRoot(skillsRoot string) error {
	manifestPath := managedSkillsManifestPath(skillsRoot)
	manifest, err := readManagedSkillsManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, file := range manifest.Files {
		rel, err := cleanManagedSkillPath(file.Path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		wantHash := strings.ToLower(strings.TrimSpace(file.SHA256))
		if wantHash == "" {
			continue
		}
		target, err := safeJoin(skillsRoot, rel)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		body, err := os.ReadFile(target)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			errs = append(errs, fmt.Errorf("read managed skill %s: %w", target, err))
			continue
		}
		if sha256Hex(body) != wantHash {
			continue
		}
		if err := os.Remove(target); err != nil {
			errs = append(errs, fmt.Errorf("remove managed skill %s: %w", target, err))
			continue
		}
		pruneEmptyParents(filepath.Dir(target), skillsRoot)
	}
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("remove managed skills manifest %s: %w", manifestPath, err))
	}
	return errors.Join(errs...)
}

func managedSkillsManifestPath(skillsRoot string) string {
	return filepath.Join(filepath.Dir(skillsRoot), managedSkillsManifestName)
}

func readManagedSkillsManifest(manifestPath string) (managedSkillsManifest, error) {
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return managedSkillsManifest{}, err
	}
	if strings.TrimSpace(string(body)) == "" {
		return managedSkillsManifest{Version: managedSkillsManifestV1}, nil
	}
	var manifest managedSkillsManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return managedSkillsManifest{}, fmt.Errorf("read managed skills manifest %s: %w", manifestPath, err)
	}
	return manifest, nil
}

func cleanManagedSkillPath(rel string) (string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" {
		return "", fmt.Errorf("managed skill manifest contains empty path")
	}
	if path.IsAbs(rel) || strings.Contains(rel, ":") {
		return "", fmt.Errorf("managed skill manifest path must be relative: %s", rel)
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("managed skill manifest path escapes skills root: %s", rel)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("managed skill manifest path escapes skills root: %s", rel)
		}
	}
	return clean, nil
}

func safeJoin(root, rel string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(rel))
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	back, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if back == "." || strings.HasPrefix(back, ".."+string(os.PathSeparator)) || back == ".." {
		return "", fmt.Errorf("target escapes destination: %s", rel)
	}
	return target, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func pruneEmptyParents(start, stop string) {
	absStop, err := filepath.Abs(stop)
	if err != nil {
		return
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return
	}
	for {
		if current == "" || current == "." {
			return
		}
		back, err := filepath.Rel(absStop, current)
		if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(os.PathSeparator)) {
			return
		}
		if err := os.Remove(current); err != nil {
			return
		}
		if current == absStop {
			return
		}
		current = filepath.Dir(current)
	}
}

func stopRunningProcesses(ctx context.Context, opts Options) error {
	var errs []error
	slaveExe := appExePath(opts.AppDir, "slave-agent.exe")
	if opts.Paths.SlavesFile != "" && opts.Paths.SlavesDir != "" && slaveExe != "" {
		reg := slave.NewRegistry(opts.Paths.SlavesFile, opts.Paths.SlavesDir)
		slaves, err := reg.List()
		if err != nil {
			errs = append(errs, fmt.Errorf("read local slaves: %w", err))
		}
		for _, sl := range slaves {
			if sl.PID == 0 {
				continue
			}
			if err := opts.StopProcess(ctx, sl.PID, slaveExe); err != nil && !errors.Is(err, slave.ErrProcessNotRunning) {
				errs = append(errs, fmt.Errorf("stop local slave %s pid %d: %w", sl.ID, sl.PID, err))
			}
		}
	}
	if opts.AppDir != "" {
		if err := opts.StopInstallProcesses(ctx, opts.AppDir, installProcessNames()); err != nil {
			errs = append(errs, fmt.Errorf("stop install processes: %w", err))
		}
	}
	if opts.Paths.LocalAppDataRoot != "" {
		if err := opts.StopInstallProcesses(ctx, opts.Paths.LocalAppDataRoot, []string{"codex.exe"}); err != nil {
			errs = append(errs, fmt.Errorf("stop local appdata processes: %w", err))
		}
	}
	return errors.Join(errs...)
}

func installProcessNames() []string {
	return []string{
		"launcher.exe",
		"onboarding-server.exe",
		"open-folder.exe",
		"slave-agent.exe",
		"driver-agent.exe",
		"token-refresher.exe",
		"codex.exe",
	}
}

func appExePath(appDir, name string) string {
	if appDir == "" {
		return ""
	}
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		name += ".exe"
	}
	return filepath.Join(appDir, name)
}
