package headless

import (
	"context"
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
	name := "codex-manifest-linux-amd64.json"
	if runtime.GOARCH == "arm64" {
		name = "codex-manifest-linux-arm64.json"
	}
	return filepath.Join(p.PackageDir, name)
}

func ResolveCodex(ctx context.Context, opts CodexResolveOptions) (CodexRuntime, error) {
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if codexPath, err := lookPath("codex"); err == nil {
		return CodexRuntime{Path: codexPath, Source: "path"}, nil
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
	destRoot := filepath.Dir(filepath.Dir(opts.Paths.CodexExePath))
	codexPath, err := ensureRuntime(ctx, opts.Package.CodexManifestPath(), destRoot, opts.Paths.CacheDir)
	if err != nil {
		return CodexRuntime{}, err
	}
	return CodexRuntime{Path: codexPath, Source: "managed"}, nil
}

func packageExeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
