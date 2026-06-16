import { ref, computed, type Ref, type ComputedRef } from 'vue';
import { stepsForMode, completedMapForMode, normalizeMode, type StepDef, type FrontendMode } from '../stepConfig';
import * as api from '../api';

export type StepStatus = 'pending' | 'active' | 'in_progress' | 'success' | 'error';

export interface StepRuntime {
  status: StepStatus;
  errorMessage?: string;
  errorDetail?: string;
  stage?: string;
  percent?: number;
  oauthUrl?: string;
}

export interface StepInstance {
  id: string;
  label: string;
  kind: StepDef['kind'];
  autoStart: boolean;
  runtime: StepRuntime;
}

export interface OnboardingHandle {
  steps: Ref<StepInstance[]>;
  current: ComputedRef<StepInstance | undefined>;
  isComplete: ComputedRef<boolean>;
  connectionError: Ref<string | null>;
  frontendMode: Ref<FrontendMode>;
  frontendName: Ref<string>;

  init(): Promise<void>;
  refreshState(): Promise<void>;

  markStepInProgress(id: string, stage?: string): void;
  markStepSuccess(id: string): void;
  markStepError(id: string, message: string, detail?: string): void;
  updateProgress(id: string, p: { stage?: string; percent?: number }): void;
  setOauthUrl(id: string, url: string): void;
  shouldAutoAdvance(step: StepInstance): boolean;
}

export function useOnboarding(): OnboardingHandle {
  const frontendMode = ref<FrontendMode>('codex_desktop');
  const frontendName = ref('Codex Desktop');
  const steps: Ref<StepInstance[]> = ref(
    stepsForMode(frontendMode.value).map(s => ({ ...s, runtime: { status: 'pending' as StepStatus } })),
  );
  const connectionError = ref<string | null>(null);
  const completed = ref<Set<string>>(new Set());
  const onboardingStatus = ref<'pending' | 'in_progress' | 'complete'>('pending');

  const current = computed(() =>
    steps.value.find(s => s.runtime.status !== 'success'),
  );

  const isComplete = computed(() => onboardingStatus.value === 'complete');

  function findStep(id: string): StepInstance | undefined {
    return steps.value.find(s => s.id === id);
  }

  function setFrontend(modeInput?: string | null, nameInput?: string) {
    const nextMode = normalizeMode(modeInput);
    const nextDefs = stepsForMode(nextMode);
    const currentIds = steps.value.map(s => s.id).join(',');
    const nextIds = nextDefs.map(s => s.id).join(',');
    frontendMode.value = nextMode;
    frontendName.value = nameInput || (nextMode === 'minimal_vscode'
      ? '极简界面'
      : nextMode === 'opencode_desktop'
        ? 'OpenCode Desktop'
        : 'Codex Desktop');
    if (currentIds !== nextIds) {
      steps.value = nextDefs.map(s => ({ ...s, runtime: { status: 'pending' as StepStatus } }));
    }
  }

  function syncFromServer() {
    const completedIds = new Set<string>();
    const completedMap = completedMapForMode(frontendMode.value);
    for (const token of Array.from(completed.value)) {
      const id = completedMap[token];
      if (id) completedIds.add(id);
    }
    // When onboarding complete, force all to success
    if (onboardingStatus.value === 'complete') {
      for (const step of steps.value) step.runtime = { status: 'success' };
      return;
    }
    let foundActive = false;
    for (const step of steps.value) {
      if (completedIds.has(step.id)) {
        step.runtime = { status: 'success' };
      } else if (!foundActive && step.runtime.status !== 'in_progress' && step.runtime.status !== 'error') {
        step.runtime = { status: 'active' };
        foundActive = true;
      } else if (step.runtime.status === 'success' && !completedIds.has(step.id)) {
        // Server walked back? Defensive.
        step.runtime = foundActive ? { status: 'pending' } : { status: 'active' };
        if (!foundActive) foundActive = true;
      }
    }
  }

  async function refreshState() {
    try {
      const s = await api.getState();
      connectionError.value = null;
      setFrontend(s.frontend_mode, s.frontend_name);
      completed.value = new Set(s.completed_steps ?? []);
      onboardingStatus.value = s.onboarding_status;
      syncFromServer();
    } catch (e) {
      connectionError.value = e instanceof Error ? e.message : String(e);
    }
  }

  async function init() {
    await refreshState();
  }

  function markStepInProgress(id: string, stage?: string) {
    const s = findStep(id);
    if (!s) return;
    s.runtime = { ...s.runtime, status: 'in_progress', stage, errorMessage: undefined, errorDetail: undefined };
  }

  function markStepSuccess(id: string) {
    const s = findStep(id);
    if (!s) return;
    s.runtime = { status: 'success' };
    // Activate next pending step
    const idx = steps.value.findIndex(x => x.id === id);
    if (idx >= 0 && idx + 1 < steps.value.length) {
      const next = steps.value[idx + 1];
      if (next.runtime.status === 'pending') {
        next.runtime = { status: 'active' };
      }
    }
  }

  function markStepError(id: string, message: string, detail?: string) {
    const s = findStep(id);
    if (!s) return;
    s.runtime = { ...s.runtime, status: 'error', errorMessage: message, errorDetail: detail };
  }

  function updateProgress(id: string, p: { stage?: string; percent?: number }) {
    const s = findStep(id);
    if (!s) return;
    s.runtime = { ...s.runtime, ...p };
  }

  function setOauthUrl(id: string, url: string) {
    const s = findStep(id);
    if (!s) return;
    s.runtime = { ...s.runtime, oauthUrl: url };
  }

  function shouldAutoAdvance(step: StepInstance): boolean {
    return step.autoStart && step.runtime.status === 'active';
  }

  return {
    steps, current, isComplete, connectionError, frontendMode, frontendName,
    init, refreshState,
    markStepInProgress, markStepSuccess, markStepError, updateProgress, setOauthUrl,
    shouldAutoAdvance,
  };
}
