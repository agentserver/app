import * as path from 'path';
import Mocha from 'mocha';
import { glob } from 'glob';

export function run(): Promise<void> {
  const mocha = new Mocha({ ui: 'bdd', color: true, timeout: 20_000 });
  const testsRoot = path.resolve(__dirname);
  return new Promise(async (resolve, reject) => {
    const files = await glob('**/*.test.js', { cwd: testsRoot });
    files.forEach(f => mocha.addFile(path.resolve(testsRoot, f)));
    try {
      mocha.run(failures => failures ? reject(new Error(`${failures} test(s) failed`)) : resolve());
    } catch (e) { reject(e); }
  });
}
