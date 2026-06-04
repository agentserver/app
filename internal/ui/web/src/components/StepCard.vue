<script setup lang="ts">
import { computed } from 'vue';
import type { StepInstance } from '../composables/useOnboarding';

const props = defineProps<{ step: StepInstance }>();

const statusClass = computed(() => {
  switch (props.step.runtime.status) {
    case 'active':      return 'step--active';
    case 'in_progress': return 'step--in_progress';
    case 'success':     return 'step--done';
    case 'error':       return 'step--error';
    default:            return '';
  }
});

const icon = computed(() => {
  switch (props.step.runtime.status) {
    case 'success':     return '✓';
    case 'error':       return '✗';
    case 'in_progress': return '⏳';
    default:            return '';
  }
});
</script>

<template>
  <div class="step" :class="statusClass">
    <div class="step__head">
      <span class="step__label">{{ step.label }}</span>
      <span class="step__icon" v-if="icon">{{ icon }}</span>
    </div>
    <div class="step__body">
      <slot name="action" />
    </div>
  </div>
</template>

<style scoped>
.step {
  padding: 16px;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  margin: 8px 0;
  background: white;
}
.step--active {
  border-color: #409eff;
  background: #f0f7ff;
}
.step--in_progress {
  border-color: #409eff;
  background: #f0f7ff;
}
.step--done {
  border-color: #e5e7eb;
  color: #909399;
  background: #fafafa;
}
.step--error {
  border-color: #f56c6c;
  background: #fef0f0;
}
.step__head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: 600;
}
.step__icon {
  font-size: 18px;
}
.step__body {
  margin-top: 12px;
}
.step__body:empty {
  margin-top: 0;
}
.step--done .step__icon {
  color: #67c23a;
}
.step--error .step__icon {
  color: #f56c6c;
}
</style>
