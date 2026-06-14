package headless

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"

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
	CodexVersion  func(context.Context, string) (string, error)
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
		if pathCodexSatisfiesManifest(ctx, codexPath, opts) {
			return CodexRuntime{Path: codexPath, Source: "path"}, nil
		}
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

func pathCodexSatisfiesManifest(ctx context.Context, codexPath string, opts CodexResolveOptions) bool {
	manifestPath, err := opts.Package.codexManifestPath(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return true
	}
	manifest, err := codexruntime.LoadManifest(manifestPath)
	if err != nil {
		return true
	}
	want, ok := parseVersionTriple(manifest.PinnedVersion)
	if !ok {
		return true
	}
	version := opts.CodexVersion
	if version == nil {
		version = codexVersion
	}
	out, err := version(ctx, codexPath)
	if err != nil {
		return false
	}
	got, ok := parseVersionTriple(out)
	if !ok {
		return false
	}
	return compareVersionTriple(got, want) >= 0
}

func codexVersion(ctx context.Context, codexPath string) (string, error) {
	out, err := exec.CommandContext(ctx, codexPath, "--version").CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

var versionTriplePattern = regexp.MustCompile(`\d+\.\d+\.\d+`)
var versionTripleDotPattern = regexp.MustCompile(`\.`)

func parseVersionTriple(s string) ([3]int, bool) {
	var zero [3]int
	match := versionTriplePattern.FindString(s)
	if match == "" {
		return zero, false
	}
	parts := versionTripleDotPattern.Split(match, 3)
	if len(parts) != 3 {
		return zero, false
	}
	var out [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return zero, false
		}
		out[i] = n
	}
	return out, true
}

func compareVersionTriple(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
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
