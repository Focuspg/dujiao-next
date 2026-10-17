<template>
  <div v-if="order" class="space-y-4 rounded-2xl border bg-card p-5">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="text-base font-bold text-foreground">{{ t('directChargeOrder.title') }}</h2>
      <Badge :variant="directChargeStatusTone(order.status)">{{ statusLabel(order.status) }}</Badge>
    </div>
    <p class="text-sm text-muted-foreground">{{ t(`directChargeOrder.statusHint.${order.status}`) }}</p>
    <p v-if="order.action_message" class="rounded-lg border border-warning/40 bg-warning/10 p-3 text-sm text-warning">
      {{ order.action_message }}
    </p>
    <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
      <dt class="text-muted-foreground">{{ t('directChargeOrder.account') }}</dt>
      <dd class="font-mono text-foreground break-all">{{ order.account_masked }}</dd>
      <dt class="text-muted-foreground">{{ t('directChargeOrder.plan') }}</dt>
      <dd class="text-foreground">{{ order.plan }}</dd>
    </dl>
    <div v-if="order.timeline.length">
      <h3 class="mb-2 text-xs font-bold uppercase tracking-widest text-muted-foreground">{{ t('directChargeOrder.timeline') }}</h3>
      <ol class="space-y-1.5 text-sm">
        <li v-for="item in order.timeline" :key="`${item.status}-${item.at}`" class="flex justify-between gap-4">
          <span class="text-foreground">{{ statusLabel(item.status) }}</span>
          <span class="tabular-nums text-muted-foreground">{{ formatTime(item.at) }}</span>
        </li>
      </ol>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Badge } from '@/components/ui/badge'
import { directChargeAPI, type DirectChargeOrder } from '../../api/directCharge'
import { directChargeShouldPoll, directChargeStatusTone } from '../../utils/directCharge'

const props = defineProps<{
  orderNo: string
  // 订单状态变化（如付款完成）时重新读取进度
  orderStatus?: string
}>()

const POLL_INTERVAL_MS = 20000

const { t, locale } = useI18n()
const order = ref<DirectChargeOrder | null>(null)
let timer: number | undefined

const statusLabel = (status: string) => t(`directChargeOrder.status.${status}`)

const formatTime = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale.value)
}

const stopPolling = () => {
  if (timer !== undefined) {
    window.clearTimeout(timer)
    timer = undefined
  }
}

// 只读本地进度；不是代充订单时接口返回 404，组件不显示
const load = async () => {
  stopPolling()
  if (!props.orderNo) return
  try {
    const response = await directChargeAPI.order(props.orderNo)
    order.value = response.data.data as DirectChargeOrder
  } catch {
    order.value = null
    return
  }
  if (order.value && directChargeShouldPoll(order.value.status) && document.visibilityState !== 'hidden') {
    timer = window.setTimeout(load, POLL_INTERVAL_MS)
  }
}

watch(() => [props.orderNo, props.orderStatus], load, { immediate: true })
onUnmounted(stopPolling)
</script>
