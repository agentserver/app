import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { runTests } from '@vscode/test-electron';
import { cleanupTestWorkspace } from '../testWorkspace';

async function main() {
  let testWorkspace = '';
  try {
    const extensionDevelopmentPath = path.resolve(__dirname, '..', '..');
    const extensionTestsPath = path.resolve(__dirname, 'suite', 'index');
    testWorkspace = fs.mkdtempSync(path.join(os.tmpdir(), 'agentserver-app-test-'));
    await runTests({
      extensionDevelopmentPath,
      extensionTestsPath,
      launchArgs: [testWorkspace, '--disable-workspace-trust'],
    });
  } catch {
    console.error('Failed to run tests');
    process.exitCode = 1;
  } finally {
    cleanupTestWorkspace(testWorkspace);
  }
}
main();
