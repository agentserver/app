<script setup lang="ts">
import { onMounted } from 'vue';
import type { StepInstance, OnboardingHandle } from '../composables/useOnboarding';
import * as api from '../api';
import ErrorPanel from './ErrorPanel.vue';

const props = defineProps<{
  step: StepInstance;
  onboarding: OnboardingHandle;
}>();

async function run() {
  props.onboarding.markStepInProgress(props.step.id, '处理中…');
  try {
    if (props.step.id === 'vscode_configure') {
      await api.configureVSCode();
    } else if (props.step.id === 'finalize') {
      await api.finalize();
    } else {
      throw new api.OnboardingError(`ActionStep doesn't know step ${props.step.id}`);
    }
    props.onboarding.markStepSuccess(props.step.id);
    await props.onboarding.refreshState();
  } catch (e) {
    const err = e as api.OnboardingError;
    props.onboarding.markStepError(props.step.id, err.message, err.detail);
  }
}

function retry() { run(); }

onMounted(() => {
  if (props.onboarding.shouldAutoAdvance(props.step)) {
    run();
  }
});
</script>

<template>
  <div v-if="step.runtime.status === 'active'">
    <el-button type="primary" @click="run">{{ step.id === 'finalize' ? '完成' : '开始' }}</el-button>
  </div>

  <div v-else-if="step.runtime.status === 'in_progress'" class="in-progress">
    <el-icon class="is-loading"><Loading /></el-icon>
    <span>{{ step.runtime.stage }}</span>
  </div>

  <ErrorPanel
    v-else-if="step.runtime.status === 'error'"
    :message="step.runtime.errorMessage || '未知错误'"
    :detail="step.runtime.errorDetail"
    @retry="retry"
  />
</template>

<style scoped>
.in-progress {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #606266;
}
.is-loading {
  animation: rotate 1s linear infinite;
}
@keyframes rotate {
  to { transform: rotate(360deg); }
}
</style>
