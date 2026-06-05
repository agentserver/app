import * as assert from 'assert';
import { hiddenViewCommandIds, hidePanelViews, normalizeHiddenViewId } from '../../panel';

suite('panel view hiding', () => {
  test('normalizes legacy and internal panel ids to actual VS Code view ids', () => {
    assert.strictEqual(normalizeHiddenViewId('workbench.panel.markers'), 'workbench.panel.markers.view');
    assert.strictEqual(normalizeHiddenViewId('workbench.panel.repl'), 'workbench.panel.repl.view');
    assert.strictEqual(normalizeHiddenViewId('workbench.debug.console'), 'workbench.panel.repl.view');
    assert.strictEqual(normalizeHiddenViewId('ports'), '~remote.forwardedPorts');
    assert.strictEqual(normalizeHiddenViewId('workbench.panel.output'), 'workbench.panel.output');
  });

  test('builds removeView commands for the configured hidden views', () => {
    assert.deepStrictEqual(
      hiddenViewCommandIds([
        'workbench.panel.markers',
        'workbench.panel.output',
        'workbench.debug.console',
        'ports',
      ]),
      [
        'workbench.panel.markers.view.removeView',
        'workbench.panel.output.removeView',
        'workbench.panel.repl.view.removeView',
        '~remote.forwardedPorts.removeView',
      ],
    );
  });

  test('hides configured views with VS Code removeView commands', async () => {
    const calls: Array<{ command: string; args: unknown[] }> = [];

    await hidePanelViews([
      'workbench.panel.markers',
      'workbench.panel.output',
      'workbench.debug.console',
      'ports',
    ], async (command: string, ...args: unknown[]) => {
      calls.push({ command, args });
    });

    for (const command of [
      'workbench.panel.markers.view.removeView',
      'workbench.panel.output.removeView',
      'workbench.panel.repl.view.removeView',
      '~remote.forwardedPorts.removeView',
    ]) {
      assert.ok(calls.some(c => c.command === command), `missing command ${command}`);
    }
    assert.ok(
      calls.some(c => c.command === 'setContext' && c.args[0] === 'workbench.panel.output.visible' && c.args[1] === false),
      'keeps the old context fallback for VS Code versions that honor it',
    );
  });
});
