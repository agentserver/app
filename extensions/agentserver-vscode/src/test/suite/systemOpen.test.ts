import * as assert from 'assert';
import { buildSystemOpenCommand, openPathWithSystemApplication } from '../../systemOpen';

suite('system open helpers', () => {
  test('uses PowerShell Start-Process for Windows local files', () => {
    const spec = buildSystemOpenCommand('C:\\Users\\61414\\Desktop\\hello world.txt', 'win32');

    assert.strictEqual(spec.command, 'powershell.exe');
    assert.deepStrictEqual(spec.args, [
      '-NoProfile',
      '-ExecutionPolicy',
      'Bypass',
      '-Command',
      'Start-Process -LiteralPath $args[0]',
      'C:\\Users\\61414\\Desktop\\hello world.txt',
    ]);
    assert.strictEqual(spec.options.windowsHide, true);
  });

  test('uses platform openers without shell quoting', () => {
    assert.deepStrictEqual(
      buildSystemOpenCommand('/tmp/hello world.txt', 'darwin'),
      { command: 'open', args: ['/tmp/hello world.txt'], options: {} },
    );
    assert.deepStrictEqual(
      buildSystemOpenCommand('/tmp/hello world.txt', 'linux'),
      { command: 'xdg-open', args: ['/tmp/hello world.txt'], options: {} },
    );
  });

  test('rejects when the system opener fails', async () => {
    const error = new Error('no association');
    await assert.rejects(
      () => openPathWithSystemApplication(
        '/tmp/nope.txt',
        'linux',
        (
          _command: string,
          _args: string[],
          _options: { windowsHide?: boolean },
          done: (error: Error | null) => void,
        ) => done(error),
      ),
      /no association/,
    );
  });
});
