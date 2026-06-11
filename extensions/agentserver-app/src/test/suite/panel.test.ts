import * as assert from 'assert';
import {
  hiddenViewCommandIds,
  hidePanelViews,
  normalizeHiddenViewId,
  panelHideRetryDelaysMs,
  schedulePanelViewHiding,
} from '../../panel';

suite('panel view hiding', () => {
  test('normalizes legacy and internal panel ids to actual VS Code view ids', () => {
    assert.strictEqual(normalizeHiddenViewId('workbench.panel.markers'), 'workbench.panel.markers.view');
    assert.strictEqual(normalizeHiddenViewId('workbench.panel.repl'), 'workbench.panel.repl.view');
    assert.strictEqual(normalizeHiddenViewId('workbench.debug.console'), 'workbench.panel.repl.view');
    assert.strictEqual(normalizeHiddenViewId('ports'), '~remote.forwardedPorts');
    assert.strictEqual(normalizeHiddenViewId('workbench.panel.output'), 'workbench.panel.output');
  });

  test('builds removeView commands only for non-panel views', () => {
    assert.deepStrictEqual(
      hiddenViewCommandIds([
        'workbench.panel.markers',
        'workbench.panel.output',
        'workbench.debug.console',
        'ports',
        'outline',
        'timeline',
      ]),
      [
        'outline.removeView',
        'timeline.removeView',
      ],
    );
  });

  test('marks configured views hidden without revealing bottom panel views', async () => {
    const calls: Array<{ command: string; args: unknown[] }> = [];

    await hidePanelViews([
      'workbench.panel.markers',
      'workbench.panel.output',
      'workbench.debug.console',
      'ports',
    ], async (command: string, ...args: unknown[]) => {
      calls.push({ command, args });
    });

    assert.ok(!calls.some(c => c.command.endsWith('.focus')), 'bottom panel views should not be revealed');
    assert.ok(!calls.some(c => c.command.endsWith('.removeView')), 'bottom panel labels are hidden by pre-seeded VS Code state');
    assert.ok(!calls.some(c => c.command === 'show.toggleCompositePinned'), 'bottom panel pinning should not be toggled at runtime');
    assert.ok(
      calls.some(c => c.command === 'setContext' && c.args[0] === 'workbench.panel.output.visible' && c.args[1] === false),
      'keeps the old context fallback for VS Code versions that honor it',
    );
  });

  test('does not reveal explorer views before removing them', async () => {
    const calls: string[] = [];

    await hidePanelViews(['outline', 'timeline'], async (command: string) => {
      calls.push(command);
    });

    assert.ok(!calls.includes('outline.focus'), 'outline should not be revealed before hiding');
    assert.ok(!calls.includes('timeline.focus'), 'timeline should not be revealed before hiding');
    assert.ok(calls.includes('outline.removeView'));
    assert.ok(calls.includes('timeline.removeView'));
  });

  test('schedules delayed hide retries for views restored after startup', async () => {
    const calls: string[] = [];
    const scheduled: Array<{ delayMs: number; run: () => Promise<void> }> = [];
    const subscriptions: Array<{ dispose(): void }> = [];

    schedulePanelViewHiding(
      { subscriptions },
      ['workbench.panel.output'],
      (command: string) => {
        calls.push(command);
      },
      (run: () => Promise<void>, delayMs: number) => {
        scheduled.push({ delayMs, run });
        return { dispose(): void {} };
      },
    );

    assert.deepStrictEqual(scheduled.map(item => item.delayMs), panelHideRetryDelaysMs);
    assert.strictEqual(subscriptions.length, panelHideRetryDelaysMs.length);

    await scheduled[0].run();
    assert.ok(calls.includes('setContext'));
    assert.ok(!calls.includes('workbench.panel.output.focus'));
    assert.ok(!calls.includes('workbench.panel.output.removeView'));
    assert.ok(!calls.includes('show.toggleCompositePinned'));
  });
});
