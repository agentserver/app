package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInstallCodexPassesExplicitPaths(t *testing.T) {
	dir := t.TempDir()
	var gotManifest, gotDestRoot, gotCacheDir string
	orig := installCodexRuntime
	installCodexRuntime = func(ctx context.Context, manifestPath, destRoot, cacheDir string) error {
		gotManifest, gotDestRoot, gotCacheDir = manifestPath, destRoot, cacheDir
		return nil
	}
	defer func() { installCodexRuntime = orig }()

	err := runInstallCodex([]string{
		"--manifest", filepath.Join(dir, "codex-manifest.json"),
		"--dest-root", filepath.Join(dir, "agentserver-app"),
		"--cache-dir", filepath.Join(dir, "cache"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotManifest == "" || gotDestRoot == "" || gotCacheDir == "" {
		t.Fatalf("missing args manifest=%q dest=%q cache=%q", gotManifest, gotDestRoot, gotCacheDir)
	}
}

func TestRunInstallCodexRejectsMissingManifest(t *testing.T) {
	err := runInstallCodex([]string{"--dest-root", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("err=%v, want manifest requirement", err)
	}
}
