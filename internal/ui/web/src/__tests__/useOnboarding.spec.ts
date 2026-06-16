import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useOnboarding } from '../composables/useOnboarding';
import * as api from '../api';

describe('useOnboarding', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('initializes all steps as pending and current=first', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1, install_id: 'x',
      onboarding_status: 'pending', completed_steps: null,
    });
    const o = useOnboarding();
    await o.init();
    expect(o.steps.value.map(s => s.runtime.status)).toEqual(['active', 'pending', 'pending', 'pending', 'pending']);
    expect(o.current.value?.id).toBe('modelserver_login');
  });

  it('syncs from completed_steps: MS done → AS active, rest pending', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1, install_id: 'x',
      onboarding_status: 'pending', completed_steps: ['modelserver_login'],
    });
    const o = useOnboarding();
    await o.init();
    expect(o.steps.value[0].runtime.status).toBe('success');
    expect(o.steps.value[1].runtime.status).toBe('active');
    expect(o.current.value?.id).toBe('agentserver_login');
  });

  it('when onboarding_status=complete, all steps success and current=null', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1, install_id: 'x',
      frontend_mode: 'minimal_vscode',
      onboarding_status: 'complete',
      completed_steps: ['modelserver_login', 'agentserver_login', 'vscode_installed', 'vscode_configured', 'shortcuts_created'],
    });
    const o = useOnboarding();
    await o.init();
    expect(o.steps.value.every(s => s.runtime.status === 'success')).toBe(true);
    expect(o.current.value).toBeUndefined();
    expect(o.isComplete.value).toBe(true);
  });

  it('init failure sets connectionError', async () => {
    vi.spyOn(api, 'getState').mockRejectedValue(new api.OnboardingError('boom'));
    const o = useOnboarding();
    await o.init();
    expect(o.connectionError.value).toContain('boom');
  });

  it('markStepError sets the runtime fields and switches status to error', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1, install_id: 'x',
      onboarding_status: 'pending', completed_steps: null,
    });
    const o = useOnboarding();
    await o.init();
    o.markStepError('modelserver_login', '登录失败', 'detail goes here');
    const step = o.steps.value[0];
    expect(step.runtime.status).toBe('error');
    expect(step.runtime.errorMessage).toBe('登录失败');
    expect(step.runtime.errorDetail).toBe('detail goes here');
  });

  it('markStepInProgress / markStepSuccess flow through statuses', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1, install_id: 'x',
      onboarding_status: 'pending', completed_steps: null,
    });
    const o = useOnboarding();
    await o.init();
    o.markStepInProgress('modelserver_login', '正在打开浏览器');
    expect(o.steps.value[0].runtime.status).toBe('in_progress');
    expect(o.steps.value[0].runtime.stage).toBe('正在打开浏览器');
    o.markStepSuccess('modelserver_login');
    expect(o.steps.value[0].runtime.status).toBe('success');
    // AS should now be the active one
    expect(o.steps.value[1].runtime.status).toBe('active');
  });

  it('updateProgress sets stage and percent', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1, install_id: 'x',
      frontend_mode: 'minimal_vscode',
      onboarding_status: 'pending', completed_steps: ['modelserver_login', 'agentserver_login'],
    });
    const o = useOnboarding();
    await o.init();
    o.markStepInProgress('vscode_install', '开始下载');
    o.updateProgress('vscode_install', { stage: '正在下载 (43%)', percent: 43 });
    expect(o.steps.value[2].runtime.stage).toBe('正在下载 (43%)');
    expect(o.steps.value[2].runtime.percent).toBe(43);
  });

  it('shouldAutoAdvance returns true for non-OAuth step after prev success', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1, install_id: 'x',
      frontend_mode: 'minimal_vscode',
      onboarding_status: 'pending',
      completed_steps: ['modelserver_login', 'agentserver_login'],
    });
    const o = useOnboarding();
    await o.init();
    // vscode_install is autoStart=true and current
    expect(o.current.value?.id).toBe('vscode_install');
    expect(o.shouldAutoAdvance(o.current.value!)).toBe(true);
  });

  it('shouldAutoAdvance returns false for OAuth step', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1, install_id: 'x',
      onboarding_status: 'pending', completed_steps: null,
    });
    const o = useOnboarding();
    await o.init();
    expect(o.shouldAutoAdvance(o.current.value!)).toBe(false);
  });

  it('shouldAutoAdvance returns false for finalize', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1, install_id: 'x',
      frontend_mode: 'minimal_vscode',
      onboarding_status: 'pending',
      completed_steps: ['modelserver_login', 'agentserver_login', 'vscode_installed', 'vscode_configured'],
    });
    const o = useOnboarding();
    await o.init();
    expect(o.current.value?.id).toBe('finalize');
    expect(o.shouldAutoAdvance(o.current.value!)).toBe(false);
  });

  it('initializes Codex Desktop steps from server mode', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1,
      install_id: 'x',
      frontend_mode: 'codex_desktop',
      frontend_name: 'Codex Desktop',
      onboarding_status: 'pending',
      completed_steps: ['modelserver_login', 'agentserver_login'],
    });
    const o = useOnboarding();
    await o.init();
    expect(o.steps.value.map(s => s.id)).toEqual([
      'modelserver_login',
      'agentserver_login',
      'codex_desktop_install',
      'codex_desktop_configure',
      'finalize',
    ]);
    expect(o.current.value?.id).toBe('codex_desktop_install');
    expect(o.frontendName.value).toBe('Codex Desktop');
  });

  it('initializes OpenCode Desktop steps from server mode', async () => {
    vi.spyOn(api, 'getState').mockResolvedValue({
      schema_version: 1,
      install_id: 'x',
      frontend_mode: 'opencode_desktop',
      onboarding_status: 'pending',
      completed_steps: ['modelserver_login', 'agentserver_login'],
    });
    const o = useOnboarding();
    await o.init();
    expect(o.steps.value.map(s => s.id)).toEqual([
      'modelserver_login',
      'agentserver_login',
      'opencode_desktop_install',
      'opencode_desktop_configure',
      'finalize',
    ]);
    expect(o.current.value?.id).toBe('opencode_desktop_install');
    expect(o.frontendName.value).toBe('OpenCode Desktop');
  });

  it('refreshState merges new completed_steps without resetting runtime', async () => {
    const getStateSpy = vi.spyOn(api, 'getState');
    getStateSpy.mockResolvedValueOnce({
      schema_version: 1, install_id: 'x',
      onboarding_status: 'pending', completed_steps: null,
    });
    const o = useOnboarding();
    await o.init();
    o.markStepError('modelserver_login', 'transient', '');

    getStateSpy.mockResolvedValueOnce({
      schema_version: 1, install_id: 'x',
      onboarding_status: 'pending', completed_steps: ['modelserver_login'],
    });
    await o.refreshState();
    // server says it's done — runtime status overridden to success
    expect(o.steps.value[0].runtime.status).toBe('success');
    expect(o.steps.value[0].runtime.errorMessage).toBeUndefined();
  });
});
