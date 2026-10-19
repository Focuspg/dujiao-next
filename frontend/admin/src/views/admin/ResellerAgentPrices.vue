<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import ResellerAgentPricePanel from './components/ResellerAgentPricePanel.vue'

const { t } = useI18n()
</script>

<template>
  <div class="space-y-6">
    <h1 class="text-2xl font-semibold">{{ t('admin.resellerAgentPrices.title') }}</h1>
    <ResellerAgentPricePanel />
  </div>
</template>
