# VS Code Minimal Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the shipped VS Code experience feel like a simple folder + file + session interface for non-technical users.

**Architecture:** Keep VS Code as the runtime shell and continue using the existing isolated `--user-data-dir` and bundled extension. Tighten generated VS Code settings, rename product-facing commands, add a simple "open with system app" command, keep codex terminal-backed sessions available while avoiding terminal-first focus, and update shortcut-facing language.

**Tech Stack:** Go, TypeScript, VS Code Extension API, VS Code settings JSON, Windows registry shortcut integration.

---

## File Structure

- `internal/vscode/settings.go`: source of truth for VS Code user-data-dir settings. Add minimal UI defaults here.
- `internal/vscode/settings_test.go`: unit coverage for generated settings and merge behavior.
- `internal/branding/branding.go`: product-facing Chinese labels used by shortcut creation.
- `internal/branding/branding_test.go`: new unit coverage for plain product labels.
- `test/e2e/windows/e2e_test.go`: Windows E2E assertions for shortcut/context-menu labels.
- `extensions/agentserver-vscode/package.json`: command titles, context menus, and contributed configuration metadata.
- `extensions/agentserver-vscode/src/extension.ts`: extension activation wiring for commands and startup behavior.
- `extensions/agentserver-vscode/src/terminal.ts`: codex terminal creation and focus behavior.
- `extensions/agentserver-vscode/src/panel.ts`: user-facing wording in comments only.
- `extensions/agentserver-vscode/src/systemOpen.ts`: new focused command for opening a selected file with the OS default app.
- `extensions/agentserver-vscode/src/advanced.ts`: new hidden escape command for showing advanced VS Code UI again.
- `extensions/agentserver-vscode/src/test/suite/packageManifest.test.ts`: new manifest tests for command titles and menu contributions.
- `extensions/agentserver-vscode/README.md`: update shipped-extension responsibilities to use "session" wording.

## Task 1: Minimal VS Code Settings

**Files:**
- Modify: `internal/vscode/settings_test.go`
- Modify: `internal/vscode/settings.go`

- [ ] **Step 1: Add failing tests for minimal UI defaults**

Append this helper and tests to `internal/vscode/settings_test.go`:

```go
func readSettingsMap(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not valid json: %v", err)
	}
	return m
}

func TestWriteSettings_MinimalModeDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "User", "settings.json")
	if err := WriteSettings(path, SettingsInput{CodexAbsPath: `C:\bin\codex.exe`}); err != nil {
		t.Fatal(err)
	}
	m := readSettingsMap(t, path)
	want := map[string]any{
		"workbench.statusBar.visible":       false,
		"workbench.panel.opensMaximized":    "never",
		"window.menuBarVisibility":          "hidden",
		"window.commandCenter":              false,
		"workbench.layoutControl.enabled":   false,
		"breadcrumbs.enabled":               false,
		"editor.minimap.enabled":            false,
		"editor.stickyScroll.enabled":       false,
		"workbench.editor.showTabs":         "single",
		"workbench.editor.empty.hint":       "hidden",
		"workbench.tips.enabled":            false,
		"update.showReleaseNotes":           false,
		"extensions.ignoreRecommendations":  true,
	}
	for key, expected := range want {
		if got := m[key]; got != expected {
			t.Errorf("%s=%v, want %v", key, got, expected)
		}
	}
}

func TestWriteSettings_OverwritesManagedMinimalModeKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "User", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	prior := `{
	  "window.menuBarVisibility": "classic",
	  "window.commandCenter": true,
	  "workbench.statusBar.visible": true,
	  "workbench.panel.opensMaximized": "always",
	  "editor.minimap.enabled": true,
	  "custom.key": "keep me"
	}`
	if err := os.WriteFile(path, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSettings(path, SettingsInput{CodexAbsPath: `C:\bin\codex.exe`}); err != nil {
		t.Fatal(err)
	}
	m := readSettingsMap(t, path)
	checks := map[string]any{
		"window.menuBarVisibility":        "hidden",
		"window.commandCenter":            false,
		"workbench.statusBar.visible":     false,
		"workbench.panel.opensMaximized":  "never",
		"editor.minimap.enabled":          false,
		"custom.key":                      "keep me",
	}
	for key, expected := range checks {
		if got := m[key]; got != expected {
			t.Errorf("%s=%v, want %v", key, got, expected)
		}
	}
}
```

- [ ] **Step 2: Run settings tests and verify failure**

Run:

```bash
go test ./internal/vscode -run 'TestWriteSettings' -count=1
```

Expected: FAIL. Missing keys include `window.menuBarVisibility`, `window.commandCenter`, `workbench.layoutControl.enabled`, and `workbench.panel.opensMaximized` still reports `always`.

- [ ] **Step 3: Implement minimal settings defaults**

In `internal/vscode/settings.go`, update the `overrides := map[string]any{...}` block to include these values. Keep the existing codex terminal profile entries unchanged.

```go
overrides := map[string]any{
	"locale":                             "zh-cn",
	"telemetry.telemetryLevel":           "off",
	"workbench.editor.languageDetection": false,
	"workbench.startupEditor":            "none",
	"workbench.activityBar.location":     "hidden",
	"workbench.statusBar.visible":        false,
	"workbench.panel.defaultLocation":    "bottom",
	"workbench.panel.opensMaximized":     "never",
	"window.menuBarVisibility":           "hidden",
	"window.commandCenter":               false,
	"workbench.layoutControl.enabled":    false,
	"breadcrumbs.enabled":                false,
	"editor.minimap.enabled":             false,
	"editor.stickyScroll.enabled":        false,
	"workbench.editor.showTabs":          "single",
	"workbench.editor.empty.hint":        "hidden",
	"workbench.tips.enabled":             false,
	"update.showReleaseNotes":            false,
	"extensions.ignoreRecommendations":   true,

	"agentserverVscode.startup.openFolderIfEmpty": true,
	"agentserverVscode.terminal.respawnOnClose":   true,
	"agentserverVscode.terminal.profileName":      "codex",
	"agentserverVscode.panel.hideViews": []string{
		"workbench.panel.repl",
		"workbench.debug.console",
		"workbench.panel.comments",
		"ports",
		"workbench.panel.testResults",
	},

	"terminal.integrated.defaultProfile.windows": "codex",
	"terminal.integrated.profiles.windows": map[string]any{
		"codex": map[string]any{
			"path": `C:\Windows\System32\cmd.exe`,
			"args": []string{"/k", in.CodexAbsPath},
		},
	},
}
```

- [ ] **Step 4: Run settings tests and verify pass**

Run:

```bash
go test ./internal/vscode -run 'TestWriteSettings' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```bash
git add internal/vscode/settings.go internal/vscode/settings_test.go
git commit -m "feat(vscode): add minimal UI settings"
```

## Task 2: Product-Facing Shortcut Labels

**Files:**
- Modify: `internal/branding/branding.go`
- Create: `internal/branding/branding_test.go`
- Modify: `test/e2e/windows/e2e_test.go`

- [ ] **Step 1: Add failing branding tests**

Create `internal/branding/branding_test.go`:

```go
package branding

import "testing"

func TestProductFacingLabelsArePlainChinese(t *testing.T) {
	if DisplayName != "星池指挥官" {
		t.Fatalf("DisplayName=%q, want 星池指挥官", DisplayName)
	}
	if ContextMenuLabel != "用星池指挥官打开" {
		t.Fatalf("ContextMenuLabel=%q, want 用星池指挥官打开", ContextMenuLabel)
	}
}
```

- [ ] **Step 2: Run branding tests and verify failure**

Run:

```bash
go test ./internal/branding -count=1
```

Expected: FAIL because `ContextMenuLabel` is currently `用 星池指挥官 打开`.

- [ ] **Step 3: Update branding constants**

Edit `internal/branding/branding.go` so the constants are:

```go
const (
	ProductID        = "agentserver-vscode"
	DisplayName      = "星池指挥官"
	ContextMenuLabel = "用星池指挥官打开"
)
```

- [ ] **Step 4: Update Windows E2E shortcut and menu-label assertions**

In `test/e2e/windows/e2e_test.go`, replace the desktop shortcut assertion in step 5 with:

```go
out, _, _ = c.Pwsh(`Test-Path "$env:USERPROFILE\Desktop\星池指挥官.lnk"`)
if strings.TrimSpace(out) != "True" {
	t.Errorf("desktop shortcut missing: %s", out)
}
out, _, _ = c.Pwsh(`Test-Path 'Registry::HKEY_CURRENT_USER\Software\Classes\Directory\shell\AgentserverVscode'`)
if strings.TrimSpace(out) != "True" {
	t.Errorf("registry key missing: %s", out)
}
out, _, _ = c.Pwsh(`(Get-Item 'Registry::HKEY_CURRENT_USER\Software\Classes\Directory\shell\AgentserverVscode').GetValue('')`)
if strings.TrimSpace(out) != "用星池指挥官打开" {
	t.Errorf("context menu label wrong: %s", out)
}
```

- [ ] **Step 5: Run branding and shortcut-adjacent tests**

Run:

```bash
go test ./internal/branding ./internal/shortcut -count=1
```

Expected: PASS on Linux. Windows-only shortcut tests remain skipped unless running on Windows.

- [ ] **Step 6: Commit Task 2**

```bash
git add internal/branding/branding.go internal/branding/branding_test.go test/e2e/windows/e2e_test.go
git commit -m "feat: use plain Chinese product labels"
```

## Task 3: Rename Extension Commands to User Language

**Files:**
- Modify: `extensions/agentserver-vscode/package.json`
- Modify: `extensions/agentserver-vscode/src/panel.ts`
- Modify: `extensions/agentserver-vscode/README.md`
- Create: `extensions/agentserver-vscode/src/test/suite/packageManifest.test.ts`

- [ ] **Step 1: Add failing manifest tests for command titles**

Create `extensions/agentserver-vscode/src/test/suite/packageManifest.test.ts`:

```typescript
import * as assert from 'assert';
import * as fs from 'fs';
import * as path from 'path';

interface CommandContribution {
  command: string;
  title: string;
}

interface MenuContribution {
  command: string;
  when?: string;
  group?: string;
}

interface PackageManifest {
  contributes: {
    commands: CommandContribution[];
    menus?: Record<string, MenuContribution[]>;
  };
}

function readManifest(): PackageManifest {
  const manifestPath = path.resolve(__dirname, '../../../package.json');
  return JSON.parse(fs.readFileSync(manifestPath, 'utf8')) as PackageManifest;
}

suite('package manifest', () => {
  test('uses user-facing command titles', () => {
    const manifest = readManifest();
    const byCommand = new Map(manifest.contributes.commands.map(c => [c.command, c.title]));
    assert.strictEqual(
      byCommand.get('agentserverVscode.reopenCodexTerminal'),
      '星池指挥官: 创建新的会话',
    );
    assert.strictEqual(
      byCommand.get('agentserverVscode.doctor'),
      '星池指挥官: 诊断工具',
    );
    for (const title of byCommand.values()) {
      assert.ok(!title.includes('终端'), `command title should not mention 终端: ${title}`);
      assert.ok(!title.toLowerCase().includes('terminal'), `command title should not mention terminal: ${title}`);
    }
  });
});
```

- [ ] **Step 2: Compile and verify manifest test would fail**

Run:

```bash
cd extensions/agentserver-vscode && npm run compile
```

Expected: PASS compile. The new test would fail under `npm test` because the manifest still says `星池指挥官: 重开 codex 终端`.

- [ ] **Step 3: Update command titles in package.json**

In `extensions/agentserver-vscode/package.json`, update the `contributes.commands` block to:

```json
"commands": [
  {
    "command": "agentserverVscode.doctor",
    "title": "星池指挥官: 诊断工具"
  },
  {
    "command": "agentserverVscode.reopenCodexTerminal",
    "title": "星池指挥官: 创建新的会话"
  }
]
```

- [ ] **Step 4: Update wording in panel.ts comment**

In `extensions/agentserver-vscode/src/panel.ts`, replace:

```typescript
// The user can also manually run "agentserver-vscode: 重开 codex 终端".
```

with:

```typescript
// The user can also manually run "星池指挥官: 创建新的会话".
```

- [ ] **Step 5: Update README responsibilities**

In `extensions/agentserver-vscode/README.md`, replace:

```markdown
- Ensure a `codex` terminal exists; reopen it if closed
- Keep focus on Terminal / Output (away from other panel views)
```

with:

```markdown
- Ensure a codex-backed session exists; recreate it if closed
- Keep technical panels out of the primary workflow
```

- [ ] **Step 6: Run extension compile and manifest tests**

Run:

```bash
cd extensions/agentserver-vscode && npm run compile && npm test
```

Expected: PASS. If `npm test` cannot launch VS Code in the current environment, keep the compile result and record the test-launch error in the task notes before continuing.

- [ ] **Step 7: Commit Task 3**

```bash
git add extensions/agentserver-vscode/package.json extensions/agentserver-vscode/src/panel.ts extensions/agentserver-vscode/README.md extensions/agentserver-vscode/src/test/suite/packageManifest.test.ts
git commit -m "feat(ext): rename session commands"
```

## Task 4: Add "Open With System App" File Command

**Files:**
- Modify: `extensions/agentserver-vscode/package.json`
- Modify: `extensions/agentserver-vscode/src/extension.ts`
- Create: `extensions/agentserver-vscode/src/systemOpen.ts`
- Modify: `extensions/agentserver-vscode/src/test/suite/packageManifest.test.ts`

- [ ] **Step 1: Extend manifest tests for file context command**

Append this test inside the existing `suite('package manifest', ...)` block in `extensions/agentserver-vscode/src/test/suite/packageManifest.test.ts`:

```typescript
  test('contributes open-with-system file context command', () => {
    const manifest = readManifest();
    const byCommand = new Map(manifest.contributes.commands.map(c => [c.command, c.title]));
    assert.strictEqual(
      byCommand.get('agentserverVscode.openWithSystem'),
      '用系统应用打开',
    );
    const menus = manifest.contributes.menus;
    const explorerMenu = menus && menus['explorer/context'] ? menus['explorer/context'] : [];
    const entry = explorerMenu.find(m => m.command === 'agentserverVscode.openWithSystem');
    assert.ok(entry, 'missing explorer/context menu entry for open-with-system');
    assert.strictEqual(entry.when, 'resourceScheme == file');
  });
```

- [ ] **Step 2: Run extension tests and verify failure**

Run:

```bash
cd extensions/agentserver-vscode && npm run compile && npm test
```

Expected: FAIL because `agentserverVscode.openWithSystem` is not contributed yet. If VS Code test launch is unavailable, run compile and verify the manifest test is logically failing by inspecting `package.json`.

- [ ] **Step 3: Add command and menu contribution**

In `extensions/agentserver-vscode/package.json`, add this command after `agentserverVscode.reopenCodexTerminal`:

```json
{
  "command": "agentserverVscode.openWithSystem",
  "title": "用系统应用打开"
}
```

Then add a top-level `menus` contribution under `contributes`:

```json
"menus": {
  "explorer/context": [
    {
      "command": "agentserverVscode.openWithSystem",
      "when": "resourceScheme == file",
      "group": "navigation@90"
    }
  ]
}
```

Keep JSON valid: `commands` and `menus` are siblings inside `contributes`.

- [ ] **Step 4: Create systemOpen.ts**

Create `extensions/agentserver-vscode/src/systemOpen.ts`:

```typescript
import * as vscode from 'vscode';

export function registerOpenWithSystem(ctx: vscode.ExtensionContext): void {
  ctx.subscriptions.push(
    vscode.commands.registerCommand('agentserverVscode.openWithSystem', async (uri?: vscode.Uri) => {
      const target = uri || vscode.window.activeTextEditor?.document.uri;
      if (!target || target.scheme !== 'file') {
        await vscode.window.showErrorMessage('请选择一个本地文件后再打开。');
        return;
      }
      const ok = await vscode.env.openExternal(target);
      if (!ok) {
        await vscode.window.showErrorMessage(`无法用系统应用打开：${target.fsPath}`);
      }
    }),
  );
}
```

- [ ] **Step 5: Register command in extension activation**

In `extensions/agentserver-vscode/src/extension.ts`, add the import:

```typescript
import { registerOpenWithSystem } from './systemOpen';
```

Then call it after panel lockdown:

```typescript
  // 3. File context commands
  registerOpenWithSystem(ctx);
```

Renumber the nearby comments so terminal creation becomes step 4, respawn step 5, and commands step 6.

- [ ] **Step 6: Run extension compile and tests**

Run:

```bash
cd extensions/agentserver-vscode && npm run compile && npm test
```

Expected: PASS. If `npm test` cannot launch VS Code, `npm run compile` must still pass and the manifest test content must match `package.json`.

- [ ] **Step 7: Commit Task 4**

```bash
git add extensions/agentserver-vscode/package.json extensions/agentserver-vscode/src/extension.ts extensions/agentserver-vscode/src/systemOpen.ts extensions/agentserver-vscode/src/test/suite/packageManifest.test.ts
git commit -m "feat(ext): add open with system command"
```

## Task 5: Keep Codex Session Available Without Terminal-First Focus

**Files:**
- Modify: `extensions/agentserver-vscode/package.json`
- Modify: `extensions/agentserver-vscode/src/extension.ts`
- Modify: `extensions/agentserver-vscode/src/terminal.ts`
- Create: `extensions/agentserver-vscode/src/advanced.ts`
- Modify: `extensions/agentserver-vscode/src/test/suite/packageManifest.test.ts`

- [ ] **Step 1: Extend manifest tests for advanced escape command**

Append this test inside `suite('package manifest', ...)`:

```typescript
  test('contributes hidden advanced interface command', () => {
    const manifest = readManifest();
    const byCommand = new Map(manifest.contributes.commands.map(c => [c.command, c.title]));
    assert.strictEqual(
      byCommand.get('agentserverVscode.showAdvancedInterface'),
      '星池指挥官: 显示高级界面',
    );
  });
```

- [ ] **Step 2: Run extension tests and verify failure**

Run:

```bash
cd extensions/agentserver-vscode && npm run compile && npm test
```

Expected: FAIL because `agentserverVscode.showAdvancedInterface` is not contributed yet. If VS Code test launch is unavailable, compile should still pass and the package manifest is visibly missing the command.

- [ ] **Step 3: Add advanced command contribution**

In `extensions/agentserver-vscode/package.json`, add this command to `contributes.commands`:

```json
{
  "command": "agentserverVscode.showAdvancedInterface",
  "title": "星池指挥官: 显示高级界面"
}
```

Do not add this command to `menus`; it should remain an escape hatch available through the command palette.

- [ ] **Step 4: Add advanced.ts**

Create `extensions/agentserver-vscode/src/advanced.ts`:

```typescript
import * as vscode from 'vscode';

export function registerAdvancedInterface(ctx: vscode.ExtensionContext): void {
  ctx.subscriptions.push(
    vscode.commands.registerCommand('agentserverVscode.showAdvancedInterface', async () => {
      const config = vscode.workspace.getConfiguration();
      await config.update('workbench.statusBar.visible', true, vscode.ConfigurationTarget.Global);
      await config.update('workbench.activityBar.location', 'default', vscode.ConfigurationTarget.Global);
      await config.update('window.menuBarVisibility', 'classic', vscode.ConfigurationTarget.Global);
      await config.update('workbench.layoutControl.enabled', true, vscode.ConfigurationTarget.Global);
      await vscode.commands.executeCommand('workbench.action.terminal.focus');
    }),
  );
}
```

- [ ] **Step 5: Update terminal focus behavior**

In `extensions/agentserver-vscode/src/terminal.ts`, replace the file with:

```typescript
import * as vscode from 'vscode';

let lastSpawn = 0;
const DEBOUNCE_MS = 200;

export async function openCodexTerminal(profileName: string, preserveFocus = true): Promise<void> {
  const term = vscode.window.createTerminal({ name: profileName });
  term.show(preserveFocus);
  lastSpawn = Date.now();
}

export function attachTerminalRespawn(
  ctx: vscode.ExtensionContext,
  profileName: string,
  enabled: () => boolean,
): void {
  ctx.subscriptions.push(
    vscode.window.onDidCloseTerminal(async (t) => {
      if (!enabled()) return;
      if (t.name !== profileName) return;
      if (Date.now() - lastSpawn < DEBOUNCE_MS) return; // avoid runaway
      // If the window itself is closing, do nothing.
      if (!vscode.window.state.focused) return;
      await openCodexTerminal(profileName, true);
    }),
  );
}
```

- [ ] **Step 6: Register advanced command and preserve focus on activation**

In `extensions/agentserver-vscode/src/extension.ts`, add:

```typescript
import { registerAdvancedInterface } from './advanced';
```

Call it near the other command registration:

```typescript
  registerAdvancedInterface(ctx);
```

Change terminal creation on activation from:

```typescript
await openCodexTerminal(cfg.terminalProfileName);
```

to:

```typescript
await openCodexTerminal(cfg.terminalProfileName, true);
```

Change the user command registration from:

```typescript
() => openCodexTerminal(readConfig().terminalProfileName)
```

to:

```typescript
() => openCodexTerminal(readConfig().terminalProfileName, false)
```

- [ ] **Step 7: Run extension compile and tests**

Run:

```bash
cd extensions/agentserver-vscode && npm run compile && npm test
```

Expected: PASS. If `npm test` cannot launch VS Code, record the launch failure and keep `npm run compile` as required evidence.

- [ ] **Step 8: Commit Task 5**

```bash
git add extensions/agentserver-vscode/package.json extensions/agentserver-vscode/src/extension.ts extensions/agentserver-vscode/src/terminal.ts extensions/agentserver-vscode/src/advanced.ts extensions/agentserver-vscode/src/test/suite/packageManifest.test.ts
git commit -m "feat(ext): preserve focus for background sessions"
```

## Task 6: Full Verification

**Files:**
- No source changes expected.

- [ ] **Step 1: Verify Go unit and integration-safe packages**

Run:

```bash
make test
```

Expected: PASS. This target builds the Vue UI first, then runs `go test -race -count=1 ./...`.

- [ ] **Step 2: Verify onboarding UI tests**

Run:

```bash
make ui-test
```

Expected: PASS with all Vitest specs passing.

- [ ] **Step 3: Verify extension compile**

Run:

```bash
cd extensions/agentserver-vscode && npm run compile
```

Expected: PASS.

- [ ] **Step 4: Verify extension test suite when environment supports VS Code Electron**

Run:

```bash
cd extensions/agentserver-vscode && npm test
```

Expected: PASS. If the local environment cannot launch VS Code Electron, capture the exact error in the implementation summary and rely on `npm run compile` plus manifest tests reviewed in source.

- [ ] **Step 5: Inspect git status**

Run:

```bash
git status --short
```

Expected: clean working tree.

## Implementation Notes

- Do not rename internal executable names or package names in v1.5. `agentserver-vscode` remains the internal product ID, VSIX name, install directory, and keyring service name.
- Do not remove the codex terminal backend in v1.5. The visible language changes to "session"; a custom Webview chat is v2 scope.
- Do not use `npm audit fix --force` during this work. Existing npm audit findings are unrelated dependency drift.
- Keep all commits scoped by task. If a verification command generates `internal/ui/assets/dist/`, leave it untracked unless it is already intentionally tracked by the repo.
