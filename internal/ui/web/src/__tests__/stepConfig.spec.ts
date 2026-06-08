import { describe, expect, it } from 'vitest';
import { stepsForMode, completedMapForMode } from '../stepConfig';

describe('stepConfig', () => {
  it('uses Codex Desktop steps by default', () => {
    expect(stepsForMode(undefined).map(s => s.id)).toEqual([
      'modelserver_login',
      'agentserver_login',
      'codex_desktop_install',
      'codex_desktop_configure',
      'finalize',
    ]);
  });

  it('uses user-facing labels for Codex Desktop setup', () => {
    expect(stepsForMode(undefined).map(s => s.label)).toEqual([
      '连接大模型',
      '连接星池工作区',
      '安装 Codex Desktop 智能助手',
      '准备 Codex Desktop 智能助手',
      '完成',
    ]);
  });

  it('uses minimal VS Code steps when selected', () => {
    expect(stepsForMode('minimal_vscode').map(s => s.id)).toEqual([
      'modelserver_login',
      'agentserver_login',
      'vscode_install',
      'vscode_configure',
      'finalize',
    ]);
  });

  it('uses user-facing labels for minimal workbench setup', () => {
    expect(stepsForMode('minimal_vscode').map(s => s.label)).toEqual([
      '连接大模型',
      '连接星池工作区',
      '安装极简工作台',
      '准备极简工作台',
      '完成',
    ]);
  });

  it('maps completed tokens by mode', () => {
    expect(completedMapForMode('codex_desktop').codex_desktop_installed).toBe('codex_desktop_install');
    expect(completedMapForMode('codex_desktop').vscode_installed).toBeUndefined();
    expect(completedMapForMode('minimal_vscode').vscode_installed).toBe('vscode_install');
    expect(completedMapForMode('minimal_vscode').codex_desktop_installed).toBeUndefined();
  });
});
