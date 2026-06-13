<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { FolderOpened } from '@element-plus/icons-vue';
import * as api from '../api';
import QuotaCard from './QuotaCard.vue';

const state = ref<api.ConsoleState | null>(null);
const updateState = ref<api.ConsoleUpdateState | null>(null);
const statusError = ref('');
const updateError = ref('');
const frontendError = ref('');
const subscriptionError = ref('');
const logoutModelserverError = ref('');
const refreshing = ref(false);
const checkingUpdate = ref(false);
const installingUpdate = ref(false);
const opening = ref(false);
const openingSubscription = ref(false);
const loggingOutModelserver = ref(false);
const reconnectingModelserver = ref(false);
const reconnectStatus = ref('');
const reconnectOauthUrl = ref('');
const reconnectError = ref('');
const reconnectingAgentserver = ref(false);
const agentserverReconnectStatus = ref('');
const agentserverReconnectOauthUrl = ref('');
const agentserverReconnectError = ref('');
const slaveMachine = ref<api.ConsoleMachine | null>(null);
const slaves = ref<api.ConsoleSlave[]>([]);
const slaveFolder = ref('');
const slaveName = ref('');
const slaveError = ref('');
const slaveNotice = ref('');
const creatingSlave = ref(false);
const selectingSlaveFolder = ref(false);
const slaveBusy = ref<Record<string, boolean>>({});
const slaveRemoteDeleteOpened = ref<Record<string, boolean>>({});
let updateLoadSeq = 0;
let slaveLoadSeq = 0;
const slavePollIntervalMs = 3000;
let slavePollTimer: number | undefined;
let dashboardMounted = false;

const visibleErrors = computed(() => [
  { key: 'status', message: statusError.value },
  { key: 'update', message: updateError.value },
  { key: 'frontend', message: frontendError.value },
  { key: 'subscription', message: subscriptionError.value },
  { key: 'logout-modelserver', message: logoutModelserverError.value },
  { key: 'reconnect', message: reconnectError.value },
  { key: 'agentserver-reconnect', message: agentserverReconnectError.value },
  { key: 'slave', message: slaveError.value },
].filter(error => error.message));

const workspaceDisplayName = computed(() => {
  const workspace = state.value?.agentserver;
  if (workspace?.workspace_name) return workspace.workspace_name;
  if (workspace?.workspace_id) return `工作空间 ${shortId(workspace.workspace_id)}`;
  return '未读取到工作空间';
});

const machineDisplayName = computed(() => {
  const machine = slaveMachine.value;
  return machine?.computer_name || '未初始化';
});

const slaveDisplayPreview = computed(() => {
  const name = normalizedSlaveName() || '文件夹名';
  return `${machineDisplayName.value}-${name}`;
});

const updateStatusText = computed(() => {
  const update = updateState.value;
  if (!update) return '正在读取更新状态';
  if (update.status === 'available' && update.update) return `发现新版本 ${update.update.version}`;
  if (update.status === 'latest') return '已是最新版本';
  if (update.status === 'checking') return '正在检查更新';
  if (update.status === 'downloading') return '正在下载更新';
  if (update.status === 'installer_started') return '安装程序已启动';
  if (update.status === 'error') return update.last_error || '更新检查失败';
  return '未检查更新';
});

const updateBusy = computed(() => checkingUpdate.value || installingUpdate.value);
const updateButtonDisabled = computed(() => updateBusy.value);
const updateInstallAvailable = computed(() => {
  const update = updateState.value;
  return !!update?.update && (update.status === 'available' || update.status === 'error');
});
const updateDetailError = computed(() => {
  const update = updateState.value;
  if (!update?.last_error) return '';
  return update.status === 'error' ? '' : update.last_error;
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

async function loadUpdate() {
  const seq = ++updateLoadSeq;
  try {
    const update = await api.getConsoleUpdate();
    if (!dashboardMounted) return;
    if (seq !== updateLoadSeq) return;
    updateState.value = update;
    updateError.value = '';
  } catch (e) {
    if (!dashboardMounted) return;
    if (seq !== updateLoadSeq) return;
    updateError.value = errorMessage(e);
  }
}

async function loadSlaves() {
  const seq = ++slaveLoadSeq;
  try {
    const res = await api.getConsoleSlaves();
    if (!dashboardMounted) return;
    if (seq !== slaveLoadSeq) return;
    slaveMachine.value = res.machine;
    slaves.value = res.slaves || [];
    slaveError.value = '';
    syncSlavePolling();
  } catch (e) {
    if (!dashboardMounted) return;
    if (seq !== slaveLoadSeq) return;
    slaveError.value = errorMessage(e);
    syncSlavePolling();
  }
}

async function checkUpdate() {
  if (updateBusy.value) return;
  const seq = ++updateLoadSeq;
  checkingUpdate.value = true;
  try {
    const update = await api.checkConsoleUpdate();
    if (!dashboardMounted) return;
    if (seq !== updateLoadSeq) return;
    updateState.value = update;
    updateError.value = '';
  } catch (e) {
    if (!dashboardMounted) return;
    if (seq !== updateLoadSeq) return;
    updateError.value = errorMessage(e);
  } finally {
    if (dashboardMounted) checkingUpdate.value = false;
  }
}

async function installUpdate() {
  if (updateBusy.value || !updateInstallAvailable.value || !updateState.value?.update) return;
  const version = updateState.value.update.version;
  const confirmed = window.confirm(`安装星池指挥官更新 ${version}？安装程序启动后可能需要按提示完成更新。`);
  if (!confirmed) return;
  const seq = ++updateLoadSeq;
  installingUpdate.value = true;
  try {
    const update = await api.installConsoleUpdate();
    if (!dashboardMounted) return;
    if (seq !== updateLoadSeq) return;
    updateState.value = update;
    updateError.value = '';
  } catch (e) {
    if (!dashboardMounted) return;
    if (seq !== updateLoadSeq) return;
    const message = errorMessage(e);
    updateError.value = message;
    await loadUpdate();
    if (!dashboardMounted) return;
    updateError.value = message;
  } finally {
    if (dashboardMounted) installingUpdate.value = false;
  }
}

function slaveNeedsPolling(sl: api.ConsoleSlave) {
  return sl.status === 'starting' || sl.status === 'auth_required';
}

function clearSlavePolling() {
  if (slavePollTimer !== undefined) {
    window.clearTimeout(slavePollTimer);
    slavePollTimer = undefined;
  }
}

function syncSlavePolling() {
  clearSlavePolling();
  if (!dashboardMounted) return;
  if (!slaves.value.some(slaveNeedsPolling)) return;
  slavePollTimer = window.setTimeout(() => {
    slavePollTimer = undefined;
    void loadSlaves();
  }, slavePollIntervalMs);
}

async function refresh() {
  if (refreshing.value) return;
  refreshing.value = true;
  try {
    state.value = await api.refreshConsoleState();
    statusError.value = '';
    await loadSlaves();
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

async function reconnectAgentserver() {
  if (reconnectingAgentserver.value) return;
  reconnectingAgentserver.value = true;
  agentserverReconnectError.value = '';
  agentserverReconnectOauthUrl.value = '';
  agentserverReconnectStatus.value = '正在打开登录页面…';
  try {
    const started = await api.startStep('agentserver_login');
    if (started.oauth_url) agentserverReconnectOauthUrl.value = started.oauth_url;
    agentserverReconnectStatus.value = '请在浏览器中完成星池工作区连接…';
    await pollAgentserverReconnect();
  } catch (e) {
    agentserverReconnectError.value = errorMessage(e);
  } finally {
    reconnectingAgentserver.value = false;
  }
}

async function pollAgentserverReconnect() {
  for (;;) {
    const s = await api.pollStepStatus('agentserver_login');
    if (s.state === 'success') {
      state.value = await api.refreshConsoleState();
      agentserverReconnectStatus.value = '';
      agentserverReconnectOauthUrl.value = '';
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

function safeExternalUrl(raw?: string) {
  if (!raw) return '';
  try {
    const u = new URL(raw);
    return u.protocol === 'http:' || u.protocol === 'https:' ? raw : '';
  } catch {
    return '';
  }
}

function delay(ms: number) {
  return new Promise(resolve => window.setTimeout(resolve, ms));
}

function normalizedSlaveName() {
  const explicit = slaveName.value.trim();
  if (explicit) return explicit;
  const normalizedFolder = slaveFolder.value.trim().replace(/\\/g, '/').replace(/\/+$/, '');
  return normalizedFolder.split('/').pop() || '';
}

async function createSlave() {
  if (creatingSlave.value) return;
  const folder = slaveFolder.value.trim();
  const name = normalizedSlaveName();
  if (!folder) {
    slaveError.value = '请选择文件夹';
    return;
  }
  if (Array.from(name).length > 20) {
    slaveError.value = '名称最多 20 个字符';
    return;
  }

  creatingSlave.value = true;
  slaveNotice.value = '';
  try {
    await api.createConsoleSlave({ folder, name });
    slaveFolder.value = '';
    slaveName.value = '';
    await loadSlaves();
  } catch (e) {
    slaveError.value = errorMessage(e);
  } finally {
    creatingSlave.value = false;
  }
}

async function selectSlaveFolder() {
  if (selectingSlaveFolder.value) return;
  selectingSlaveFolder.value = true;
  slaveNotice.value = '';
  try {
    const selected = await api.selectConsoleSlaveFolder();
    if (selected.folder) {
      slaveFolder.value = selected.folder;
    }
    slaveError.value = '';
  } catch (e) {
    slaveError.value = errorMessage(e);
  } finally {
    selectingSlaveFolder.value = false;
  }
}

async function restartSlave(id: string) {
  slaveNotice.value = '';
  await runSlaveAction(id, () => api.restartConsoleSlave(id));
}

async function pauseSlave(id: string) {
  slaveNotice.value = '';
  await runSlaveAction(id, () => api.pauseConsoleSlave(id));
}

async function deleteSlave(id: string) {
  let remoteOpenFailed = false;
  if (!slaveRemoteDeleteOpened.value[id]) {
    if (slaveBusy.value[id]) return;
    slaveBusy.value = { ...slaveBusy.value, [id]: true };
    slaveNotice.value = '';
    try {
      const remote = await api.openConsoleSlaveRemote(id);
      if (remote.state === 'opened') {
        slaveRemoteDeleteOpened.value = { ...slaveRemoteDeleteOpened.value, [id]: true };
        slaveNotice.value = '已打开 agentserver 页面。请先在网页中删除远程记录，完成后再次点击删除来清理本机配置和进程。';
        slaveError.value = '';
        return;
      }
    } catch (e) {
      remoteOpenFailed = true;
      slaveError.value = '';
      slaveNotice.value = `未能自动打开 agentserver 页面：${errorMessage(e)}。远程记录可能需要手动清理。`;
    } finally {
      slaveBusy.value = { ...slaveBusy.value, [id]: false };
    }
  }

  const confirmed = window.confirm(deleteSlaveConfirmMessage(id, remoteOpenFailed));
  if (!confirmed) return;
  await runSlaveAction(id, async () => {
    await api.deleteConsoleSlave(id);
    clearSlaveRemoteDeleteOpened(id);
    slaveNotice.value = '';
  });
}

function deleteSlaveConfirmMessage(id: string, remoteOpenFailed: boolean) {
  if (slaveRemoteDeleteOpened.value[id]) {
    return '我已在 agentserver 网页删除远程记录，现在删除这台电脑上的本地配置和进程。确定继续吗？';
  }
  if (remoteOpenFailed) {
    return '未能自动打开 agentserver 页面，远程记录可能需要手动清理。现在删除这台电脑上的本地配置和进程。确定继续吗？';
  }
  return '删除这台电脑上的本地配置和进程。确定删除吗？';
}

async function runSlaveAction(id: string, action: () => Promise<unknown>) {
  if (slaveBusy.value[id]) return;
  slaveBusy.value = { ...slaveBusy.value, [id]: true };
  try {
    await action();
    await loadSlaves();
  } catch (e) {
    slaveError.value = errorMessage(e);
  } finally {
    slaveBusy.value = { ...slaveBusy.value, [id]: false };
  }
}

function clearSlaveRemoteDeleteOpened(id: string) {
  const next = { ...slaveRemoteDeleteOpened.value };
  delete next[id];
  slaveRemoteDeleteOpened.value = next;
}

function slaveStatusLabel(status: api.ConsoleSlaveStatus | string) {
  const labels: Record<api.ConsoleSlaveStatus, string> = {
    stopped: '已停止',
    starting: '启动中',
    auth_required: '待认证',
    running: '运行中',
    paused: '已暂停',
    error: '出错',
  };
  return labels[status as api.ConsoleSlaveStatus] || status;
}

onMounted(() => {
  dashboardMounted = true;
  void load();
  void loadUpdate();
  void loadSlaves();
});

onBeforeUnmount(() => {
  dashboardMounted = false;
  clearSlavePolling();
});
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
    <el-alert v-if="slaveNotice" type="info" :title="slaveNotice" :closable="false" show-icon />
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
        v-if="safeExternalUrl(reconnectOauthUrl)"
        :href="safeExternalUrl(reconnectOauthUrl)"
        target="_blank"
        rel="noopener noreferrer"
      >
        浏览器没自动打开? 点这里
      </a>
    </div>
    <el-alert
      v-if="state?.agentserver.reconnect_required"
      type="warning"
      :title="state.agentserver.auth_message || '星池工作区连接已失效，请重新连接。'"
      :closable="false"
      show-icon
    />

    <div v-if="state?.agentserver.reconnect_required" class="reconnect-row">
      <el-button
        type="primary"
        :loading="reconnectingAgentserver"
        :disabled="reconnectingAgentserver"
        @click="reconnectAgentserver"
      >
        重新连接星池工作区
      </el-button>
      <span v-if="agentserverReconnectStatus">{{ agentserverReconnectStatus }}</span>
      <a
        v-if="safeExternalUrl(agentserverReconnectOauthUrl)"
        :href="safeExternalUrl(agentserverReconnectOauthUrl)"
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

    <section class="update-panel">
      <div class="section-head">
        <h2>星池指挥官更新</h2>
        <p>
          <span v-if="updateState">当前版本 {{ updateState.current_version }}</span>
          <span v-else>正在读取当前版本</span>
          <span v-if="updateState?.last_checked_at">上次检查 {{ updateState.last_checked_at }}</span>
        </p>
      </div>
      <div class="update-row">
        <div class="update-summary">
          <strong>{{ updateStatusText }}</strong>
          <span v-if="updateState?.update?.notes">{{ updateState.update.notes }}</span>
          <span v-if="updateDetailError">{{ updateDetailError }}</span>
        </div>
        <div class="update-actions">
          <el-button
            data-test="check-console-update"
            :loading="checkingUpdate"
            :disabled="updateButtonDisabled"
            @click="checkUpdate"
          >
            检查更新
          </el-button>
          <el-button
            v-if="updateInstallAvailable"
            data-test="install-console-update"
            type="primary"
            :loading="installingUpdate"
            :disabled="updateButtonDisabled"
            @click="installUpdate"
          >
            安装更新
          </el-button>
        </div>
      </div>
    </section>

    <section class="slave-panel">
      <div class="section-head">
        <h2>允许被远程控制的文件夹（智能体形式提供）</h2>
        <p>本机：{{ machineDisplayName }}</p>
      </div>

      <div class="slave-create">
        <div class="folder-select">
          <el-input
            v-model="slaveFolder"
            data-test="slave-folder-input"
            placeholder="请选择文件夹"
            readonly
            clearable
          />
          <el-button
            data-test="select-slave-folder"
            :icon="FolderOpened"
            :loading="selectingSlaveFolder"
            :disabled="selectingSlaveFolder"
            @click="selectSlaveFolder"
          >
            选择文件夹
          </el-button>
        </div>
        <el-input
          v-model="slaveName"
          data-test="slave-name-input"
          maxlength="20"
          show-word-limit
          placeholder="名称，默认使用文件夹名"
          clearable
        />
        <span class="slave-preview">预览：{{ slaveDisplayPreview }}</span>
        <el-button
          data-test="create-slave"
          type="primary"
          :loading="creatingSlave"
          :disabled="creatingSlave"
          @click="createSlave"
        >
          创建并启动
        </el-button>
      </div>

      <div class="slave-list">
        <div v-for="sl in slaves" :key="sl.id" class="slave-row">
          <div class="slave-main">
            <div class="slave-title-line">
              <strong>{{ sl.display_name }}</strong>
              <span class="slave-status">{{ slaveStatusLabel(sl.status) }}</span>
            </div>
            <span class="slave-folder">{{ sl.folder }}</span>
            <a
              v-if="sl.status === 'auth_required' && safeExternalUrl(sl.auth_url)"
              :href="safeExternalUrl(sl.auth_url)"
              target="_blank"
              rel="noopener noreferrer"
            >
              完成认证
            </a>
            <em v-if="sl.last_error">{{ sl.last_error }}</em>
          </div>
          <div class="slave-actions">
            <el-button
              :data-test="`restart-slave-${sl.id}`"
              :loading="slaveBusy[sl.id]"
              :disabled="slaveBusy[sl.id]"
              @click="restartSlave(sl.id)"
            >
              启动/重启
            </el-button>
            <el-button
              :data-test="`pause-slave-${sl.id}`"
              :loading="slaveBusy[sl.id]"
              :disabled="slaveBusy[sl.id]"
              @click="pauseSlave(sl.id)"
            >
              暂停
            </el-button>
            <el-button
              :data-test="`delete-slave-${sl.id}`"
              type="danger"
              plain
              :loading="slaveBusy[sl.id]"
              :disabled="slaveBusy[sl.id]"
              @click="deleteSlave(sl.id)"
            >
              删除
            </el-button>
          </div>
        </div>
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

.update-panel,
.slave-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 0;
  border-top: 1px solid #e5e7eb;
  border-bottom: 1px solid #e5e7eb;
}

.section-head h2 {
  margin: 0 0 4px;
  font-size: 16px;
  line-height: 1.35;
}

.section-head p {
  margin: 0;
  color: #606266;
  font-size: 13px;
  overflow-wrap: anywhere;
}

.section-head p span + span {
  margin-left: 12px;
}

.update-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 12px;
  align-items: center;
}

.update-summary {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.update-summary strong,
.update-summary span {
  overflow-wrap: anywhere;
}

.update-summary span {
  color: #606266;
  font-size: 13px;
}

.update-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.slave-create {
  display: grid;
  grid-template-columns: minmax(300px, 2fr) minmax(180px, 1fr) minmax(180px, auto) auto;
  align-items: center;
  gap: 10px;
}

.folder-select {
  min-width: 0;
  display: flex;
  gap: 8px;
}

.folder-select :deep(.el-input) {
  min-width: 0;
}

.slave-preview {
  min-width: 0;
  color: #606266;
  font-size: 13px;
  overflow-wrap: anywhere;
}

.slave-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.slave-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 12px;
  align-items: center;
  padding: 10px 0;
  border-top: 1px solid #f0f2f5;
}

.slave-row:first-child {
  border-top: 0;
}

.slave-main {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.slave-title-line {
  min-width: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.slave-title-line strong,
.slave-folder,
.slave-main em {
  overflow-wrap: anywhere;
}

.slave-status {
  flex: 0 0 auto;
  color: #409eff;
  font-size: 12px;
}

.slave-folder,
.slave-main a,
.slave-main em {
  font-size: 13px;
}

.slave-folder {
  color: #606266;
}

.slave-main em {
  color: #c45656;
  font-style: normal;
}

.slave-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.slave-actions :deep(.el-button),
.slave-create :deep(.el-button),
.update-actions :deep(.el-button) {
  margin-left: 0;
  white-space: nowrap;
}

@media (max-width: 560px) {
  .dashboard-head {
    flex-direction: column;
  }

  .dashboard-actions {
    justify-content: flex-start;
    width: 100%;
  }

  .slave-create,
  .slave-row,
  .update-row {
    grid-template-columns: 1fr;
  }

  .folder-select {
    flex-direction: column;
  }

  .slave-actions,
  .update-actions {
    justify-content: flex-start;
  }
}
</style>
