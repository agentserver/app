<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { useOnboarding } from './composables/useOnboarding';
import StepCard from './components/StepCard.vue';
import OauthStep from './components/OauthStep.vue';
import ActionStep from './components/ActionStep.vue';
import ProgressStep from './components/ProgressStep.vue';
import SuccessBanner from './components/SuccessBanner.vue';
import * as api from './api';

const onboarding = useOnboarding();
const launching = ref(false);

onMounted(async () => {
  await onboarding.init();
});

async function launchVSCode() {
  launching.value = true;
  try {
    await api.launchVSCode();
    // launcher will shut down its HTTP server ~500ms after responding.
    // Subsequent fetchState calls will fail; UI freezes in launching state.
  } catch (e) {
    launching.value = false;
    // Surface error somehow; for now use a connectionError-style banner
    onboarding.connectionError.value = '启动 VS Code 失败: ' + (e instanceof Error ? e.message : String(e));
  }
}
</script>

<template>
  <div class="container">
    <h1>agentserver-vscode 配置向导</h1>

    <el-alert
      v-if="onboarding.connectionError.value"
      type="error"
      :title="onboarding.connectionError.value"
      :closable="false"
      show-icon
      class="conn-error"
    />

    <SuccessBanner
      v-if="onboarding.isComplete.value"
      :launching="launching"
      @launch="launchVSCode"
    />

    <StepCard
      v-for="step in onboarding.steps.value"
      :key="step.id"
      :step="step"
    >
      <template #action>
        <OauthStep
          v-if="step.kind === 'oauth' && (step.runtime.status === 'active' || step.runtime.status === 'in_progress' || step.runtime.status === 'error')"
          :step="step"
          :onboarding="onboarding"
        />
        <ProgressStep
          v-else-if="step.kind === 'progress' && (step.runtime.status === 'active' || step.runtime.status === 'in_progress' || step.runtime.status === 'error')"
          :step="step"
          :onboarding="onboarding"
        />
        <ActionStep
          v-else-if="step.kind === 'action' && (step.runtime.status === 'active' || step.runtime.status === 'in_progress' || step.runtime.status === 'error')"
          :step="step"
          :onboarding="onboarding"
        />
      </template>
    </StepCard>
  </div>
</template>

<style scoped>
.conn-error {
  margin-bottom: 16px;
}
</style>
