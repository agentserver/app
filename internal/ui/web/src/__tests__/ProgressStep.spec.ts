import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { computed, ref, nextTick } from 'vue';
import ProgressStep from '../components/ProgressStep.vue';
import type { OnboardingHandle } from '../composables/useOnboarding';
import * as api from '../api';

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  readyState = 1;

  constructor(public url: string) {
    FakeEventSource.instances.push(this);
  }

  emit(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }

  finish() {
    this.readyState = 2;
    this.onerror?.();
  }

  close() {
    this.readyState = 2;
  }
}

describe('ProgressStep', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  it('does not mark the step failed before the SSE stream closes', async () => {
    vi.spyOn(api, 'startFrontendInstall').mockResolvedValue({ stream_id: 's1' });
    const onboarding = makeOnboarding();

    mount(ProgressStep, {
      props: { step: makeStep(), onboarding },
      global: { stubs: { Loading: true } },
    });
    await nextTick();
    await Promise.resolve();
    await nextTick();

    expect(onboarding.markStepError).not.toHaveBeenCalled();
  });

  it('renders concrete backend error events instead of a generic launcher-log message', async () => {
    vi.spyOn(api, 'startFrontendInstall').mockResolvedValue({ stream_id: 's1' });
    const onboarding = makeOnboarding();

    mount(ProgressStep, {
      props: { step: makeStep(), onboarding },
      global: { stubs: { Loading: true } },
    });
    await nextTick();
    await Promise.resolve();
    const es = FakeEventSource.instances[0];

    es.emit({ stage: 'error', msg: 'download incomplete: got 3145728 bytes' });
    es.finish();
    await nextTick();
    await Promise.resolve();
    await nextTick();

    expect(onboarding.markStepError).toHaveBeenCalledWith(
      'vscode_install',
      '安装未完成',
      'download incomplete: got 3145728 bytes',
    );
  });
});

function makeStep() {
  return {
    id: 'vscode_install',
    label: '安装 VS Code',
    kind: 'progress' as const,
    autoStart: true,
    runtime: { status: 'active' as const },
  };
}

function makeOnboarding(): OnboardingHandle {
  return {
    steps: ref([
      { ...makeStep(), runtime: { status: 'in_progress' as const } },
    ]),
    current: computed(() => undefined),
    isComplete: computed(() => false),
    connectionError: ref(null),
    frontendMode: ref('codex_desktop'),
    frontendName: ref('Codex Desktop'),
    init: vi.fn(),
    refreshState: vi.fn(),
    markStepInProgress: vi.fn(),
    markStepSuccess: vi.fn(),
    markStepError: vi.fn(),
    updateProgress: vi.fn(),
    setOauthUrl: vi.fn(),
    shouldAutoAdvance: vi.fn(() => true),
  };
}
