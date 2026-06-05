package vscode

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestEnsureTerminalOnlyPanelStateUpdatesExistingPinnedPanels(t *testing.T) {
	userDataDir := t.TempDir()
	dbPath := filepath.Join(userDataDir, "User", "globalStorage", "state.vscdb")
	db := openPanelStateTestDB(t, dbPath)
	defer db.Close()

	input := []map[string]any{
		{"id": "workbench.panel.markers", "pinned": true, "visible": false, "order": float64(0)},
		{"id": "workbench.panel.output", "pinned": true, "visible": false, "order": float64(1)},
		{"id": "workbench.panel.repl", "pinned": true, "visible": false, "order": float64(2)},
		{"id": "terminal", "pinned": true, "visible": false, "order": float64(3)},
		{"id": "workbench.panel.testResults", "pinned": true, "visible": false, "order": float64(4)},
		{"id": "~remote.forwardedPortsContainer", "pinned": true, "visible": false, "order": float64(5)},
	}
	insertPanelStateTestValue(t, db, "workbench.panel.pinnedPanels", input)

	if err := EnsureTerminalOnlyPanelState(userDataDir); err != nil {
		t.Fatalf("EnsureTerminalOnlyPanelState: %v", err)
	}

	got := readPanelStateTestValue(t, db, "workbench.panel.pinnedPanels")
	for _, entry := range got {
		id, _ := entry["id"].(string)
		pinned, _ := entry["pinned"].(bool)
		if id == "terminal" {
			if !pinned {
				t.Fatalf("terminal should remain pinned: %#v", entry)
			}
			continue
		}
		if pinned {
			t.Fatalf("%s should be unpinned: %#v", id, entry)
		}
	}
}

func TestEnsureTerminalOnlyPanelStateCreatesMissingStateDB(t *testing.T) {
	userDataDir := t.TempDir()

	if err := EnsureTerminalOnlyPanelState(userDataDir); err != nil {
		t.Fatalf("EnsureTerminalOnlyPanelState: %v", err)
	}

	dbPath := filepath.Join(userDataDir, "User", "globalStorage", "state.vscdb")
	db := openPanelStateTestDB(t, dbPath)
	defer db.Close()

	got := readPanelStateTestValue(t, db, "workbench.panel.pinnedPanels")
	if len(got) == 0 {
		t.Fatal("expected seeded pinned panel state")
	}
	for _, entry := range got {
		id, _ := entry["id"].(string)
		pinned, _ := entry["pinned"].(bool)
		if id == "terminal" && !pinned {
			t.Fatalf("terminal should be pinned: %#v", entry)
		}
		if id != "terminal" && pinned {
			t.Fatalf("%s should not be pinned: %#v", id, entry)
		}
	}
}

func openPanelStateTestDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertPanelStateTestValue(t *testing.T, db *sql.DB, key string, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)`, key, string(b)); err != nil {
		t.Fatal(err)
	}
}

func readPanelStateTestValue(t *testing.T, db *sql.DB, key string) []map[string]any {
	t.Helper()
	var raw string
	if err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = ?`, key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal %s: %v\n%s", key, err, raw)
	}
	return out
}
