package headless

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/agentserver/agentserver-pkg/internal/codexruntime"
	"github.com/agentserver/agentserver-pkg/internal/paths"
)

type Package struct {
	AgentserverExe string
	PackageDir     string
	DriverAgent    string
	SlaveAgent     string
}

type CodexRuntime struct {
	Path   string
	Source string
}

type CodexResolveOptions struct {
	Paths         paths.Paths
	Package       Package
	LookPath      func(string) (string, error)
	EnsureRuntime func(context.Context, string, string, string) (string, error)
}

func PackagePaths(agentserverExe string) Package {
	packageDir := filepath.Dir(agentserverExe)
	return Package{
		AgentserverExe: agentserverExe,
		PackageDir:     packageDir,
		DriverAgent:    filepath.Join(packageDir, packageExeName("driver-agent")),
		SlaveAgent:     filepath.Join(packageDir, packageExeName("slave-agent")),
	}
}

func (p Package) CodexManifestPath() string {
	path, err := p.codexManifestPath(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return ""
	}
	return path
}

func (p Package) codexManifestPath(goos, goarch string) (string, error) {
	name, err := codexManifestName(goos, goarch)
	if err != nil {
		return "", err
	}
	return filepath.Join(p.PackageDir, name), nil
}

func codexManifestName(goos, goarch string) (string, error) {
	if goos != "linux" {
		return "", fmt.Errorf("unsupported Codex runtime platform %s/%s", goos, goarch)
	}
	switch goarch {
	case "amd64":
		return "codex-manifest-linux-amd64.json", nil
	case "arm64":
		return "codex-manifest-linux-arm64.json", nil
	default:
		return "", fmt.Errorf("unsupported Codex runtime platform %s/%s", goos, goarch)
	}
}

func ResolveCodex(ctx context.Context, opts CodexResolveOptions) (CodexRuntime, error) {
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if codexPath, err := lookPath("codex"); err == nil {
		return CodexRuntime{Path: codexPath, Source: "path"}, nil
	}
	if err := validateManagedCodexOptions(opts); err != nil {
		return CodexRuntime{}, err
	}

	ensureRuntime := opts.EnsureRuntime
	if ensureRuntime == nil {
		ensureRuntime = func(ctx context.Context, manifestPath, destRoot, cacheDir string) (string, error) {
			res, err := codexruntime.Ensure(ctx, codexruntime.Options{
				ManifestPath: manifestPath,
				DestRoot:     destRoot,
				CacheDir:     cacheDir,
			})
			if err != nil {
				return "", err
			}
			return res.CodexExe, nil
		}
	}
	manifestPath, err := opts.Package.codexManifestPath(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return CodexRuntime{}, err
	}
	destRoot, err := managedCodexDestRoot(opts.Paths.CodexExePath)
	if err != nil {
		return CodexRuntime{}, err
	}
	codexPath, err := ensureRuntime(ctx, manifestPath, destRoot, opts.Paths.CacheDir)
	if err != nil {
		return CodexRuntime{}, err
	}
	return CodexRuntime{Path: codexPath, Source: "managed"}, nil
}

func validateManagedCodexOptions(opts CodexResolveOptions) error {
	if opts.Paths.CodexExePath == "" {
		return errors.New("CodexExePath required")
	}
	if !filepath.IsAbs(opts.Paths.CodexExePath) {
		return errors.New("CodexExePath must be absolute")
	}
	if _, err := managedCodexDestRoot(opts.Paths.CodexExePath); err != nil {
		return err
	}
	if opts.Paths.CacheDir == "" {
		return errors.New("CacheDir required")
	}
	if !filepath.IsAbs(opts.Paths.CacheDir) {
		return errors.New("CacheDir must be absolute")
	}
	if opts.Package.PackageDir == "" {
		return errors.New("PackageDir required")
	}
	if !filepath.IsAbs(opts.Package.PackageDir) {
		return errors.New("PackageDir must be absolute")
	}
	return nil
}

func managedCodexDestRoot(codexExePath string) (string, error) {
	cleanPath := filepath.Clean(codexExePath)
	codexExeName := packageExeName("codex")
	if filepath.Base(cleanPath) != codexExeName {
		return "", fmt.Errorf("CodexExePath must use managed runtime layout <root>/bin/%s", codexExeName)
	}
	binDir := filepath.Dir(cleanPath)
	if filepath.Base(binDir) != "bin" {
		return "", fmt.Errorf("CodexExePath must use managed runtime layout <root>/bin/%s", codexExeName)
	}
	destRoot := filepath.Dir(binDir)
	if destRoot == string(filepath.Separator) {
		return "", errors.New("CodexExePath managed runtime root must not be filesystem root")
	}
	return destRoot, nil
}

func packageExeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
