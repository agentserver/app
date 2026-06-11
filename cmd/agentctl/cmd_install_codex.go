package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/codexruntime"
	"github.com/agentserver/agentserver-pkg/internal/paths"
)

var installCodexRuntime = func(ctx context.Context, manifestPath, destRoot, cacheDir string) error {
	res, err := codexruntime.Ensure(ctx, codexruntime.Options{
		ManifestPath: manifestPath,
		DestRoot:     destRoot,
		CacheDir:     cacheDir,
	})
	if err != nil {
		return err
	}
	if res.Skipped {
		fmt.Printf("codex runtime already installed at %s\n", res.CodexExe)
		return nil
	}
	fmt.Printf("codex runtime %s installed at %s\n", res.Version, res.CodexExe)
	return nil
}

func runInstallCodex(args []string) error {
	fs := flag.NewFlagSet("install-codex", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	manifest := fs.String("manifest", "", "path to codex-manifest.json")
	destRoot := fs.String("dest-root", "", "destination root, usually %LOCALAPPDATA%\\agentserver-app")
	cacheDir := fs.String("cache-dir", "", "download cache directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := paths.Default()
	if err != nil {
		return err
	}
	if *destRoot == "" {
		*destRoot = p.LocalAppDataRoot
	}
	if *cacheDir == "" {
		*cacheDir = filepath.Join(p.LocalAppDataRoot, "cache", "codex")
	}
	if *manifest == "" {
		return fmt.Errorf("install-codex requires --manifest")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	return installCodexRuntime(ctx, *manifest, *destRoot, *cacheDir)
}
