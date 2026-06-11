package loom

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallDriverSupportInstallsSkillsAndPrompt(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	skillsArchive := filepath.Join(dir, "driver-skills.tar.gz")
	promptsArchive := filepath.Join(dir, "driver-codex-prompts.tar.gz")
	writeTarGz(t, skillsArchive, map[string]string{
		"skills/multiagent/SKILL.md":                   "---\nname: multiagent\n---\nUse driver tools.\n",
		"skills/multiagent/references/driver-tools.md": "Driver tools reference.\n",
	})
	writeTarGz(t, promptsArchive, map[string]string{
		"prompts-codex/AGENTS.md": "# Multi-Agent Driver\n\nUse `role == \"slave\"`.\n",
	})

	if err := InstallDriverSupport(DriverSupportInput{
		UserHome:                home,
		SkillsArchivePath:       skillsArchive,
		CodexPromptsArchivePath: promptsArchive,
	}); err != nil {
		t.Fatalf("InstallDriverSupport: %v", err)
	}

	for _, path := range []string{
		filepath.Join(home, ".agents", "skills", "multiagent", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "multiagent", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "multiagent", "references", "driver-tools.md"),
		filepath.Join(home, ".codex", "skills", "multiagent", "references", "driver-tools.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected installed file %s: %v", path, err)
		}
	}
	body := readFile(t, filepath.Join(home, ".codex", "AGENTS.md"))
	for _, want := range []string{
		loomPromptStartMarker,
		"# Multi-Agent Driver",
		"role == \"slave\"",
		loomPromptEndMarker,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("AGENTS.md missing %q:\n%s", want, body)
		}
	}
}

func TestInstallDriverSupportInstallsRootLevelSkillsArchive(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	skillsArchive := filepath.Join(dir, "driver-skills.tar.gz")
	writeTarGz(t, skillsArchive, map[string]string{
		"multiagent/SKILL.md":            "---\nname: multiagent\n---\nUse driver tools.\n",
		"mcp-acceptance/SKILL.md":        "---\nname: mcp-acceptance\n---\nVerify MCP tools.\n",
		"userspace-publish/SKILL.md":     "---\nname: userspace-publish\n---\nPublish userspace packages.\n",
		"scaffold-mcp-server/SKILL.md":   "---\nname: scaffold-mcp-server\n---\nScaffold MCP servers.\n",
		"mcp-acceptance/scripts/run.py":  "print('ok')\n",
		"multiagent/references/tools.md": "Driver tools reference.\n",
	})

	if err := InstallDriverSupport(DriverSupportInput{
		UserHome:          home,
		SkillsArchivePath: skillsArchive,
	}); err != nil {
		t.Fatalf("InstallDriverSupport: %v", err)
	}

	for _, path := range []string{
		filepath.Join(home, ".agents", "skills", "multiagent", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "multiagent", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "mcp-acceptance", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "mcp-acceptance", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "userspace-publish", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "userspace-publish", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "scaffold-mcp-server", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "scaffold-mcp-server", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "mcp-acceptance", "scripts", "run.py"),
		filepath.Join(home, ".codex", "skills", "multiagent", "references", "tools.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected installed file %s: %v", path, err)
		}
	}
}

func TestInstallDriverSupportInstallsSuperpowerSkillsArchive(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	loomSkillsArchive := filepath.Join(dir, "driver-skills.tar.gz")
	superpowerSkillsArchive := filepath.Join(dir, "driver-superpower-skills.tar.gz")
	writeTarGz(t, loomSkillsArchive, map[string]string{
		"multiagent/SKILL.md": "---\nname: multiagent\n---\nUse driver tools.\n",
	})
	writeTarGz(t, superpowerSkillsArchive, map[string]string{
		"using-superpowers/SKILL.md":                  "---\nname: using-superpowers\n---\nUse skills.\n",
		"test-driven-development/SKILL.md":            "---\nname: test-driven-development\n---\nWrite tests first.\n",
		"using-superpowers/references/codex-tools.md": "Codex tool mapping.\n",
		"systematic-debugging/root-cause-tracing.md":  "Trace root cause.\n",
	})

	if err := InstallDriverSupport(DriverSupportInput{
		UserHome:                    home,
		SkillsArchivePath:           loomSkillsArchive,
		SuperpowerSkillsArchivePath: superpowerSkillsArchive,
	}); err != nil {
		t.Fatalf("InstallDriverSupport: %v", err)
	}

	for _, path := range []string{
		filepath.Join(home, ".agents", "skills", "multiagent", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "multiagent", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "using-superpowers", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "using-superpowers", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "test-driven-development", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "test-driven-development", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "using-superpowers", "references", "codex-tools.md"),
		filepath.Join(home, ".codex", "skills", "systematic-debugging", "root-cause-tracing.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected installed file %s: %v", path, err)
		}
	}
}

func TestInstallDriverSupportReplacesManagedPromptBlock(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	agentsPath := filepath.Join(home, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(agentsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	prior := strings.Join([]string{
		"keep before",
		loomPromptStartMarker,
		"old managed prompt",
		loomPromptEndMarker,
		"keep after",
		"",
	}, "\n")
	if err := os.WriteFile(agentsPath, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	promptsArchive := filepath.Join(dir, "driver-codex-prompts.tar.gz")
	writeTarGz(t, promptsArchive, map[string]string{
		"prompts-codex/AGENTS.md": "new managed prompt\n",
	})

	if err := InstallDriverSupport(DriverSupportInput{
		UserHome:                home,
		CodexPromptsArchivePath: promptsArchive,
	}); err != nil {
		t.Fatalf("InstallDriverSupport: %v", err)
	}

	body := readFile(t, agentsPath)
	for _, want := range []string{"keep before", "new managed prompt", "keep after"} {
		if !strings.Contains(body, want) {
			t.Fatalf("AGENTS.md missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "old managed prompt") {
		t.Fatalf("old managed prompt was not replaced:\n%s", body)
	}
	if strings.Count(body, loomPromptStartMarker) != 1 || strings.Count(body, loomPromptEndMarker) != 1 {
		t.Fatalf("managed markers should appear exactly once:\n%s", body)
	}
}

func TestInstallDriverSupportRejectsEscapingArchivePath(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	skillsArchive := filepath.Join(dir, "driver-skills.tar.gz")
	writeTarGz(t, skillsArchive, map[string]string{
		"skills/../evil.txt": "bad\n",
	})

	err := InstallDriverSupport(DriverSupportInput{
		UserHome:          home,
		SkillsArchivePath: skillsArchive,
	})
	if err == nil {
		t.Fatal("expected archive path escape error")
	}
	if _, statErr := os.Stat(filepath.Join(home, ".agents", "evil.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("escaping file exists or stat failed unexpectedly: %v", statErr)
	}
}

func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()
	for name, content := range files {
		b := []byte(content)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
