package headless

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/codexruntime"
	"github.com/agentserver/agentserver-pkg/internal/paths"
)

func TestPackagePathsResolvesSiblingBinaries(t *testing.T) {
	exe := filepath.Join(string(filepath.Separator), "opt", "agentserver", exeName("agentserver"))

	pkg := PackagePaths(exe)

	wantDir := filepath.Dir(exe)
	if pkg.AgentserverExe != exe {
		t.Fatalf("AgentserverExe=%q, want %q", pkg.AgentserverExe, exe)
	}
	if pkg.PackageDir != wantDir {
		t.Fatalf("PackageDir=%q, want %q", pkg.PackageDir, wantDir)
	}
	if pkg.DriverAgent != filepath.Join(wantDir, exeName("driver-agent")) {
		t.Fatalf("DriverAgent=%q", pkg.DriverAgent)
	}
	if pkg.SlaveAgent != filepath.Join(wantDir, exeName("slave-agent")) {
		t.Fatalf("SlaveAgent=%q", pkg.SlaveAgent)
	}
}

func TestResolveCodexPrefersPathCodex(t *testing.T) {
	ctx := context.Background()
	pathCodex := filepath.Join(t.TempDir(), exeName("codex"))
	ensureCalled := false

	got, err := ResolveCodex(ctx, CodexResolveOptions{
		Paths: paths.Paths{
			CodexExePath: filepath.Join(t.TempDir(), "bin-root", "bin", exeName("codex")),
			CacheDir:     filepath.Join(t.TempDir(), "cache"),
		},
		Package: PackagePaths(filepath.Join(string(filepath.Separator), "opt", "agentserver", exeName("agentserver"))),
		LookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("LookPath name=%q, want codex", name)
			}
			return pathCodex, nil
		},
		EnsureRuntime: func(context.Context, string, string, string) (string, error) {
			ensureCalled = true
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ensureCalled {
		t.Fatal("EnsureRuntime called even though codex was on PATH")
	}
	if got.Path != pathCodex {
		t.Fatalf("Path=%q, want %q", got.Path, pathCodex)
	}
	if got.Source != "path" {
		t.Fatalf("Source=%q, want path", got.Source)
	}
}

func TestResolveCodexEnsuresManagedRuntimeWhenPathMissing(t *testing.T) {
	ctx := context.Background()
	temp := t.TempDir()
	codexExe := filepath.Join(temp, "bin-root", "bin", exeName("codex"))
	cacheDir := filepath.Join(temp, "cache")
	pkg := PackagePaths(filepath.Join(string(filepath.Separator), "opt", "agentserver", exeName("agentserver")))
	managedCodex := filepath.Join(temp, "managed", exeName("codex"))
	var gotManifest, gotDestRoot, gotCacheDir string

	got, err := ResolveCodex(ctx, CodexResolveOptions{
		Paths: paths.Paths{
			CodexExePath: codexExe,
			CacheDir:     cacheDir,
		},
		Package: pkg,
		LookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("LookPath name=%q, want codex", name)
			}
			return "", exec.ErrNotFound
		},
		EnsureRuntime: func(_ context.Context, manifestPath, destRoot, cache string) (string, error) {
			gotManifest = manifestPath
			gotDestRoot = destRoot
			gotCacheDir = cache
			return managedCodex, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != managedCodex {
		t.Fatalf("Path=%q, want %q", got.Path, managedCodex)
	}
	if got.Source != "managed" {
		t.Fatalf("Source=%q, want managed", got.Source)
	}
	if gotManifest != pkg.CodexManifestPath() {
		t.Fatalf("manifest=%q, want %q", gotManifest, pkg.CodexManifestPath())
	}
	if gotDestRoot != filepath.Dir(filepath.Dir(codexExe)) {
		t.Fatalf("destRoot=%q, want %q", gotDestRoot, filepath.Dir(filepath.Dir(codexExe)))
	}
	if gotCacheDir != cacheDir {
		t.Fatalf("cacheDir=%q, want %q", gotCacheDir, cacheDir)
	}
}

func TestResolveCodexRequiresManagedRuntimePathsWhenPathMissing(t *testing.T) {
	validPaths := paths.Paths{
		CodexExePath: filepath.Join(t.TempDir(), "bin-root", "bin", exeName("codex")),
		CacheDir:     filepath.Join(t.TempDir(), "cache"),
	}
	validPackage := PackagePaths(filepath.Join(string(filepath.Separator), "opt", "agentserver", exeName("agentserver")))
	tests := []struct {
		name    string
		paths   paths.Paths
		pkg     Package
		wantErr string
	}{
		{
			name: "codex exe path",
			paths: paths.Paths{
				CacheDir: validPaths.CacheDir,
			},
			pkg:     validPackage,
			wantErr: "CodexExePath required",
		},
		{
			name: "cache dir",
			paths: paths.Paths{
				CodexExePath: validPaths.CodexExePath,
			},
			pkg:     validPackage,
			wantErr: "CacheDir required",
		},
		{
			name:    "package dir",
			paths:   validPaths,
			pkg:     Package{},
			wantErr: "PackageDir required",
		},
		{
			name: "relative codex exe path",
			paths: paths.Paths{
				CodexExePath: filepath.Join("bin-root", "bin", exeName("codex")),
				CacheDir:     validPaths.CacheDir,
			},
			pkg:     validPackage,
			wantErr: "CodexExePath",
		},
		{
			name: "parent relative codex exe path",
			paths: paths.Paths{
				CodexExePath: filepath.Join("..", "bin-root", "bin", exeName("codex")),
				CacheDir:     validPaths.CacheDir,
			},
			pkg:     validPackage,
			wantErr: "CodexExePath",
		},
		{
			name: "codex exe path at filesystem root",
			paths: paths.Paths{
				CodexExePath: filepath.Join(string(filepath.Separator), exeName("codex")),
				CacheDir:     validPaths.CacheDir,
			},
			pkg:     validPackage,
			wantErr: "CodexExePath",
		},
		{
			name: "codex exe path without bin directory",
			paths: paths.Paths{
				CodexExePath: filepath.Join(string(filepath.Separator), "tmp", exeName("codex")),
				CacheDir:     validPaths.CacheDir,
			},
			pkg:     validPackage,
			wantErr: "CodexExePath",
		},
		{
			name: "codex exe path with wrong executable name",
			paths: paths.Paths{
				CodexExePath: filepath.Join(string(filepath.Separator), "tmp", "bin", "not-codex"),
				CacheDir:     validPaths.CacheDir,
			},
			pkg:     validPackage,
			wantErr: "CodexExePath",
		},
		{
			name: "codex exe path derives root dest root",
			paths: paths.Paths{
				CodexExePath: filepath.Join(string(filepath.Separator), "bin", exeName("codex")),
				CacheDir:     validPaths.CacheDir,
			},
			pkg:     validPackage,
			wantErr: "CodexExePath",
		},
		{
			name: "relative cache dir",
			paths: paths.Paths{
				CodexExePath: validPaths.CodexExePath,
				CacheDir:     "cache",
			},
			pkg:     validPackage,
			wantErr: "CacheDir",
		},
		{
			name: "parent relative cache dir",
			paths: paths.Paths{
				CodexExePath: validPaths.CodexExePath,
				CacheDir:     filepath.Join("..", "cache"),
			},
			pkg:     validPackage,
			wantErr: "CacheDir",
		},
		{
			name:  "relative package dir",
			paths: validPaths,
			pkg: Package{
				PackageDir: filepath.Join("packaging", "linux"),
			},
			wantErr: "PackageDir",
		},
		{
			name:  "parent relative package dir",
			paths: validPaths,
			pkg: Package{
				PackageDir: filepath.Join("..", "packaging", "linux"),
			},
			wantErr: "PackageDir",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ensureCalled := false

			_, err := ResolveCodex(context.Background(), CodexResolveOptions{
				Paths:   tt.paths,
				Package: tt.pkg,
				LookPath: func(string) (string, error) {
					return "", exec.ErrNotFound
				},
				EnsureRuntime: func(context.Context, string, string, string) (string, error) {
					ensureCalled = true
					return "", nil
				},
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err=%v, want containing %q", err, tt.wantErr)
			}
			if ensureCalled {
				t.Fatal("EnsureRuntime called with invalid managed runtime paths")
			}
		})
	}
}

func TestCodexManifestNameSupportsOnlyLinuxCodexRuntimes(t *testing.T) {
	tests := []struct {
		goos     string
		goarch   string
		wantName string
		wantErr  string
	}{
		{
			goos:     "linux",
			goarch:   "amd64",
			wantName: "codex-manifest-linux-amd64.json",
		},
		{
			goos:     "linux",
			goarch:   "arm64",
			wantName: "codex-manifest-linux-arm64.json",
		},
		{
			goos:    "linux",
			goarch:  "riscv64",
			wantErr: "unsupported Codex runtime platform linux/riscv64",
		},
		{
			goos:    "darwin",
			goarch:  "amd64",
			wantErr: "unsupported Codex runtime platform darwin/amd64",
		},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			got, err := codexManifestName(tt.goos, tt.goarch)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err=%v, want containing %q", err, tt.wantErr)
				}
				if got != "" {
					t.Fatalf("name=%q, want empty", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.wantName {
				t.Fatalf("name=%q, want %q", got, tt.wantName)
			}
		})
	}
}

func TestLinuxCodexManifestPath(t *testing.T) {
	pkg := PackagePaths(filepath.Join(string(filepath.Separator), "opt", "agentserver", exeName("agentserver")))

	got := pkg.CodexManifestPath()

	var want string
	switch runtime.GOARCH {
	case "arm64":
		want = filepath.Join(string(filepath.Separator), "opt", "agentserver", "codex-manifest-linux-arm64.json")
	default:
		want = filepath.Join(string(filepath.Separator), "opt", "agentserver", "codex-manifest-linux-amd64.json")
	}
	if got != want {
		t.Fatalf("CodexManifestPath=%q, want %q", got, want)
	}
}

func TestLinuxCodexManifestsLoad(t *testing.T) {
	tests := []struct {
		name          string
		platform      string
		pinnedVersion string
		stripPrefix   string
	}{
		{
			name:          "codex-manifest-linux-amd64.json",
			platform:      "linux-x64",
			pinnedVersion: "0.139.0-linux-x64",
			stripPrefix:   "vendor/x86_64-unknown-linux-musl/",
		},
		{
			name:          "codex-manifest-linux-arm64.json",
			platform:      "linux-arm64",
			pinnedVersion: "0.139.0-linux-arm64",
			stripPrefix:   "vendor/aarch64-unknown-linux-musl/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := codexruntime.LoadManifest(filepath.Join("..", "..", "packaging", "linux", tt.name))
			if err != nil {
				t.Fatal(err)
			}
			if m.Package != "@openai/codex" {
				t.Fatalf("Package=%q", m.Package)
			}
			if m.Platform != tt.platform {
				t.Fatalf("Platform=%q, want %q", m.Platform, tt.platform)
			}
			if m.PinnedVersion != tt.pinnedVersion {
				t.Fatalf("PinnedVersion=%q, want %q", m.PinnedVersion, tt.pinnedVersion)
			}
			if m.StripPrefix != tt.stripPrefix {
				t.Fatalf("StripPrefix=%q, want %q", m.StripPrefix, tt.stripPrefix)
			}
		})
	}
}

func TestResolveCodexReturnsManagedError(t *testing.T) {
	wantErr := errors.New("install failed")

	_, err := ResolveCodex(context.Background(), CodexResolveOptions{
		Paths: paths.Paths{
			CodexExePath: filepath.Join(t.TempDir(), "bin-root", "bin", exeName("codex")),
			CacheDir:     filepath.Join(t.TempDir(), "cache"),
		},
		Package: PackagePaths(filepath.Join(string(filepath.Separator), "opt", "agentserver", exeName("agentserver"))),
		LookPath: func(string) (string, error) {
			return "", exec.ErrNotFound
		},
		EnsureRuntime: func(context.Context, string, string, string) (string, error) {
			return "", wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want %v", err, wantErr)
	}
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
