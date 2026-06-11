import * as assert from 'assert';
import { cleanupTestWorkspace } from '../../testWorkspace';

suite('test workspace cleanup', () => {
  test('does not fail the test run when Windows still has the temp workspace locked', () => {
    const warnings: string[] = [];
    const error = Object.assign(new Error('resource busy or locked'), { code: 'EBUSY' });

    assert.doesNotThrow(() => cleanupTestWorkspace(
      'C:\\Users\\RUNNER~1\\AppData\\Local\\Temp\\agentserver-app-test-locked',
      () => { throw error; },
      message => warnings.push(message),
    ));

    assert.strictEqual(warnings.length, 1);
    assert.match(warnings[0], /Could not remove temporary test workspace/);
  });
});
