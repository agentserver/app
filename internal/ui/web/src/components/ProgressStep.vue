<script setup lang="ts">
import { ref, shallowRef, onMounted, watch } from 'vue';
import type { StepInstance, OnboardingHandle } from '../composables/useOnboarding';
import * as api from '../api';
import { useSSE, type SSEHandle, type ProgressEvent } from '../composables/useSSE';
import ErrorPanel from './ErrorPanel.vue';

const props = defineProps<{
  step: StepInstance;
  onboarding: OnboardingHandle;
}>();

const sse = shallowRef<SSEHandle | null>(null);
const streamError = ref<string | undefined>();

async function start() {
  props.onboarding.markStepInProgress(props.step.id, '准备中…');
  streamError.value = undefined;
  try {
    const handle = await api.startFrontendInstall();
    sse.value = useSSE(handle.stream_id);
    // Watch incoming events; on stream end, refresh state to learn outcome
    watch(() => sse.value?.latest.value, (ev: ProgressEvent | undefined) => {
      if (ev) {
        renderEvent(ev);
        if (ev.stage === 'error') {
          streamError.value = ev.msg || '安装失败';
        }
      }
    });
    watch(() => sse.value?.done.value, async (d) => {
      if (d) {
        // Stream closed — refresh state and infer success/error from
        // completed_steps. The backend marks the mode-specific install token
        // only on success, so if it's NOT in completed_steps, treat as error.
        await props.onboarding.refreshState();
        const completed = props.onboarding.steps.value.find(s => s.id === props.step.id);
        if (completed?.runtime.status === 'success') {
          // syncFromServer already moved it to success; nothing else to do
        } else {
          props.onboarding.markStepError(
            props.step.id,
            '安装未完成',
            streamError.value || '请重试；如果仍失败，请查看 launcher 日志获取详情。',
          );
        }
      }
    });
  } catch (e) {
    const err = e as api.OnboardingError;
    props.onboarding.markStepError(props.step.id, err.message, err.detail);
  }
}

function renderEvent(ev: ProgressEvent) {
  const stage = ev.msg || ev.stage || '处理中…';
  const percent =
    ev.total && ev.downloaded != null
      ? Math.round((ev.downloaded / ev.total) * 100)
      : undefined;
  props.onboarding.updateProgress(props.step.id, { stage, percent });
}

function retry() {
  sse.value?.close();
  sse.value = null;
  start();
}

onMounted(() => {
  if (props.onboarding.shouldAutoAdvance(props.step)) {
    start();
  }
});
</script>

<template>
  <div v-if="step.runtime.status === 'active'">
    <el-button type="primary" @click="start">开始</el-button>
  </div>

  <div v-else-if="step.runtime.status === 'in_progress'" class="in-progress">
    <div class="stage-row">
      <el-icon class="is-loading"><Loading /></el-icon>
      <span>{{ step.runtime.stage }}</span>
    </div>
    <el-progress
      v-if="typeof step.runtime.percent === 'number'"
      :percentage="step.runtime.percent"
      :stroke-width="8"
    />
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
  flex-direction: column;
  gap: 8px;
  color: #606266;
}
.stage-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.is-loading {
  animation: rotate 1s linear infinite;
}
@keyframes rotate {
  to { transform: rotate(360deg); }
}
</style>
