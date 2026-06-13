package headless

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

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
