<script setup lang="ts">
import type { ConsoleQuota } from '../api';

defineProps<{ quota: ConsoleQuota }>();

function label(window: string) {
  if (window === '5h') return '5小时额度';
  if (window === '7d') return '7天额度';
  return `${window} 额度`;
}
</script>

<template>
  <section class="quota-card">
    <div class="quota-head">
      <strong>{{ label(quota.window) }}</strong>
      <span>已用 {{ quota.percentage }}%</span>
    </div>
    <el-progress :percentage="quota.percentage" :stroke-width="10" />
    <div class="quota-meta">
      <span>剩余约 {{ quota.remaining_percentage }}%</span>
      <span v-if="quota.resets_at">重置 {{ new Date(quota.resets_at).toLocaleString() }}</span>
    </div>
  </section>
</template>

<style scoped>
.quota-card {
  min-width: 0;
  padding: 16px;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  background: #fff;
}

.quota-head,
.quota-meta {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}

.quota-head {
  align-items: center;
  margin-bottom: 12px;
}

.quota-head strong {
  min-width: 0;
  overflow-wrap: anywhere;
  font-size: 15px;
}

.quota-head span,
.quota-meta {
  color: #606266;
  font-size: 13px;
}

.quota-meta {
  flex-wrap: wrap;
  margin-top: 10px;
}
</style>
