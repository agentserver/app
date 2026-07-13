import { describe, it, expect, vi, beforeEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { computed, reactive, ref } from 'vue';
import ActionStep from '../components/ActionStep.vue';
import type { OnboardingHandle, StepInstance } from '../composables/useOnboarding';
import * as api from '../api';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function finalizeStep() {
  return reactive<StepInstance>({
    id: 'finalize',
    label: '完成',
    kind: 'action',
    autoStart: false,
    runtime: { status: 'active' },
  });
}

function onboardingFor(step: StepInstance): OnboardingHandle {
  return {
    steps: ref([step]),
    current: computed(() => step),
    isComplete: computed(() => false),
    connectionError: ref(null),
    frontendMode: ref('codex_desktop'),
    frontendName: ref('ChatGPT / Codex'),
    init: vi.fn(),
    refreshState: vi.fn(),
    markStepInProgress: vi.fn((_id: string, stage?: string) => {
      step.runtime = { status: 'in_progress', stage };
    }),
    markStepSuccess: vi.fn(() => {
      step.runtime = { status: 'success' };
    }),
    markStepError: vi.fn((_id: string, message: string, detail?: string) => {
      step.runtime = { status: 'error', errorMessage: message, errorDetail: detail };
    }),
    updateProgress: vi.fn(),
    setOauthUrl: vi.fn(),
    shouldAutoAdvance: vi.fn(() => false),
  };
}

describe('ActionStep finalize', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('waits for launch before marking finalize successful or refreshing', async () => {
    const step = finalizeStep();
    const onboarding = onboardingFor(step);
    const launch = deferred<{ state: 'launching' }>();
    const order: string[] = [];
    vi.spyOn(api, 'finalize').mockImplementation(async () => {
      order.push('finalize');
      return { state: 'complete' };
    });
    vi.spyOn(api, 'launchFrontend').mockImplementation(() => {
      order.push('launch');
      return launch.promise;
    });

    const wrapper = mount(ActionStep, { props: { step, onboarding } });
    await wrapper.find('button').trigger('click');
    await flushPromises();

    expect(order).toEqual(['finalize', 'launch']);
    expect(onboarding.markStepSuccess).not.toHaveBeenCalled();
    expect(onboarding.refreshState).not.toHaveBeenCalled();

    launch.resolve({ state: 'launching' });
    await flushPromises();

    expect(onboarding.markStepSuccess).toHaveBeenCalledWith('finalize');
    expect(onboarding.refreshState).toHaveBeenCalledOnce();
  });

  it('renders the backend diagnosis and keeps finalize unsuccessful when launch fails', async () => {
    const diagnosis = 'ChatGPT / Codex 桌面应用本身无法启动；请依次尝试 Repair、Reset、Reinstall。';
    const step = finalizeStep();
    const onboarding = onboardingFor(step);
    vi.spyOn(api, 'finalize').mockResolvedValue({ state: 'complete' });
    vi.spyOn(api, 'launchFrontend').mockRejectedValue(new api.OnboardingError(diagnosis, 500, diagnosis));

    const wrapper = mount(ActionStep, { props: { step, onboarding } });
    await wrapper.find('button').trigger('click');
    await flushPromises();

    expect(api.finalize).toHaveBeenCalledOnce();
    expect(api.launchFrontend).toHaveBeenCalledOnce();
    expect(onboarding.markStepSuccess).not.toHaveBeenCalled();
    expect(onboarding.refreshState).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain(diagnosis);
  });
});
