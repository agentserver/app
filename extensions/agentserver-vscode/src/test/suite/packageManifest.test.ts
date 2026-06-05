import * as assert from 'assert';
import * as fs from 'fs';
import * as path from 'path';

interface CommandContribution {
  command: string;
  title: string;
}

interface MenuContribution {
  command: string;
  when?: string;
  group?: string;
}

interface PackageManifest {
  contributes: {
    commands: CommandContribution[];
    menus?: Record<string, MenuContribution[]>;
  };
}

function readManifest(): PackageManifest {
  const manifestPath = path.resolve(__dirname, '../../../package.json');
  return JSON.parse(fs.readFileSync(manifestPath, 'utf8')) as PackageManifest;
}

suite('package manifest', () => {
  test('uses user-facing command titles', () => {
    const manifest = readManifest();
    const byCommand = new Map(manifest.contributes.commands.map(c => [c.command, c.title]));
    assert.strictEqual(
      byCommand.get('agentserverVscode.reopenCodexTerminal'),
      '星池指挥官: 创建新的会话',
    );
    assert.strictEqual(
      byCommand.get('agentserverVscode.doctor'),
      '星池指挥官: 诊断工具',
    );
    for (const title of byCommand.values()) {
      assert.ok(!title.includes('终端'), `command title should not mention 终端: ${title}`);
      assert.ok(!title.toLowerCase().includes('terminal'), `command title should not mention terminal: ${title}`);
    }
  });
});
