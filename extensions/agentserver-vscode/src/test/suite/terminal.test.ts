import * as assert from 'assert';
import { hasTerminalNamed } from '../../terminal';

suite('terminal helpers', () => {
  test('matches terminals by configured session name', () => {
    const terminals = [
      { name: 'PowerShell' },
      { name: 'codex' },
    ];

    assert.strictEqual(hasTerminalNamed(terminals, 'codex'), true);
    assert.strictEqual(hasTerminalNamed(terminals, 'other'), false);
  });
});
