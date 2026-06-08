import { describe, it, expect, vi, beforeEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import Dashboard from '../components/Dashboard.vue';
import * as api from '../api';

function consoleState(): api.ConsoleState {
  return {
    frontend_mode: 'codex_desktop',
    frontend_name: 'Codex Desktop',
    onboarding_status: 'complete',
    modelserver: { project_id: 'proj-1', project_name: 'Default project' },
    agentserver: { workspace_id: 'ws-1', workspace_name: 'Default workspace' },
    subscription_url: 'https://code.cs.ac.cn/projects/proj-1/subscription',
    quotas: [
      { window: '5h', percentage: 58, remaining_percentage: 42 },
      { window: '7d', percentage: 22, remaining_percentage: 78 },
    ],
    last_refreshed_at: '2026-06-07T12:00:00Z',
  };
}

function mockConsoleState() {
  vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe('Dashboard', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('renders project, workspace, quota, and subscription action', async () => {
    mockConsoleState();
    const w = mount(Dashboard);
    await flushPromises();
    expect(w.text()).toContain('Default project');
    expect(w.text()).toContain('Default workspace');
    expect(w.text()).toContain('5小时');
    expect(w.text()).toContain('剩余约 42%');
  });

  it('does not expose raw workspace id when workspace name is missing', async () => {
    const state = consoleState();
    state.agentserver = { workspace_id: 'ws-1234567890abcdef' };
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(state);

    const w = mount(Dashboard);
    await flushPromises();

    expect(w.text()).toContain('工作空间 90abcdef');
    expect(w.text()).not.toContain('ws-1234567890abcdef');
  });

  it('displays an error when opening the frontend fails', async () => {
    mockConsoleState();
    vi.spyOn(api, 'openConsoleFrontend').mockRejectedValue(new Error('open failed'));
    const w = mount(Dashboard);
    await flushPromises();

    const openButton = w.findAll('button').find(b => b.text().includes('打开 Codex Desktop'));
    expect(openButton).toBeDefined();
    await openButton!.trigger('click');
    await flushPromises();

    expect(w.text()).toContain('open failed');
  });

  it('keeps frontend errors visible when an overlapping refresh succeeds later', async () => {
    mockConsoleState();
    const refresh = deferred<api.ConsoleState>();
    vi.spyOn(api, 'refreshConsoleState').mockReturnValue(refresh.promise);
    vi.spyOn(api, 'openConsoleFrontend').mockRejectedValue(new Error('open failed'));
    const w = mount(Dashboard);
    await flushPromises();

    const refreshButton = w.findAll('button').find(b => b.text().includes('刷新状态'));
    const openButton = w.findAll('button').find(b => b.text().includes('打开 Codex Desktop'));
    expect(refreshButton).toBeDefined();
    expect(openButton).toBeDefined();
    await refreshButton!.trigger('click');
    await openButton!.trigger('click');
    await flushPromises();
    expect(w.text()).toContain('open failed');

    refresh.resolve(consoleState());
    await flushPromises();

    expect(w.text()).toContain('open failed');
  });

  it('displays an error when opening the subscription fails', async () => {
    mockConsoleState();
    vi.spyOn(api, 'openConsoleSubscription').mockRejectedValue(new Error('subscription failed'));
    const w = mount(Dashboard);
    await flushPromises();

    const subscriptionButton = w.findAll('button').find(b => b.text().includes('打开订阅页'));
    expect(subscriptionButton).toBeDefined();
    await subscriptionButton!.trigger('click');
    await flushPromises();

    expect(w.text()).toContain('subscription failed');
  });

  it('logs out modelserver after confirmation and refreshes state', async () => {
    mockConsoleState();
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const logoutSpy = vi.spyOn(api, 'logoutConsoleModelserver').mockResolvedValue({ state: 'logged_out' });
    const refreshed = consoleState();
    refreshed.modelserver = { reconnect_required: true, auth_message: '大模型连接已失效，请重新连接。' };
    refreshed.subscription_url = 'https://code.cs.ac.cn/projects';
    const refreshSpy = vi.spyOn(api, 'refreshConsoleState').mockResolvedValue(refreshed);
    const w = mount(Dashboard);
    await flushPromises();

    const logoutButton = w.findAll('button').find(b => b.text().includes('退出大模型登录'));
    expect(logoutButton).toBeDefined();
    await logoutButton!.trigger('click');
    await flushPromises();

    expect(confirmSpy).toHaveBeenCalledWith(expect.stringContaining('退出大模型登录'));
    expect(logoutSpy).toHaveBeenCalledTimes(1);
    expect(refreshSpy).toHaveBeenCalledTimes(1);
    expect(w.text()).toContain('大模型连接已失效，请重新连接。');
  });

  it('reconnects modelserver when the token can no longer be refreshed', async () => {
    const expired = consoleState();
    expired.modelserver.reconnect_required = true;
    expired.modelserver.auth_message = '大模型连接已失效，请重新连接。';
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(expired);
    const startSpy = vi.spyOn(api, 'startStep').mockResolvedValue({
      state: 'started',
      oauth_url: 'https://codeapi.example/oauth2/auth',
    });
    const pollSpy = vi.spyOn(api, 'pollStepStatus').mockResolvedValue({ state: 'success' });
    const refreshSpy = vi.spyOn(api, 'refreshConsoleState').mockResolvedValue(consoleState());

    const w = mount(Dashboard);
    await flushPromises();

    expect(w.text()).toContain('大模型连接已失效，请重新连接。');
    const reconnectButton = w.findAll('button').find(b => b.text().includes('重新连接大模型'));
    expect(reconnectButton).toBeDefined();
    await reconnectButton!.trigger('click');
    await flushPromises();

    expect(startSpy).toHaveBeenCalledWith('modelserver_login');
    expect(pollSpy).toHaveBeenCalledWith('modelserver_login');
    expect(refreshSpy).toHaveBeenCalledTimes(1);
  });

  it('ignores duplicate refresh clicks while refresh is pending', async () => {
    mockConsoleState();
    const refreshSpy = vi.spyOn(api, 'refreshConsoleState').mockReturnValue(new Promise(() => {}));
    const w = mount(Dashboard);
    await flushPromises();

    const refreshButton = w.findAll('button').find(b => b.text().includes('刷新状态'));
    expect(refreshButton).toBeDefined();
    await refreshButton!.trigger('click');
    await refreshButton!.trigger('click');

    expect(refreshSpy).toHaveBeenCalledTimes(1);
  });

  it('ignores duplicate frontend clicks while opening is pending', async () => {
    mockConsoleState();
    const openSpy = vi.spyOn(api, 'openConsoleFrontend').mockReturnValue(new Promise(() => {}));
    const w = mount(Dashboard);
    await flushPromises();

    const openButton = w.findAll('button').find(b => b.text().includes('打开 Codex Desktop'));
    expect(openButton).toBeDefined();
    await openButton!.trigger('click');
    await openButton!.trigger('click');

    expect(openSpy).toHaveBeenCalledTimes(1);
  });

  it('ignores duplicate subscription clicks while opening is pending', async () => {
    mockConsoleState();
    const subscriptionSpy = vi.spyOn(api, 'openConsoleSubscription').mockReturnValue(new Promise(() => {}));
    const w = mount(Dashboard);
    await flushPromises();

    const subscriptionButton = w.findAll('button').find(b => b.text().includes('打开订阅页'));
    expect(subscriptionButton).toBeDefined();
    await subscriptionButton!.trigger('click');
    await subscriptionButton!.trigger('click');

    expect(subscriptionSpy).toHaveBeenCalledTimes(1);
  });
});
