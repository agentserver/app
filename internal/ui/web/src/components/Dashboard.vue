<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import * as api from '../api';
import QuotaCard from './QuotaCard.vue';

const state = ref<api.ConsoleState | null>(null);
const statusError = ref('');
const frontendError = ref('');
const subscriptionError = ref('');
const logoutModelserverError = ref('');
const refreshing = ref(false);
const opening = ref(false);
const openingSubscription = ref(false);
const loggingOutModelserver = ref(false);
const reconnectingModelserver = ref(false);
const reconnectStatus = ref('');
const reconnectOauthUrl = ref('');
const reconnectError = ref('');

const visibleErrors = computed(() => [
  { key: 'status', message: statusError.value },
  { key: 'frontend', message: frontendError.value },
  { key: 'subscription', message: subscriptionError.value },
  { key: 'logout-modelserver', message: logoutModelserverError.value },
  { key: 'reconnect', message: reconnectError.value },
].filter(error => error.message));

const workspaceDisplayName = computed(() => {
  const workspace = state.value?.agentserver;
  if (workspace?.workspace_name) return workspace.workspace_name;
  if (workspace?.workspace_id) return `工作空间 ${shortId(workspace.workspace_id)}`;
  return '未读取到工作空间';
});

function shortId(id: string) {
  return id.length <= 8 ? id : id.slice(-8);
}

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

async function logoutModelserver() {
  if (loggingOutModelserver.value || !state.value) return;
  const confirmed = window.confirm('退出大模型登录后需要重新连接大模型。确定退出大模型登录吗？');
  if (!confirmed) return;
  loggingOutModelserver.value = true;
  try {
    await api.logoutConsoleModelserver();
    state.value = await api.refreshConsoleState();
    logoutModelserverError.value = '';
  } catch (e) {
    logoutModelserverError.value = errorMessage(e);
  } finally {
    loggingOutModelserver.value = false;
  }
}

async function reconnectModelserver() {
  if (reconnectingModelserver.value) return;
  reconnectingModelserver.value = true;
  reconnectError.value = '';
  reconnectOauthUrl.value = '';
  reconnectStatus.value = '正在打开登录页面…';
  try {
    const started = await api.startStep('modelserver_login');
    if (started.oauth_url) reconnectOauthUrl.value = started.oauth_url;
    reconnectStatus.value = '请在浏览器中完成大模型连接…';
    await pollModelserverReconnect();
  } catch (e) {
    reconnectError.value = errorMessage(e);
  } finally {
    reconnectingModelserver.value = false;
  }
}

async function pollModelserverReconnect() {
  for (;;) {
    const s = await api.pollStepStatus('modelserver_login');
    if (s.state === 'success') {
      state.value = await api.refreshConsoleState();
      reconnectStatus.value = '';
      reconnectOauthUrl.value = '';
      return;
    }
    if (s.error && !isLongPollTimeout(s.error)) {
      throw new Error(s.error);
    }
    await delay(3000);
  }
}

function isLongPollTimeout(message: string) {
  return message.includes('context deadline exceeded') || message.includes('deadline exceeded');
}

function delay(ms: number) {
  return new Promise(resolve => window.setTimeout(resolve, ms));
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
    <el-alert
      v-if="state?.modelserver.reconnect_required"
      type="warning"
      :title="state.modelserver.auth_message || '大模型连接已失效，请重新连接。'"
      :closable="false"
      show-icon
    />

    <div v-if="state?.modelserver.reconnect_required" class="reconnect-row">
      <el-button
        type="primary"
        :loading="reconnectingModelserver"
        :disabled="reconnectingModelserver"
        @click="reconnectModelserver"
      >
        重新连接大模型
      </el-button>
      <span v-if="reconnectStatus">{{ reconnectStatus }}</span>
      <a
        v-if="reconnectOauthUrl"
        :href="reconnectOauthUrl"
        target="_blank"
        rel="noopener noreferrer"
      >
        浏览器没自动打开? 点这里
      </a>
    </div>

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
        <strong>{{ workspaceDisplayName }}</strong>
      </div>
    </section>

    <div class="subscription-actions">
      <el-button
        :loading="openingSubscription"
        :disabled="openingSubscription || !state?.subscription_url"
        @click="openSubscription"
      >
        打开订阅页
      </el-button>
      <el-button
        type="danger"
        plain
        :loading="loggingOutModelserver"
        :disabled="loggingOutModelserver || !state"
        @click="logoutModelserver"
      >
        退出大模型登录
      </el-button>
    </div>
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

.subscription-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.subscription-actions :deep(.el-button) {
  margin-left: 0;
}

.reconnect-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.reconnect-row span,
.reconnect-row a {
  font-size: 13px;
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
