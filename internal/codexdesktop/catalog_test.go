package codexdesktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/protoconv"
)

func TestWriteModelCatalog_ContainsEveryProtoConvRouteWithDisplayNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := WriteModelCatalog(path); err != nil {
		t.Fatalf("WriteModelCatalog: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal catalog: %v", err)
	}
	routes := protoconv.Catalog()
	if len(got.Models) != len(routes) {
		t.Fatalf("models count = %d, want %d", len(got.Models), len(routes))
	}
	gotBySlug := map[string]map[string]any{}
	for _, m := range got.Models {
		slug, _ := m["slug"].(string)
		gotBySlug[slug] = m
	}
	for _, r := range routes {
		m, ok := gotBySlug[r.Model]
		if !ok {
			t.Errorf("slug %q missing from catalog", r.Model)
			continue
		}
		if dn, _ := m["display_name"].(string); dn != r.DisplayName && dn != r.Model {
			t.Errorf("slug %q display = %q, want %q (or fallback %q)", r.Model, dn, r.DisplayName, r.Model)
		}
		// Each row must carry the full set of fields from the bundled gpt-5.5
		// template — otherwise Codex's deserialize will fail (some fields are
		// required, no default). Sample-check a few that have no default:
		for _, required := range []string{
			"shell_type", "visibility", "supported_in_api", "priority",
			"base_instructions", "supports_reasoning_summaries", "support_verbosity",
			"truncation_policy", "supports_parallel_tool_calls",
			"experimental_supported_tools",
		} {
			if _, ok := m[required]; !ok {
				t.Errorf("slug %q missing required field %q", r.Model, required)
			}
		}
	}
}

func TestWriteModelCatalog_AtomicRenameNoLeakedTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := WriteModelCatalog(path); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// expect exactly one file (catalog.json), no leftover .tmp
	if len(entries) != 1 || entries[0].Name() != "catalog.json" {
		t.Errorf("unexpected dir entries: %+v", entries)
	}
}
