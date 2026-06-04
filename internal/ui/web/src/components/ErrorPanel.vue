<script setup lang="ts">
import { ref } from 'vue';

defineProps<{
  message: string;
  detail?: string;
}>();

const emit = defineEmits<{
  retry: [];
}>();

const expanded = ref(false);
</script>

<template>
  <el-alert
    type="error"
    :closable="false"
    show-icon
  >
    <template #title>
      {{ message }}
    </template>
    <template #default>
      <div class="error-actions">
        <el-button size="small" type="primary" @click="emit('retry')">重试</el-button>
        <el-button
          v-if="detail"
          size="small"
          link
          @click="expanded = !expanded"
        >
          {{ expanded ? '收起详情' : '查看详情' }}
        </el-button>
      </div>
      <pre v-if="expanded && detail" class="error-detail">{{ detail }}</pre>
    </template>
  </el-alert>
</template>

<style scoped>
.error-actions {
  margin-top: 8px;
  display: flex;
  gap: 8px;
}
.error-detail {
  margin-top: 8px;
  padding: 8px;
  background: rgba(0,0,0,0.04);
  border-radius: 4px;
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 200px;
  overflow: auto;
}
</style>
