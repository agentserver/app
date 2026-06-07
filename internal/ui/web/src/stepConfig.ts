export type StepKind = 'oauth' | 'progress' | 'action';
export type FrontendMode = 'codex_desktop' | 'minimal_vscode';

export interface StepDef {
  id: string;
  label: string;
  kind: StepKind;
  autoStart: boolean;
}

const CODEX_DESKTOP_STEPS: ReadonlyArray<StepDef> = [
  { id: 'modelserver_login',       label: '登录 modelserver',   kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login',       label: '登录 agentserver',   kind: 'oauth',    autoStart: false },
  { id: 'codex_desktop_install',   label: '安装 Codex Desktop', kind: 'progress', autoStart: true  },
  { id: 'codex_desktop_configure', label: '配置 Codex Desktop', kind: 'action',   autoStart: true  },
  { id: 'finalize',                label: '完成配置',           kind: 'action',   autoStart: false },
];

const MINIMAL_VSCODE_STEPS: ReadonlyArray<StepDef> = [
  { id: 'modelserver_login', label: '登录 modelserver', kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login', label: '登录 agentserver', kind: 'oauth',    autoStart: false },
  { id: 'vscode_install',    label: '安装极简界面',     kind: 'progress', autoStart: true  },
  { id: 'vscode_configure',  label: '准备极简界面',     kind: 'action',   autoStart: true  },
  { id: 'finalize',          label: '完成配置',         kind: 'action',   autoStart: false },
];

export function normalizeMode(mode?: string | null): FrontendMode {
  return mode === 'minimal_vscode' ? 'minimal_vscode' : 'codex_desktop';
}

export function stepsForMode(mode?: string | null): ReadonlyArray<StepDef> {
  return normalizeMode(mode) === 'minimal_vscode' ? MINIMAL_VSCODE_STEPS : CODEX_DESKTOP_STEPS;
}

export function completedMapForMode(mode?: string | null): Record<string, string> {
  if (normalizeMode(mode) === 'minimal_vscode') {
    return {
      modelserver_login: 'modelserver_login',
      agentserver_login: 'agentserver_login',
      vscode_installed: 'vscode_install',
      vscode_configured: 'vscode_configure',
      shortcuts_created: 'finalize',
    };
  }
  return {
    modelserver_login: 'modelserver_login',
    agentserver_login: 'agentserver_login',
    codex_desktop_installed: 'codex_desktop_install',
    codex_desktop_configured: 'codex_desktop_configure',
    shortcuts_created: 'finalize',
  };
}
