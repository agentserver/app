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
	if m.PinnedVersion != "0.142.5-win32-x64" {
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
	if m.Pinned.Integrity != "sha512-a+wI4PEx9a2fg6V5ueTTDkOkr1XpEvA5RFXIbo/L2hOfzMmGtyRnbG24bCGu5Q2RSgVxSQV0aLkdb3vdYMNH9A==" {
		t.Fatalf("pinned integrity=%q", m.Pinned.Integrity)
	}
	if m.Pinned.Shasum != "a49a474ac281bf72128d490c1dd8dac1886479b4" {
		t.Fatalf("pinned shasum=%q", m.Pinned.Shasum)
	}
	if len(m.Pinned.URLs) != 3 {
		t.Fatalf("pinned URLs=%#v", m.Pinned.URLs)
	}
	for _, u := range m.Pinned.URLs {
		if !strings.Contains(u, "codex-0.142.5-win32-x64.tgz") {
			t.Fatalf("pinned URL should target 0.142.5 runtime, got %q", u)
		}
	}
	if !strings.Contains(m.Pinned.URLs[0], "registry.npmjs.org") {
		t.Fatalf("first mirror should be official npm, got %q", m.Pinned.URLs[0])
	}
	if !strings.Contains(m.Pinned.URLs[1], "registry.npmmirror.com") {
		t.Fatalf("second mirror should be npmmirror, got %q", m.Pinned.URLs[1])
	}
	if !strings.Contains(m.Pinned.URLs[2], "npmreg.proxy.ustclug.org") {
		t.Fatalf("third mirror should be USTC, got %q", m.Pinned.URLs[2])
	}
	if len(m.FallbackPinned) != 0 {
		t.Fatalf("fallback_pinned=%#v", m.FallbackPinned)
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
