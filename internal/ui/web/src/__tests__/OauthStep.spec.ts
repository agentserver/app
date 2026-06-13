import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import OauthStep from '../components/OauthStep.vue';
import * as api from '../api';
import type { OnboardingHandle, StepInstance } from '../composables/useOnboarding';

function step(overrides?: Partial<StepInstance>): StepInstance {
  return {
    id: 'agentserver_login',
    label: '星池登录',
    kind: 'oauth',
    autoStart: false,
    runtime: {
      status: 'active',
      stage: '',
      ...overrides?.runtime,
    },
    ...overrides,
  };
}

function onboarding() {
  return {
    markStepInProgress: vi.fn(),
    markStepError: vi.fn(),
    markStepSuccess: vi.fn(),
    setOauthUrl: vi.fn(),
    refreshState: vi.fn().mockResolvedValue(undefined),
  } as unknown as OnboardingHandle;
}

describe('OauthStep', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('continues polling after long-poll timeout status errors', async () => {
    const handle = onboarding();
    vi.spyOn(api, 'pollStepStatus')
      .mockResolvedValueOnce({ state: 'waiting', error: 'context deadline exceeded' })
      .mockResolvedValueOnce({ state: 'success' });

    mount(OauthStep, {
      props: {
        step: step({ runtime: { status: 'in_progress', stage: '请登录' } }),
        onboarding: handle,
      },
    });

    await vi.advanceTimersByTimeAsync(100);
    await flushPromises();
    await vi.advanceTimersByTimeAsync(3000);
    await flushPromises();

    expect(handle.markStepError).not.toHaveBeenCalled();
    expect(handle.markStepSuccess).toHaveBeenCalledWith('agentserver_login');
    expect(handle.refreshState).toHaveBeenCalledTimes(1);
  });

  it('does not render unsafe fallback oauth urls', async () => {
    const w = mount(OauthStep, {
      props: {
        step: step({
          runtime: {
            status: 'in_progress',
            stage: '请登录',
            oauthUrl: "javascript:fetch('/api/console/quit',{method:'POST'})",
          },
        }),
        onboarding: onboarding(),
      },
    });

    expect(w.find('a[href^="javascript:"]').exists()).toBe(false);
    expect(w.text()).not.toContain('浏览器没自动打开');
  });
});
