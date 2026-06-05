package vscode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	_ "modernc.org/sqlite"
)

const (
	panelPinnedPanelsKey   = "workbench.panel.pinnedPanels"
	targetStorageMarkerKey = "__$__targetStorageMarker"
	newStorageMarkerKey    = "__$__isNewStorageMarker"
)

var terminalOnlyPanelOrder = []string{
	"workbench.panel.markers",
	"workbench.panel.output",
	"workbench.panel.repl",
	"terminal",
	"workbench.panel.testResults",
	"~remote.forwardedPortsContainer",
	"refactorPreview",
}

// EnsureTerminalOnlyPanelState pre-seeds VS Code's workbench storage so the
// bottom panel starts with only Terminal pinned.
func EnsureTerminalOnlyPanelState(userDataDir string) error {
	if userDataDir == "" {
		return fmt.Errorf("EnsureTerminalOnlyPanelState: userDataDir required")
	}
	dbPath := filepath.Join(userDataDir, "User", "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`); err != nil {
		return err
	}

	panels, err := readPinnedPanelEntries(db)
	if err != nil {
		return err
	}
	panels = terminalOnlyPinnedPanelEntries(panels)
	if err := writeJSONStorageValue(db, panelPinnedPanelsKey, panels); err != nil {
		return err
	}

	if err := ensureTargetStorageMarker(db); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)`, newStorageMarkerKey, "true")
	return err
}

func readPinnedPanelEntries(db *sql.DB) ([]map[string]any, error) {
	var raw string
	err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = ?`, panelPinnedPanelsKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var panels []map[string]any
	if err := json.Unmarshal([]byte(raw), &panels); err != nil {
		return nil, nil
	}
	return panels, nil
}

func terminalOnlyPinnedPanelEntries(existing []map[string]any) []map[string]any {
	order := panelOrder()
	seen := make(map[string]bool, len(existing)+len(terminalOnlyPanelOrder))
	out := make([]map[string]any, 0, len(existing)+len(terminalOnlyPanelOrder))

	for _, entry := range existing {
		id, ok := entry["id"].(string)
		if !ok || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		next := copyPanelEntry(entry)
		next["pinned"] = id == "terminal"
		if id != "terminal" {
			next["visible"] = false
		}
		if _, ok := next["order"]; !ok {
			next["order"] = order[id]
		}
		out = append(out, next)
	}

	for _, id := range terminalOnlyPanelOrder {
		if seen[id] {
			continue
		}
		out = append(out, map[string]any{
			"id":      id,
			"pinned":  id == "terminal",
			"visible": false,
			"order":   order[id],
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return panelEntryOrder(out[i], order) < panelEntryOrder(out[j], order)
	})
	return out
}

func panelOrder() map[string]int {
	out := make(map[string]int, len(terminalOnlyPanelOrder))
	for i, id := range terminalOnlyPanelOrder {
		out[id] = i
	}
	return out
}

func copyPanelEntry(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		out[k] = v
	}
	return out
}

func panelEntryOrder(entry map[string]any, order map[string]int) int {
	if id, ok := entry["id"].(string); ok {
		if n, ok := order[id]; ok {
			return n
		}
	}
	return len(order) + 1
}

func ensureTargetStorageMarker(db *sql.DB) error {
	var raw string
	err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = ?`, targetStorageMarkerKey).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	marker := map[string]any{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &marker)
	}
	marker[panelPinnedPanelsKey] = 0
	return writeJSONStorageValue(db, targetStorageMarkerKey, marker)
}

func writeJSONStorageValue(db *sql.DB, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)`, key, string(b))
	return err
}
