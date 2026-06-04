<script setup lang="ts">
import { ref, onMounted } from 'vue';

const status = ref<string>('加载中…');

onMounted(async () => {
  try {
    const r = await fetch('/api/state');
    const s = await r.json();
    status.value = '已连接: onboarding_status=' + s.onboarding_status;
  } catch (e) {
    status.value = '加载失败: ' + (e as Error).message;
  }
});
</script>

<template>
  <div class="container">
    <h1>agentserver-vscode 配置向导</h1>
    <p>{{ status }}</p>
  </div>
</template>
