// Package codexdesktop's catalog.go writes a model_catalog.json that Codex
// Desktop's built-in picker can consume. Codex Desktop's picker hard-filters
// out anything that is not in its bundled OpenAI catalog (openai/codex
// #15138, #19694, #22160 — all open since 2026/02). To get GLM/DeepSeek to
// show up by name (rather than as "自定义") we feed Codex a JSON catalog with
// rows shaped exactly like its bundled ones — each row built by cloning the
// bundled gpt-5.5 row and replacing only `slug` and `display_name`. That
// keeps every required field present and version-correct.
//
// model_catalog_json is a *full replacement* of Codex's bundled catalog
// (openai/codex #29156), so we always include a gpt-5.5 entry too, plus
// every entry from the protoconv routing catalog.
package codexdesktop

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/protoconv"
)

// modelTemplate is the bundled gpt-5.5 row extracted from
// openai/codex@main:codex-rs/models-manager/models.json. We clone this per
// model and rewrite slug + display_name. Refresh when Codex adds required
// schema fields (otherwise the catalog file fails to load and Codex picker
// silently falls back to defaults).
//
//go:embed model_template.json
var modelTemplate []byte

// WriteModelCatalog renders a catalog.json containing every route in the
// protoconv catalog. Each entry is a deep copy of the bundled gpt-5.5
// template, with only `slug` and `display_name` overridden. Returns the
// number of bytes written and any error from disk I/O or JSON encoding.
func WriteModelCatalog(path string) error {
	if path == "" {
		return fmt.Errorf("codexdesktop: catalog path required")
	}
	var template map[string]any
	if err := json.Unmarshal(modelTemplate, &template); err != nil {
		return fmt.Errorf("codexdesktop: embedded template: %w", err)
	}

	routes := protoconv.Catalog()
	models := make([]map[string]any, 0, len(routes))
	for _, r := range routes {
		entry := cloneModelEntry(template)
		entry["slug"] = r.Model
		if r.DisplayName != "" {
			entry["display_name"] = r.DisplayName
		} else {
			entry["display_name"] = r.Model
		}
		models = append(models, entry)
	}

	out := map[string]any{"models": models}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("codexdesktop: marshal catalog: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("codexdesktop: mkdir catalog dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("codexdesktop: temp catalog: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("codexdesktop: write catalog: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("codexdesktop: close catalog: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("codexdesktop: rename catalog: %w", err)
	}
	tmpPath = ""
	return nil
}

// cloneModelEntry deep-copies a model entry via JSON round-trip. Cheap (≤50 KB)
// and avoids ad-hoc map walking; the template is small enough that perf is a
// non-issue and structural fidelity matters more.
func cloneModelEntry(src map[string]any) map[string]any {
	b, _ := json.Marshal(src)
	var dst map[string]any
	_ = json.Unmarshal(b, &dst)
	return dst
}
