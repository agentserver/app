package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWindowsE2EWorkflowIsManualOnly(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "e2e-windows.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	on := mappingValue(documentMapping(t, &root), "on")
	if on == nil {
		t.Fatal("Windows E2E workflow must define triggers")
	}
	if mappingValue(on, "workflow_dispatch") == nil {
		t.Fatal("Windows E2E workflow must support manual workflow_dispatch runs")
	}
	if mappingValue(on, "push") != nil {
		t.Fatal("Windows E2E must not run on push/tag events; it needs a reachable private Windows SSH host")
	}
}

func TestReleaseWorkflowBuildsFullWindowsPackageFromFreshCheckout(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{
		"actions/setup-node@v4",
		"node-version: '20'",
		"choco install innosetup",
		"make package",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("release.yml should build the full Windows package from a fresh checkout; missing %q", want)
		}
	}
	if strings.Contains(source, "./scripts/package-windows.sh") {
		t.Fatal("release.yml must not call scripts/package-windows.sh directly; it requires prebuilt UI, VSIX, and Windows binaries")
	}
}

func documentMapping(t *testing.T, root *yaml.Node) *yaml.Node {
	t.Helper()
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		t.Fatalf("unexpected workflow YAML root kind=%v len=%d", root.Kind, len(root.Content))
	}
	return root.Content[0]
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
