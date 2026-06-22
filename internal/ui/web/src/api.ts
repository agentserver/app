// All HTTP traffic to the launcher's onboarding HTTP server is routed
// through this module. Components / composables never call fetch directly.

export interface ServerState {
  schema_version: number;
  install_id: string;
  frontend_mode?: 'codex_desktop' | 'opencode_desktop' | 'minimal_vscode';
  frontend_name?: string;
  onboarding_status: 'pending' | 'in_progress' | 'complete';
  completed_steps: string[] | null;
  last_error?: string;
  modelserver_project_id?: string;
  agentserver_workspace_id?: string;
  agentserver_workspace_name?: string;
  vscode_path?: string;
  vscode_version?: string;
  codex_desktop_installed?: boolean;
  codex_desktop_version?: string;
  opencode_desktop_installed?: boolean;
  opencode_desktop_version?: string;
  opencode_desktop_path?: string;
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

export interface ConsoleQuota {
  window: string;
  percentage: number;
  remaining_percentage: number;
  resets_at?: string;
}

export interface ConsoleModelOption {
  name: string;
  display_name?: string;
}

export interface ConsoleState {
  frontend_mode: 'codex_desktop' | 'opencode_desktop' | 'minimal_vscode';
  frontend_name: string;
  onboarding_status: string;
  modelserver: {
    project_id?: string;
    project_name?: string;
    reconnect_required?: boolean;
    auth_message?: string;
  };
  agentserver: {
    workspace_id?: string;
    workspace_name?: string;
    reconnect_required?: boolean;
    auth_message?: string;
  };
  quotas: ConsoleQuota[];
  quota_error?: string;
  subscription_url?: string;
  current_model?: string;
  available_models?: ConsoleModelOption[];
  last_refreshed_at?: string;
}

export interface ConsoleMachine {
  machine_id: string;
  computer_name: string;
}

export type ConsoleSlaveStatus =
  'stopped' | 'starting' | 'auth_required' | 'running' | 'paused' | 'error';

export interface ConsoleSlave {
  id: string;
  name: string;
  display_name: string;
  folder: string;
  config_path?: string;
  status: ConsoleSlaveStatus;
  pid?: number;
  auth_url?: string;
  last_error?: string;
  created_at?: string;
  updated_at?: string;
}

export interface ConsoleSlavesResponse {
  machine: ConsoleMachine;
  slaves: ConsoleSlave[];
}

export interface CreateConsoleSlaveInput {
  folder: string;
  name?: string;
}

export interface SelectConsoleSlaveFolderResponse {
  folder: string;
}

export interface OpenConsoleSlaveRemoteResponse {
  state: 'opened' | 'unavailable';
  url?: string;
}

export type ConsoleUpdateStatus =
  'idle'
  | 'checking'
  | 'latest'
  | 'available'
  | 'downloading'
  | 'installer_started'
  | 'error';

export interface ConsoleAvailableUpdate {
  version: string;
  url?: string;
  sha256?: string;
  size?: number;
  notes?: string;
}

export interface ConsoleUpdateState {
  current_version: string;
  last_checked_at?: string;
  status: ConsoleUpdateStatus;
  update?: ConsoleAvailableUpdate;
  last_error?: string;
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

const consoleTokenHeader = 'X-AgentServer-Console-Token';

function consoleInstanceToken(): string {
  if (typeof document === 'undefined') {
    return '';
  }
  return document
    .querySelector<HTMLMetaElement>('meta[name="agentserver-console-token"]')
    ?.content
    .trim() ?? '';
}

function withConsoleToken(init?: RequestInit): RequestInit | undefined {
  const token = consoleInstanceToken();
  if (!token) {
    return init;
  }
  const headers = new Headers(init?.headers);
  headers.set(consoleTokenHeader, token);
  return { ...init, headers };
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
  request<StepStatusResponse>(`/api/step/${stepId}/status`, { method: 'POST' });

export const startFrontendInstall = () =>
  request<StreamHandle>('/api/step/frontend_install', { method: 'POST' });

export const configureFrontend = () =>
  request<{ state: 'success' }>('/api/step/frontend_configure', { method: 'POST' });

export const finalize = () =>
  request<{ state: 'complete' }>('/api/finalize', { method: 'POST' });

export const launchFrontend = () =>
  request<{ state: 'launching' }>('/api/launch', { method: 'POST' });

export const getConsoleState = () => request<ConsoleState>('/api/console/state');

export const refreshConsoleState = () =>
  request<ConsoleState>('/api/console/refresh', withConsoleToken({ method: 'POST' }));

export const openConsoleFrontend = () =>
  request<{ state: 'opened' }>('/api/console/open-frontend', withConsoleToken({ method: 'POST' }));

export const openConsoleSubscription = () =>
  request<{ state: 'opened' }>('/api/console/open-subscription', withConsoleToken({ method: 'POST' }));

export const logoutConsoleModelserver = () =>
  request<{ state: 'logged_out' }>('/api/console/logout-modelserver', withConsoleToken({ method: 'POST' }));

export const setConsoleModel = (model: string) =>
  request<{ state: 'set'; model: string }>('/api/console/model', withConsoleToken({
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model }),
  }));

export const getConsoleSlaves = () =>
  request<ConsoleSlavesResponse>('/api/console/slaves');

export const createConsoleSlave = (input: CreateConsoleSlaveInput) =>
  request<ConsoleSlave>('/api/console/slaves', withConsoleToken({
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  }));

export const selectConsoleSlaveFolder = () =>
  request<SelectConsoleSlaveFolderResponse>('/api/console/select-folder', withConsoleToken({ method: 'POST' }));

export const restartConsoleSlave = (id: string) =>
  request<ConsoleSlave>(`/api/console/slaves/${encodeURIComponent(id)}/restart`, withConsoleToken({ method: 'POST' }));

export const pauseConsoleSlave = (id: string) =>
  request<ConsoleSlave>(`/api/console/slaves/${encodeURIComponent(id)}/pause`, withConsoleToken({ method: 'POST' }));

export const openConsoleSlaveRemote = (id: string) =>
  request<OpenConsoleSlaveRemoteResponse>(`/api/console/slaves/${encodeURIComponent(id)}/open-remote`, withConsoleToken({ method: 'POST' }));

export const deleteConsoleSlave = (id: string) =>
  request<{ state: 'deleted' }>(`/api/console/slaves/${encodeURIComponent(id)}`, withConsoleToken({ method: 'DELETE' }));

export const getConsoleUpdate = () =>
  request<ConsoleUpdateState>('/api/console/update');

export const checkConsoleUpdate = () =>
  request<ConsoleUpdateState>('/api/console/update/check', withConsoleToken({ method: 'POST' }));

export const installConsoleUpdate = () =>
  request<ConsoleUpdateState>('/api/console/update/install', withConsoleToken({ method: 'POST' }));

export const startVSCodeInstall = startFrontendInstall;
export const configureVSCode = configureFrontend;
export const launchVSCode = launchFrontend;

export const abort = () =>
  request<{ state: 'aborted' }>('/api/abort', { method: 'POST' });
