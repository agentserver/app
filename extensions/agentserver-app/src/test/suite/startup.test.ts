import * as assert from 'assert';
import * as vscode from 'vscode';

suite('startup', () => {
  test('codex terminal exists after activation', async () => {
    await vscode.extensions.getExtension('agentserver.agentserver-app')?.activate();
    // Give the activate handler time to spawn the terminal.
    await new Promise(r => setTimeout(r, 1000));
    const names = vscode.window.terminals.map(t => t.name);
    assert.ok(names.includes('codex'), `expected 'codex' terminal, got ${JSON.stringify(names)}`);
  });
});
