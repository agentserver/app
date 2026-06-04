export type StepKind = 'oauth' | 'progress' | 'action';

export interface StepDef {
  id: string;
  label: string;
  kind: StepKind;
  // True = automatically start this step when the previous step succeeds.
  // False = wait for user to click "开始" (OAuth steps and finalize).
  autoStart: boolean;
}

export const STEPS: ReadonlyArray<StepDef> = [
  { id: 'modelserver_login', label: '登录 modelserver',       kind: 'oauth',    autoStart: false },
  { id: 'agentserver_login', label: '登录 agentserver',       kind: 'oauth',    autoStart: false },
  { id: 'vscode_install',    label: '安装 VS Code',            kind: 'progress', autoStart: true  },
  { id: 'vscode_configure',  label: '配置 VS Code 与 codex',   kind: 'action',   autoStart: true  },
  { id: 'finalize',          label: '完成配置',                kind: 'action',   autoStart: false },
];
