import * as assert from 'assert';
import { hideMinimalChrome, minimalChromeCommands, minimalViewContextIds } from '../../chrome';

suite('minimal chrome helpers', () => {
  test('does not close the bottom panel on startup', async () => {
    const calls: Array<{ command: string; args: unknown[] }> = [];

    await hideMinimalChrome(async (command: string, ...args: unknown[]) => {
      calls.push({ command, args });
    });

    assert.ok(
      !calls.some(c => c.command === 'workbench.action.closePanel'),
      'startup cleanup should keep the terminal panel visible',
    );
    assert.ok(
      calls.some(c => c.command === 'workbench.action.closeAuxiliaryBar'),
      'expected startup cleanup to close the auxiliary bar',
    );
  });

  test('marks outline and timeline as hidden when VS Code honors view contexts', async () => {
    const calls: Array<{ command: string; args: unknown[] }> = [];

    await hideMinimalChrome(async (command: string, ...args: unknown[]) => {
      calls.push({ command, args });
    });

    for (const id of ['outline', 'timeline']) {
      assert.ok(minimalViewContextIds.includes(id), `${id} should be in minimal view contexts`);
      assert.ok(
        calls.some(c => c.command === 'setContext' && c.args[0] === `${id}.visible` && c.args[1] === false),
        `expected ${id}.visible=false context`,
      );
    }
  });

  test('documents startup cleanup command order', () => {
    assert.deepStrictEqual(minimalChromeCommands, [
      'workbench.action.closeAuxiliaryBar',
    ]);
  });
});
