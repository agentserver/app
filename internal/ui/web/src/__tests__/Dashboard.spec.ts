import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
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

function consoleSlaves(overrides?: Partial<api.ConsoleSlavesResponse>): api.ConsoleSlavesResponse {
  return {
    machine: { machine_id: 'machine-1', computer_name: 'devbox' },
    slaves: [],
    ...overrides,
  };
}

function consoleUpdate(overrides?: Partial<api.ConsoleUpdateState>): api.ConsoleUpdateState {
  return {
    current_version: '1.2.3',
    status: 'latest',
    last_checked_at: '2026-06-07T12:00:00Z',
    ...overrides,
  };
}

function mockConsoleState() {
  vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
  vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves());
}

async function setInput(w: ReturnType<typeof mount>, testId: string, value: string) {
  const wrappedInput = w.find(`[data-test="${testId}"] input`);
  const input = wrappedInput.exists()
    ? wrappedInput
    : w.find(`input[data-test="${testId}"]`);
  expect(input.exists()).toBe(true);
  await input.setValue(value);
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
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    vi.spyOn(api, 'getConsoleUpdate').mockResolvedValue(consoleUpdate());
  });
  afterEach(() => vi.useRealTimers());

  it('loads and renders console update state on mount', async () => {
    const getUpdateSpy = vi.spyOn(api, 'getConsoleUpdate').mockResolvedValue(consoleUpdate({
      current_version: '1.2.3',
      status: 'available',
      update: {
        version: '1.3.0',
        notes: 'Fixes startup checks',
      },
    }));
    mockConsoleState();

    const w = mount(Dashboard);
    await flushPromises();

    expect(getUpdateSpy).toHaveBeenCalledTimes(1);
    expect(w.text()).toContain('当前版本 1.2.3');
    expect(w.text()).toContain('发现新版本 1.3.0');
    expect(w.text()).toContain('Fixes startup checks');
  });

  it('checks for console updates manually and refreshes displayed state', async () => {
    mockConsoleState();
    const checkSpy = vi.spyOn(api, 'checkConsoleUpdate').mockResolvedValue(consoleUpdate({
      current_version: '1.2.3',
      status: 'available',
      update: { version: '1.3.0' },
    }));

    const w = mount(Dashboard);
    await flushPromises();
    const checkButton = w.find('[data-test="check-console-update"]');
    expect(checkButton.exists()).toBe(true);
    await checkButton.trigger('click');
    await flushPromises();

    expect(checkSpy).toHaveBeenCalledTimes(1);
    expect(w.text()).toContain('发现新版本 1.3.0');
  });

  it('does not install an available console update when confirmation is cancelled', async () => {
    vi.spyOn(api, 'getConsoleUpdate').mockResolvedValue(consoleUpdate({
      status: 'available',
      update: { version: '1.3.0' },
    }));
    mockConsoleState();
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const installSpy = vi.spyOn(api, 'installConsoleUpdate').mockResolvedValue(consoleUpdate({
      status: 'installer_started',
    }));

    const w = mount(Dashboard);
    await flushPromises();
    const installButton = w.find('[data-test="install-console-update"]');
    expect(installButton.exists()).toBe(true);
    await installButton.trigger('click');
    await flushPromises();

    expect(confirmSpy).toHaveBeenCalledWith(expect.stringContaining('1.3.0'));
    expect(installSpy).not.toHaveBeenCalled();
  });

  it('installs an available console update after confirmation without sending manifest details', async () => {
    vi.spyOn(api, 'getConsoleUpdate').mockResolvedValue(consoleUpdate({
      status: 'available',
      update: {
        version: '1.3.0',
        url: 'https://updates.example/console',
        sha256: 'abc123',
      },
    }));
    mockConsoleState();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const installSpy = vi.spyOn(api, 'installConsoleUpdate').mockResolvedValue(consoleUpdate({
      status: 'installer_started',
      update: { version: '1.3.0' },
    }));

    const w = mount(Dashboard);
    await flushPromises();
    await w.find('[data-test="install-console-update"]').trigger('click');
    await flushPromises();

    expect(installSpy).toHaveBeenCalledTimes(1);
    expect(installSpy).toHaveBeenCalledWith();
    expect(w.text()).toContain('安装程序已启动');
  });

  it('displays console update API errors with dashboard errors', async () => {
    mockConsoleState();
    vi.spyOn(api, 'checkConsoleUpdate').mockRejectedValue(new Error('update service unavailable'));

    const w = mount(Dashboard);
    await flushPromises();
    await w.find('[data-test="check-console-update"]').trigger('click');
    await flushPromises();

    expect(w.text()).toContain('update service unavailable');
  });

  it('posts console update install without a user-controlled manifest body', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      json: async () => consoleUpdate({ status: 'installer_started' }),
    } as Response);

    await api.installConsoleUpdate();

    expect(fetchSpy).toHaveBeenCalledWith('/api/console/update/install', { method: 'POST' });
  });

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

  it('reconnects agentserver when the workspace token is unauthorized', async () => {
    const expired = consoleState();
    expired.agentserver.reconnect_required = true;
    expired.agentserver.auth_message = '星池工作区连接已失效，请重新连接。';
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(expired);
    vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves());
    const startSpy = vi.spyOn(api, 'startStep').mockResolvedValue({
      state: 'started',
      oauth_url: 'https://agent.example/device',
    });
    const pollSpy = vi.spyOn(api, 'pollStepStatus').mockResolvedValue({ state: 'success' });
    const refreshSpy = vi.spyOn(api, 'refreshConsoleState').mockResolvedValue(consoleState());

    const w = mount(Dashboard);
    await flushPromises();

    expect(w.text()).toContain('星池工作区连接已失效，请重新连接。');
    const reconnectButton = w.findAll('button').find(b => b.text().includes('重新连接星池工作区'));
    expect(reconnectButton).toBeDefined();
    await reconnectButton!.trigger('click');
    await flushPromises();

    expect(startSpy).toHaveBeenCalledWith('agentserver_login');
    expect(pollSpy).toHaveBeenCalledWith('agentserver_login');
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

  it('renders local slave group with machine name and auth link', async () => {
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
    vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves({
      slaves: [{
        id: 'sl-1',
        name: 'worker',
        display_name: 'devbox-worker',
        folder: '/repo/app',
        status: 'auth_required',
        auth_url: 'https://auth.example/device',
        last_error: '等待认证',
      }],
    }));

    const w = mount(Dashboard);
    await flushPromises();

    expect(w.text()).toContain('允许被远程控制的文件夹（智能体形式提供）');
    expect(w.text()).toContain('本机：devbox');
    expect(w.text()).toContain('devbox-worker');
    expect(w.text()).toContain('/repo/app');
    expect(w.text()).toContain('待认证');
    expect(w.text()).toContain('等待认证');
    const authLink = w.find('a[href="https://auth.example/device"]');
    expect(authLink.exists()).toBe(true);
    expect(authLink.text()).toContain('认证');
  });

  it('creates a local slave with folder and custom name then refreshes', async () => {
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
    const getSlavesSpy = vi.spyOn(api, 'getConsoleSlaves')
      .mockResolvedValueOnce(consoleSlaves())
      .mockResolvedValueOnce(consoleSlaves({
        slaves: [{
          id: 'sl-1',
          name: 'worker',
          display_name: 'devbox-worker',
          folder: '/repo/app',
          status: 'starting',
        }],
      }));
    const createSpy = vi.spyOn(api, 'createConsoleSlave').mockResolvedValue({
      id: 'sl-1',
      name: 'worker',
      display_name: 'devbox-worker',
      folder: '/repo/app',
      status: 'starting',
    });
    const selectSpy = vi.spyOn(api, 'selectConsoleSlaveFolder').mockResolvedValue({ folder: '/repo/app' });

    const w = mount(Dashboard);
    await flushPromises();
    await w.find('[data-test="select-slave-folder"]').trigger('click');
    await flushPromises();
    await setInput(w, 'slave-name-input', 'worker');
    expect(w.text()).toContain('devbox-worker');

    await w.find('[data-test="create-slave"]').trigger('click');
    await flushPromises();

    expect(selectSpy).toHaveBeenCalledTimes(1);
    expect(createSpy).toHaveBeenCalledWith({ folder: '/repo/app', name: 'worker' });
    expect(getSlavesSpy).toHaveBeenCalledTimes(2);
    expect(w.text()).toContain('devbox-worker');
  });

  it('displays an error when native folder selection fails', async () => {
    mockConsoleState();
    vi.spyOn(api, 'selectConsoleSlaveFolder').mockRejectedValue(new Error('picker unavailable'));

    const w = mount(Dashboard);
    await flushPromises();
    await w.find('[data-test="select-slave-folder"]').trigger('click');
    await flushPromises();

    expect(w.text()).toContain('picker unavailable');
  });

  it('automatically refreshes pending local slave status until auth is available', async () => {
    vi.useFakeTimers();
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
    const getSlavesSpy = vi.spyOn(api, 'getConsoleSlaves')
      .mockResolvedValueOnce(consoleSlaves())
      .mockResolvedValueOnce(consoleSlaves({
        slaves: [{
          id: 'sl-1',
          name: 'worker',
          display_name: 'devbox-worker',
          folder: '/repo/app',
          status: 'starting',
        }],
      }))
      .mockResolvedValueOnce(consoleSlaves({
        slaves: [{
          id: 'sl-1',
          name: 'worker',
          display_name: 'devbox-worker',
          folder: '/repo/app',
          status: 'auth_required',
          auth_url: 'https://auth.example/device',
        }],
      }));
    vi.spyOn(api, 'createConsoleSlave').mockResolvedValue({
      id: 'sl-1',
      name: 'worker',
      display_name: 'devbox-worker',
      folder: '/repo/app',
      status: 'starting',
    });

    const w = mount(Dashboard);
    await flushPromises();
    await setInput(w, 'slave-folder-input', '/repo/app');
    await setInput(w, 'slave-name-input', 'worker');
    await w.find('[data-test="create-slave"]').trigger('click');
    await flushPromises();

    expect(w.text()).toContain('启动中');
    await vi.advanceTimersByTimeAsync(3000);
    await flushPromises();

    expect(getSlavesSpy).toHaveBeenCalledTimes(3);
    expect(w.text()).toContain('待认证');
    expect(w.find('a[href="https://auth.example/device"]').exists()).toBe(true);
    w.unmount();
  });

  it('does not recreate local slave polling when an in-flight load resolves after unmount', async () => {
    vi.useFakeTimers();
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
    const initial = deferred<api.ConsoleSlavesResponse>();
    const getSlavesSpy = vi.spyOn(api, 'getConsoleSlaves')
      .mockReturnValueOnce(initial.promise)
      .mockResolvedValue(consoleSlaves({
        slaves: [{
          id: 'sl-1',
          name: 'worker',
          display_name: 'devbox-worker',
          folder: '/repo/app',
          status: 'auth_required',
          auth_url: 'https://auth.example/device',
        }],
      }));

    const w = mount(Dashboard);
    await flushPromises();
    w.unmount();
    initial.resolve(consoleSlaves({
      slaves: [{
        id: 'sl-1',
        name: 'worker',
        display_name: 'devbox-worker',
        folder: '/repo/app',
        status: 'starting',
      }],
    }));
    await flushPromises();
    await vi.advanceTimersByTimeAsync(3000);
    await flushPromises();

    expect(getSlavesSpy).toHaveBeenCalledTimes(1);
  });

  it('keeps the newest local slave refresh when the initial load resolves later', async () => {
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
    const staleInitial = deferred<api.ConsoleSlavesResponse>();
    const freshAfterCreate = deferred<api.ConsoleSlavesResponse>();
    vi.spyOn(api, 'getConsoleSlaves')
      .mockReturnValueOnce(staleInitial.promise)
      .mockReturnValueOnce(freshAfterCreate.promise);
    vi.spyOn(api, 'createConsoleSlave').mockResolvedValue({
      id: 'sl-2',
      name: 'fresh',
      display_name: 'devbox-fresh',
      folder: '/repo/fresh',
      status: 'running',
    });

    const w = mount(Dashboard);
    await flushPromises();
    await setInput(w, 'slave-folder-input', '/repo/fresh');
    await setInput(w, 'slave-name-input', 'fresh');
    await w.find('[data-test="create-slave"]').trigger('click');
    await flushPromises();

    freshAfterCreate.resolve(consoleSlaves({
      slaves: [{
        id: 'sl-2',
        name: 'fresh',
        display_name: 'devbox-fresh',
        folder: '/repo/fresh',
        status: 'running',
      }],
    }));
    await flushPromises();
    expect(w.text()).toContain('devbox-fresh');

    staleInitial.resolve(consoleSlaves({
      slaves: [{
        id: 'sl-1',
        name: 'stale',
        display_name: 'devbox-stale',
        folder: '/repo/stale',
        status: 'paused',
      }],
    }));
    await flushPromises();

    expect(w.text()).toContain('devbox-fresh');
    expect(w.text()).not.toContain('devbox-stale');
  });

  it('ignores stale local slave load failures after a newer refresh succeeds', async () => {
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
    const staleInitial = deferred<api.ConsoleSlavesResponse>();
    const freshAfterCreate = deferred<api.ConsoleSlavesResponse>();
    vi.spyOn(api, 'getConsoleSlaves')
      .mockReturnValueOnce(staleInitial.promise)
      .mockReturnValueOnce(freshAfterCreate.promise);
    vi.spyOn(api, 'createConsoleSlave').mockResolvedValue({
      id: 'sl-2',
      name: 'fresh',
      display_name: 'devbox-fresh',
      folder: '/repo/fresh',
      status: 'running',
    });

    const w = mount(Dashboard);
    await flushPromises();
    await setInput(w, 'slave-folder-input', '/repo/fresh');
    await setInput(w, 'slave-name-input', 'fresh');
    await w.find('[data-test="create-slave"]').trigger('click');
    await flushPromises();

    freshAfterCreate.resolve(consoleSlaves({
      slaves: [{
        id: 'sl-2',
        name: 'fresh',
        display_name: 'devbox-fresh',
        folder: '/repo/fresh',
        status: 'running',
      }],
    }));
    await flushPromises();
    staleInitial.reject(new Error('stale failed'));
    await flushPromises();

    expect(w.text()).toContain('devbox-fresh');
    expect(w.text()).not.toContain('stale failed');
  });

  it('blocks local slave names longer than 20 characters', async () => {
    mockConsoleState();
    const createSpy = vi.spyOn(api, 'createConsoleSlave').mockResolvedValue({} as api.ConsoleSlave);

    const w = mount(Dashboard);
    await flushPromises();
    await setInput(w, 'slave-folder-input', '/repo/app');
    await setInput(w, 'slave-name-input', '工'.repeat(21));
    await w.find('[data-test="create-slave"]').trigger('click');
    await flushPromises();

    expect(createSpy).not.toHaveBeenCalled();
    expect(w.text()).toContain('名称最多 20 个字符');
  });

  it('runs local slave actions and refreshes after confirmation for delete', async () => {
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
    const getSlavesSpy = vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves({
      slaves: [{
        id: 'sl-1',
        name: 'worker',
        display_name: 'devbox-worker',
        folder: '/repo/app',
        status: 'running',
      }],
    }));
    const restartSpy = vi.spyOn(api, 'restartConsoleSlave').mockResolvedValue({} as api.ConsoleSlave);
    const pauseSpy = vi.spyOn(api, 'pauseConsoleSlave').mockResolvedValue({} as api.ConsoleSlave);
    const openRemoteSpy = vi.spyOn(api, 'openConsoleSlaveRemote').mockResolvedValue({ state: 'unavailable' });
    const deleteSpy = vi.spyOn(api, 'deleteConsoleSlave').mockResolvedValue({ state: 'deleted' });
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);

    const w = mount(Dashboard);
    await flushPromises();

    await w.find('[data-test="restart-slave-sl-1"]').trigger('click');
    await flushPromises();
    await w.find('[data-test="pause-slave-sl-1"]').trigger('click');
    await flushPromises();
    await w.find('[data-test="delete-slave-sl-1"]').trigger('click');
    await flushPromises();

    expect(restartSpy).toHaveBeenCalledWith('sl-1');
    expect(pauseSpy).toHaveBeenCalledWith('sl-1');
    expect(openRemoteSpy).toHaveBeenCalledWith('sl-1');
    expect(confirmSpy).toHaveBeenCalledWith(expect.stringContaining('删除这台电脑上的本地配置和进程'));
    expect(deleteSpy).toHaveBeenCalledWith('sl-1');
    expect(getSlavesSpy).toHaveBeenCalledTimes(4);
  });

  it('opens the remote agentserver page before deleting a linked local slave', async () => {
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
    const getSlavesSpy = vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves({
      slaves: [{
        id: 'sl-1',
        name: 'worker',
        display_name: 'devbox-worker',
        folder: '/repo/app',
        status: 'running',
      }],
    }));
    const openRemoteSpy = vi.spyOn(api, 'openConsoleSlaveRemote').mockResolvedValue({
      state: 'opened',
      url: 'https://agent.cs.ac.cn/w/workspace-1/sandboxes/sandbox-1',
    });
    const deleteSpy = vi.spyOn(api, 'deleteConsoleSlave').mockResolvedValue({ state: 'deleted' });
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);

    const w = mount(Dashboard);
    await flushPromises();

    await w.find('[data-test="delete-slave-sl-1"]').trigger('click');
    await flushPromises();

    expect(openRemoteSpy).toHaveBeenCalledWith('sl-1');
    expect(deleteSpy).not.toHaveBeenCalled();
    expect(confirmSpy).not.toHaveBeenCalled();
    expect(w.text()).toContain('已打开 agentserver 页面');

    await w.find('[data-test="delete-slave-sl-1"]').trigger('click');
    await flushPromises();

    expect(confirmSpy).toHaveBeenCalledWith(expect.stringContaining('我已在 agentserver 网页删除远程记录'));
    expect(deleteSpy).toHaveBeenCalledWith('sl-1');
    expect(getSlavesSpy).toHaveBeenCalledTimes(2);
  });

  it('still allows local slave deletion when opening the remote page fails', async () => {
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
    const getSlavesSpy = vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves({
      slaves: [{
        id: 'sl-1',
        name: 'worker',
        display_name: 'devbox-worker',
        folder: '/repo/app',
        status: 'running',
      }],
    }));
    const openRemoteSpy = vi.spyOn(api, 'openConsoleSlaveRemote').mockRejectedValue(new Error('config unreadable'));
    const deleteSpy = vi.spyOn(api, 'deleteConsoleSlave').mockResolvedValue({ state: 'deleted' });
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);

    const w = mount(Dashboard);
    await flushPromises();

    await w.find('[data-test="delete-slave-sl-1"]').trigger('click');
    await flushPromises();

    expect(openRemoteSpy).toHaveBeenCalledWith('sl-1');
    expect(confirmSpy).toHaveBeenCalledWith(expect.stringContaining('远程记录可能需要手动清理'));
    expect(deleteSpy).toHaveBeenCalledWith('sl-1');
    expect(getSlavesSpy).toHaveBeenCalledTimes(2);
    expect(w.text()).not.toContain('config unreadable');
  });

  it('renders unknown local slave statuses as the raw status', async () => {
    vi.spyOn(api, 'getConsoleState').mockResolvedValue(consoleState());
    vi.spyOn(api, 'getConsoleSlaves').mockResolvedValue(consoleSlaves({
      slaves: [{
        id: 'sl-unknown',
        name: 'odd',
        display_name: 'devbox-odd',
        folder: '/repo/odd',
        status: 'warming' as api.ConsoleSlaveStatus,
      }],
    }));

    const w = mount(Dashboard);
    await flushPromises();

    expect(w.text()).toContain('warming');
  });
});
