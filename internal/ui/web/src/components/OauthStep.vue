<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import type { StepInstance, OnboardingHandle } from '../composables/useOnboarding';
import * as api from '../api';
import ErrorPanel from './ErrorPanel.vue';

const props = defineProps<{
  step: StepInstance;
  onboarding: OnboardingHandle;
}>();

const pollHandle = ref<number | null>(null);

async function start() {
  props.onboarding.markStepInProgress(props.step.id, '正在打开浏览器…');
  try {
    const r = await api.startStep(props.step.id);
    if (r.oauth_url) props.onboarding.setOauthUrl(props.step.id, r.oauth_url);
    props.onboarding.markStepInProgress(props.step.id, '请在弹出的浏览器中完成登录…');
    pollLoop();
  } catch (e) {
    const err = e as api.OnboardingError;
    props.onboarding.markStepError(props.step.id, err.message, err.detail);
  }
}

async function pollLoop() {
  if (pollHandle.value) return; // already polling
  const tick = async () => {
    try {
      const s = await api.pollStepStatus(props.step.id);
      if (s.state === 'success') {
        stopPoll();
        props.onboarding.markStepSuccess(props.step.id);
        await props.onboarding.refreshState();
        return;
      }
      if (s.error) {
        stopPoll();
        props.onboarding.markStepError(props.step.id, s.error);
        return;
      }
      // still waiting; reschedule
      pollHandle.value = window.setTimeout(tick, 3000);
    } catch (e) {
      stopPoll();
      const err = e as api.OnboardingError;
      props.onboarding.markStepError(props.step.id, err.message, err.detail);
    }
  };
  pollHandle.value = window.setTimeout(tick, 100);
}

function stopPoll() {
  if (pollHandle.value !== null) {
    clearTimeout(pollHandle.value);
    pollHandle.value = null;
  }
}

function retry() {
  stopPoll();
  start();
}

onUnmounted(stopPoll);

// If we're remounted in an in_progress state (refresh / navigation),
// resume polling without re-POSTing.
onMounted(() => {
  if (props.step.runtime.status === 'in_progress') {
    pollLoop();
  }
});
</script>

<template>
  <div v-if="step.runtime.status === 'active'">
    <el-button type="primary" @click="start">开始</el-button>
  </div>

  <div v-else-if="step.runtime.status === 'in_progress'" class="in-progress">
    <el-icon class="is-loading"><Loading /></el-icon>
    <span>{{ step.runtime.stage }}</span>
    <a
      v-if="step.runtime.oauthUrl"
      :href="step.runtime.oauthUrl"
      target="_blank"
      rel="noopener noreferrer"
      class="fallback"
    >
      浏览器没自动打开? 点这里
    </a>
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
.fallback {
  margin-left: 16px;
  font-size: 13px;
  color: #909399;
}
.is-loading {
  animation: rotate 1s linear infinite;
}
@keyframes rotate {
  to { transform: rotate(360deg); }
}
</style>
