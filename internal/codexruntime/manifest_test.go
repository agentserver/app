package codexruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifestParsesPinnedMirrorsAndRuntimeFiles(t *testing.T) {
	m, err := LoadManifest(filepath.Join("..", "..", "packaging", "windows", "codex-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Package != "@openai/codex" {
		t.Fatalf("Package=%q", m.Package)
	}
	if m.Platform != "win32-x64" {
		t.Fatalf("Platform=%q", m.Platform)
	}
	if m.PinnedVersion != "0.136.0-win32-x64" {
		t.Fatalf("PinnedVersion=%q", m.PinnedVersion)
	}
	if m.StripPrefix != "vendor/x86_64-pc-windows-msvc/" {
		t.Fatalf("StripPrefix=%q", m.StripPrefix)
	}
	if m.CodexExe != "bin/codex.exe" {
		t.Fatalf("CodexExe=%q", m.CodexExe)
	}
	for _, want := range []string{
		"bin/codex.exe",
		"codex-path/rg.exe",
		"codex-resources/codex-command-runner.exe",
		"codex-resources/codex-windows-sandbox-setup.exe",
	} {
		if !contains(m.RequiredFiles, want) {
			t.Fatalf("required_files missing %q: %#v", want, m.RequiredFiles)
		}
	}
	if !strings.HasPrefix(m.Pinned.Integrity, "sha512-") {
		t.Fatalf("pinned integrity should be npm sha512, got %q", m.Pinned.Integrity)
	}
	if len(m.Pinned.URLs) != 2 {
		t.Fatalf("pinned URLs=%#v", m.Pinned.URLs)
	}
	if !strings.Contains(m.Pinned.URLs[0], "registry.npmmirror.com") {
		t.Fatalf("first mirror should be npmmirror, got %q", m.Pinned.URLs[0])
	}
	if !strings.Contains(m.Pinned.URLs[1], "npmreg.proxy.ustclug.org") {
		t.Fatalf("second mirror should be USTC, got %q", m.Pinned.URLs[1])
	}
	if len(m.FallbackPinned) != 1 {
		t.Fatalf("fallback_pinned=%#v", m.FallbackPinned)
	}
	fallback := m.FallbackPinned[0]
	if fallback.Version != "0.139.0-win32-x64" {
		t.Fatalf("fallback version=%q", fallback.Version)
	}
	if !strings.HasPrefix(fallback.Integrity, "sha512-") {
		t.Fatalf("fallback integrity should be npm sha512, got %q", fallback.Integrity)
	}
	if len(fallback.URLs) != 2 {
		t.Fatalf("fallback URLs=%#v", fallback.URLs)
	}
	if !strings.Contains(fallback.URLs[0], "registry.npmmirror.com") {
		t.Fatalf("first fallback mirror should be npmmirror, got %q", fallback.URLs[0])
	}
	if !strings.Contains(fallback.URLs[1], "npmreg.proxy.ustclug.org") {
		t.Fatalf("second fallback mirror should be USTC, got %q", fallback.URLs[1])
	}
}

func TestLoadManifestRejectsMissingRequiredFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-manifest.json")
	if err := os.WriteFile(path, []byte(`{"package":"@openai/codex"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("expected missing field error")
	}
	for _, want := range []string{"platform", "pinned_version", "strip_prefix", "codex_exe", "required_files", "pinned.urls"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%v, want mention %q", err, want)
		}
	}
}

func TestLightInstallerDocsDoNotDescribeUnpinnedLatestFallback(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "docs", "superpowers", "specs", "2026-06-11-light-windows-installer-design.md"),
		filepath.Join("..", "..", "docs", "superpowers", "plans", "2026-06-12-light-windows-installer.md"),
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s := string(body)
		for _, forbidden := range []string{
			"latest_metadata_urls",
			"package_metadata_url_templates",
			"@openai/codex/latest",
			"ResolveLatest",
			"Latest Fallback",
			"pinned-or-latest",
			"falls back to latest",
			"fallback to latest",
			"Source = \"latest\"",
			"Source:   \"latest\"",
		} {
			if strings.Contains(s, forbidden) {
				t.Fatalf("%s still describes unpinned latest fallback via %q", path, forbidden)
			}
		}
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
