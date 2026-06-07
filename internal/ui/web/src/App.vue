<script setup lang="ts">
import { onMounted } from 'vue';
import { useOnboarding } from './composables/useOnboarding';
import Dashboard from './components/Dashboard.vue';
import StepCard from './components/StepCard.vue';
import OauthStep from './components/OauthStep.vue';
import ActionStep from './components/ActionStep.vue';
import ProgressStep from './components/ProgressStep.vue';

const onboarding = useOnboarding();

onMounted(async () => {
  await onboarding.init();
});
</script>

<template>
  <div class="container">
    <Dashboard v-if="onboarding.isComplete.value" />

    <template v-else>
      <h1>星池指挥官配置向导</h1>

      <el-alert
        v-if="onboarding.connectionError.value"
        type="error"
        :title="onboarding.connectionError.value"
        :closable="false"
        show-icon
        class="conn-error"
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
    </template>
  </div>
</template>

<style scoped>
.conn-error {
  margin-bottom: 16px;
}
</style>
