<script setup lang="ts">
withDefaults(defineProps<{
  launching?: boolean;
  frontendName: string;
}>(), { launching: false });

const emit = defineEmits<{
  launch: [];
}>();
</script>

<template>
  <el-alert
    type="success"
    :closable="false"
    show-icon
    class="banner"
  >
    <template #title>
      <strong>全部完成!</strong>
    </template>
    <template #default>
      <p class="msg">
        配置已就绪。可双击桌面快捷方式启动，或者：
      </p>
      <div v-if="!launching" class="actions">
        <el-button type="primary" @click="emit('launch')">打开 {{ frontendName }}</el-button>
      </div>
      <div v-else class="launching">
        <el-icon class="is-loading"><Loading /></el-icon>
        <span>{{ frontendName }} 启动中，此窗口即将关闭…</span>
      </div>
    </template>
  </el-alert>
</template>

<style scoped>
.banner {
  margin-bottom: 24px;
}
.msg {
  margin: 8px 0;
}
.actions {
  display: flex;
  gap: 8px;
}
.launching {
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
