import * as assert from 'assert';
import { hasTerminalNamed, openCodexTerminalWithWindow } from '../../terminal';

suite('terminal helpers', () => {
  test('matches terminals by configured session name', () => {
    const terminals = [
      { name: 'PowerShell' },
      { name: 'codex' },
    ];

    assert.strictEqual(hasTerminalNamed(terminals, 'codex'), true);
    assert.strictEqual(hasTerminalNamed(terminals, 'other'), false);
  });

  test('creates background sessions without revealing the bottom panel', async () => {
    let showCount = 0;
    const win = {
      createTerminal: (_options: { name: string }) => ({
        name: 'codex',
        show: () => { showCount++; },
      }),
    };

    await openCodexTerminalWithWindow(win, 'codex', { reveal: false });

    assert.strictEqual(showCount, 0);
  });

  test('reveals explicit user-created sessions', async () => {
    const showArgs: boolean[] = [];
    const win = {
      createTerminal: (_options: { name: string }) => ({
        name: 'codex',
        show: (preserveFocus?: boolean) => { showArgs.push(Boolean(preserveFocus)); },
      }),
    };

    await openCodexTerminalWithWindow(win, 'codex', { reveal: true, preserveFocus: false });

    assert.deepStrictEqual(showArgs, [false]);
  });
});
