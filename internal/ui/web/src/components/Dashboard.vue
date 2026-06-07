<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import * as api from '../api';
import QuotaCard from './QuotaCard.vue';

const state = ref<api.ConsoleState | null>(null);
const statusError = ref('');
const frontendError = ref('');
const subscriptionError = ref('');
const refreshing = ref(false);
const opening = ref(false);
const openingSubscription = ref(false);

const visibleErrors = computed(() => [
  { key: 'status', message: statusError.value },
  { key: 'frontend', message: frontendError.value },
  { key: 'subscription', message: subscriptionError.value },
].filter(error => error.message));

function errorMessage(e: unknown) {
  return e instanceof Error ? e.message : String(e);
}

async function load() {
  try {
    state.value = await api.getConsoleState();
    statusError.value = '';
  } catch (e) {
    statusError.value = errorMessage(e);
  }
}

async function refresh() {
  if (refreshing.value) return;
  refreshing.value = true;
  try {
    state.value = await api.refreshConsoleState();
    statusError.value = '';
  } catch (e) {
    statusError.value = errorMessage(e);
  } finally {
    refreshing.value = false;
  }
}

async function openFrontend() {
  if (opening.value) return;
  opening.value = true;
  try {
    await api.openConsoleFrontend();
    frontendError.value = '';
  } catch (e) {
    frontendError.value = errorMessage(e);
  } finally {
    opening.value = false;
  }
}

async function openSubscription() {
  if (openingSubscription.value || !state.value?.subscription_url) return;
  openingSubscription.value = true;
  try {
    await api.openConsoleSubscription();
    subscriptionError.value = '';
  } catch (e) {
    subscriptionError.value = errorMessage(e);
  } finally {
    openingSubscription.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div class="dashboard">
    <header class="dashboard-head">
      <div>
        <h1>星池指挥官</h1>
        <p>{{ state?.frontend_name || '正在读取状态' }}</p>
      </div>
      <div class="dashboard-actions">
        <el-button :loading="refreshing" :disabled="refreshing" @click="refresh">刷新状态</el-button>
        <el-button type="primary" :loading="opening" :disabled="opening" @click="openFrontend">
          打开 {{ state?.frontend_name || '前端' }}
        </el-button>
      </div>
    </header>

    <el-alert
      v-for="error in visibleErrors"
      :key="error.key"
      type="error"
      :title="error.message"
      :closable="false"
      show-icon
    />
    <el-alert v-if="state?.quota_error" type="warning" :title="state.quota_error" :closable="false" show-icon />

    <section class="quota-grid">
      <QuotaCard v-for="q in state?.quotas || []" :key="q.window" :quota="q" />
    </section>

    <section class="connection-grid">
      <div class="info-block">
        <span>modelserver 项目</span>
        <strong>{{ state?.modelserver.project_name || state?.modelserver.project_id || '未读取到项目' }}</strong>
      </div>
      <div class="info-block">
        <span>agentserver 工作空间</span>
        <strong>{{ state?.agentserver.workspace_name || state?.agentserver.workspace_id || '未读取到工作空间' }}</strong>
      </div>
    </section>

    <el-button
      :loading="openingSubscription"
      :disabled="openingSubscription || !state?.subscription_url"
      @click="openSubscription"
    >
      打开订阅页
    </el-button>
  </div>
</template>

<style scoped>
.dashboard {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.dashboard-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.dashboard-head h1 {
  margin: 0 0 6px;
}

.dashboard-head p {
  margin: 0;
  color: #606266;
}

.dashboard-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.dashboard-actions :deep(.el-button) {
  margin-left: 0;
}

.quota-grid,
.connection-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px;
}

.info-block {
  min-width: 0;
  padding: 14px 16px;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  background: #fff;
}

.info-block span {
  display: block;
  margin-bottom: 6px;
  color: #606266;
  font-size: 13px;
}

.info-block strong {
  display: block;
  overflow-wrap: anywhere;
  font-size: 15px;
}

@media (max-width: 560px) {
  .dashboard-head {
    flex-direction: column;
  }

  .dashboard-actions {
    justify-content: flex-start;
    width: 100%;
  }
}
</style>
