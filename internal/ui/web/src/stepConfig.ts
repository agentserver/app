export type StepKind = 'oauth' | 'progress' | 'action';
export type FrontendMode = 'codex_desktop' | 'opencode_desktop' | 'minimal_vscode';

export interface StepDef {
  id: string;
  label: string;
  kind: StepKind;
  autoStart: boolean;
}

const CODEX_DESKTOP_STEPS: ReadonlyArray<StepDef> = [
  { id: 'modelserver_login',       label: '连接大模型',                    kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login',       label: '连接星池工作区',                kind: 'oauth',    autoStart: false },
  { id: 'codex_desktop_install',   label: '安装 Codex Desktop 智能助手',   kind: 'progress', autoStart: true  },
  { id: 'codex_desktop_configure', label: '准备 Codex Desktop 智能助手',   kind: 'action',   autoStart: true  },
  { id: 'finalize',                label: '完成',                          kind: 'action',   autoStart: false },
];

const MINIMAL_VSCODE_STEPS: ReadonlyArray<StepDef> = [
  { id: 'modelserver_login', label: '连接大模型',       kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login', label: '连接星池工作区',   kind: 'oauth',    autoStart: false },
  { id: 'vscode_install',    label: '安装极简工作台',   kind: 'progress', autoStart: true  },
  { id: 'vscode_configure',  label: '准备极简工作台',   kind: 'action',   autoStart: true  },
  { id: 'finalize',          label: '完成',             kind: 'action',   autoStart: false },
];

const OPENCODE_DESKTOP_STEPS: ReadonlyArray<StepDef> = [
  { id: 'modelserver_login',          label: '连接大模型',                         kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login',          label: '连接星池工作区',                     kind: 'oauth',    autoStart: false },
  { id: 'opencode_desktop_install',   label: '安装 OpenCode Desktop 智能助手',     kind: 'progress', autoStart: true  },
  { id: 'opencode_desktop_configure', label: '准备 OpenCode Desktop 智能助手',     kind: 'action',   autoStart: true  },
  { id: 'finalize',                   label: '完成',                               kind: 'action',   autoStart: false },
];

export function normalizeMode(mode?: string | null): FrontendMode {
  if (mode === 'opencode_desktop') return 'opencode_desktop';
  return mode === 'minimal_vscode' ? 'minimal_vscode' : 'codex_desktop';
}

export function stepsForMode(mode?: string | null): ReadonlyArray<StepDef> {
  const normalized = normalizeMode(mode);
  if (normalized === 'minimal_vscode') return MINIMAL_VSCODE_STEPS;
  if (normalized === 'opencode_desktop') return OPENCODE_DESKTOP_STEPS;
  return CODEX_DESKTOP_STEPS;
}

export function completedMapForMode(mode?: string | null): Record<string, string> {
  const normalized = normalizeMode(mode);
  if (normalized === 'minimal_vscode') {
    return {
      modelserver_login: 'modelserver_login',
      agentserver_login: 'agentserver_login',
      vscode_installed: 'vscode_install',
      vscode_configured: 'vscode_configure',
      shortcuts_created: 'finalize',
    };
  }
  if (normalized === 'opencode_desktop') {
    return {
      modelserver_login: 'modelserver_login',
      agentserver_login: 'agentserver_login',
      opencode_desktop_installed: 'opencode_desktop_install',
      opencode_desktop_configured: 'opencode_desktop_configure',
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
