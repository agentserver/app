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
  description: string;
  contributes: {
    commands: CommandContribution[];
    configuration: {
      properties: Record<string, {
        description: string;
      }>;
    };
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

  test('contributes open-with-system file context command', () => {
    const manifest = readManifest();
    const byCommand = new Map(manifest.contributes.commands.map(c => [c.command, c.title]));
    assert.strictEqual(
      byCommand.get('agentserverVscode.openWithSystem'),
      '用系统应用打开',
    );
    const menus = manifest.contributes.menus;
    const explorerMenu = menus && menus['explorer/context'] ? menus['explorer/context'] : [];
    const entry = explorerMenu.find(m => m.command === 'agentserverVscode.openWithSystem');
    assert.ok(entry, 'missing explorer/context menu entry for open-with-system');
    assert.strictEqual(entry.when, 'resourceScheme == file');
  });

  test('contributes hidden advanced interface command', () => {
    const manifest = readManifest();
    const byCommand = new Map(manifest.contributes.commands.map(c => [c.command, c.title]));
    assert.strictEqual(
      byCommand.get('agentserverVscode.showAdvancedInterface'),
      '星池指挥官: 显示高级界面',
    );

    const menuEntries = Object.values(manifest.contributes.menus ?? {}).flat();
    assert.ok(
      !menuEntries.some(m => m.command === 'agentserverVscode.showAdvancedInterface'),
      'advanced interface command should stay hidden from menus',
    );
  });

  test('uses simple Chinese descriptions for visible settings', () => {
    const manifest = readManifest();
    const properties = manifest.contributes.configuration.properties;

    assert.strictEqual(
      manifest.description,
      '让 VS Code 作为星池指挥官的简洁文件夹和会话界面',
    );
    assert.strictEqual(
      properties['agentserverVscode.startup.openFolderIfEmpty'].description,
      '启动时如果还没有打开文件夹，就提示用户选择一个文件夹。',
    );
    assert.strictEqual(
      properties['agentserverVscode.terminal.respawnOnClose'].description,
      '关闭会话后自动创建新的会话。',
    );
    assert.strictEqual(
      properties['agentserverVscode.terminal.profileName'].description,
      '用于后台会话的内部配置名称。',
    );
    assert.strictEqual(
      properties['agentserverVscode.panel.hideViews'].description,
      '高级设置：需要隐藏的 VS Code 内部视图。',
    );
  });
});
