import * as assert from 'assert';
import * as vscode from 'vscode';

suite('respawn', () => {
  test('closing codex terminal respawns one', async () => {
    await vscode.extensions.getExtension('agentserver.agentserver-app')?.activate();
    await new Promise(r => setTimeout(r, 500));
    const t = vscode.window.terminals.find(t => t.name === 'codex');
    assert.ok(t, 'no codex terminal to close');
    t!.dispose();
    await new Promise(r => setTimeout(r, 800));
    const names = vscode.window.terminals.map(t => t.name);
    assert.ok(names.includes('codex'), `expected respawn, got ${JSON.stringify(names)}`);
  });
});
