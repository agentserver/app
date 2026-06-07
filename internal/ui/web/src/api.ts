// All HTTP traffic to the launcher's onboarding HTTP server is routed
// through this module. Components / composables never call fetch directly.

export interface ServerState {
  schema_version: number;
  install_id: string;
  frontend_mode?: 'codex_desktop' | 'minimal_vscode';
  frontend_name?: string;
  onboarding_status: 'pending' | 'in_progress' | 'complete';
  completed_steps: string[] | null;
  last_error?: string;
  modelserver_project_id?: string;
  agentserver_workspace_id?: string;
  vscode_path?: string;
  vscode_version?: string;
  codex_desktop_installed?: boolean;
  codex_desktop_version?: string;
}

export interface StartStepResponse {
  state: 'started';
  oauth_url?: string;
}

export interface StepStatusResponse {
  state: 'waiting' | 'success';
  error?: string;
  key_suffix?: string;
}

export interface StreamHandle {
  stream_id: string;
}

export class OnboardingError extends Error {
  constructor(
    message: string,
    public readonly status?: number,
    public readonly detail?: string,
  ) {
    super(message);
    this.name = 'OnboardingError';
  }
}

async function request<T>(input: string, init?: RequestInit): Promise<T> {
  let resp: Response;
  try {
    resp = await fetch(input, init);
  } catch (e) {
    throw new OnboardingError(
      '网络错误: ' + (e instanceof Error ? e.message : String(e)),
    );
  }
  if (!resp.ok) {
    let body = '';
    try { body = await resp.text(); } catch { /* ignore */ }
    throw new OnboardingError(
      `${input} 返回 ${resp.status}: ${body || resp.statusText}`,
      resp.status,
      body,
    );
  }
  return resp.json() as Promise<T>;
}

export const getState = () => request<ServerState>('/api/state');

export const startStep = (stepId: string) =>
  request<StartStepResponse>(`/api/step/${stepId}`, { method: 'POST' });

export const pollStepStatus = (stepId: string) =>
  request<StepStatusResponse>(`/api/step/${stepId}/status`);

export const startFrontendInstall = () =>
  request<StreamHandle>('/api/step/frontend_install', { method: 'POST' });

export const configureFrontend = () =>
  request<{ state: 'success' }>('/api/step/frontend_configure', { method: 'POST' });

export const finalize = () =>
  request<{ state: 'complete' }>('/api/finalize', { method: 'POST' });

export const launchFrontend = () =>
  request<{ state: 'launching' }>('/api/launch', { method: 'POST' });

export const startVSCodeInstall = startFrontendInstall;
export const configureVSCode = configureFrontend;
export const launchVSCode = launchFrontend;

export const abort = () =>
  request<{ state: 'aborted' }>('/api/abort', { method: 'POST' });
