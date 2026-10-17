<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn } from '@vueuse/core'
import { adminAPI } from '@/api/admin'
import type { AdminDirectChargeTask, AdminDirectChargeTaskDetail } from '@/api/types'
import { formatDate } from '@/utils/format'
import TableSkeleton from '@/components/TableSkeleton.vue'
import ListPagination from '@/components/ListPagination.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogScrollContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { notifySuccess } from '@/utils/notify'
import { useAdminAuthStore } from '@/stores/auth'

type ActionKind = 'retry' | 'resolveSucceeded' | 'resolveFailed' | 'refund'

const { t } = useI18n()
const authStore = useAdminAuthStore()
const loading = ref(true)
const tasks = ref<AdminDirectChargeTask[]>([])
const pagination = reactive({ page: 1, page_size: 20, total: 0, total_page: 1 })
const pageSizeOptions = [10, 20, 50, 100]
const filters = reactive({ status: '__all__', refund_status: '__all__', order_no: '' })

const statuses = [
  'awaiting_payment', 'ready', 'preflighting', 'waiting_credential', 'submitting', 'submit_unknown',
  'processing', 'requires_action', 'review', 'succeeded', 'failed_confirmed', 'cancelled_confirmed', 'closed',
]

const showDetail = ref(false)
const detail = ref<AdminDirectChargeTaskDetail | null>(null)
const detailLoading = ref(false)
const reconciling = ref(false)

const actionKind = ref<ActionKind | null>(null)
const actionText = ref('')
const actionSubmitting = ref(false)

const can = (method: string, path: string) => authStore.hasPermission(`${method}:${path}`)
const canReconcile = computed(() => can('POST', '/admin/direct-charge/tasks/:id/reconcile'))
const canRetry = computed(() => can('POST', '/admin/direct-charge/tasks/:id/retry'))
const canResolve = computed(() => can('POST', '/admin/direct-charge/tasks/:id/resolve'))
const canRefund = computed(() => can('POST', '/admin/direct-charge/tasks/:id/refund'))

const fetchTasks = async (page = 1) => {
  loading.value = true
  try {
    const params: Record<string, unknown> = { page, page_size: pagination.page_size }
    if (filters.status !== '__all__') params.status = filters.status
    if (filters.refund_status !== '__all__') params.refund_status = filters.refund_status
    if (filters.order_no.trim()) params.order_no = filters.order_no.trim()
    const res = await adminAPI.getDirectChargeTasks(params)
    tasks.value = (res.data.data as AdminDirectChargeTask[]) || []
    const p = res.data.pagination
    if (p) Object.assign(pagination, p)
  } catch {
    tasks.value = []
  } finally {
    loading.value = false
  }
}

const handleSearch = () => fetchTasks(1)
const debouncedSearch = useDebounceFn(handleSearch, 300)
const changePage = (page: number) => {
  if (page >= 1 && page <= pagination.total_page) fetchTasks(page)
}
const changePageSize = (size: number) => {
  pagination.page_size = size
  fetchTasks(1)
}

const loadDetail = async (id: number) => {
  detailLoading.value = true
  try {
    const res = await adminAPI.getDirectChargeTask(id)
    detail.value = res.data.data as AdminDirectChargeTaskDetail
  } catch {
    // 错误已由请求层提示
  } finally {
    detailLoading.value = false
  }
}

const openDetail = (task: AdminDirectChargeTask) => {
  detail.value = { task, attempts: [], logs: [], events: [] }
  showDetail.value = true
  loadDetail(task.id)
}

const afterAction = (next: AdminDirectChargeTaskDetail) => {
  detail.value = next
  fetchTasks(pagination.page)
}

const reconcile = async () => {
  if (!detail.value) return
  reconciling.value = true
  try {
    const res = await adminAPI.reconcileDirectChargeTask(detail.value.task.id)
    const result = res.data.data as { result: string; detail: AdminDirectChargeTaskDetail }
    notifySuccess(t('directCharge.reconcileResult.' + result.result))
    afterAction(result.detail)
  } catch {
    // 错误已由请求层提示
  } finally {
    reconciling.value = false
  }
}

const openAction = (kind: ActionKind) => {
  actionKind.value = kind
  actionText.value = ''
}

const submitAction = async () => {
  if (!detail.value || !actionKind.value || !actionText.value.trim()) return
  const id = detail.value.task.id
  const text = actionText.value.trim()
  actionSubmitting.value = true
  try {
    let res
    switch (actionKind.value) {
      case 'retry':
        res = await adminAPI.retryDirectChargeTask(id, { reason: text })
        break
      case 'resolveSucceeded':
        res = await adminAPI.resolveDirectChargeTask(id, { outcome: 'succeeded', reason: text })
        break
      case 'resolveFailed':
        res = await adminAPI.resolveDirectChargeTask(id, { outcome: 'failed', reason: text })
        break
      case 'refund':
        res = await adminAPI.refundDirectChargeTask(id, { remark: text })
        break
    }
    notifySuccess(t('directCharge.actions.done'))
    actionKind.value = null
    afterAction(res.data.data as AdminDirectChargeTaskDetail)
  } catch {
    // 错误已由请求层提示
  } finally {
    actionSubmitting.value = false
  }
}

const task = computed(() => detail.value?.task)
const showReconcile = computed(() => !!task.value && canReconcile.value &&
  ['processing', 'requires_action', 'submit_unknown', 'review'].includes(task.value.status))
const showRetry = computed(() => !!task.value && canRetry.value && task.value.status === 'review' && !task.value.refund_locked)
const showResolve = computed(() => !!task.value && canResolve.value && task.value.status === 'review')
const showRefund = computed(() => !!task.value && canRefund.value && task.value.refund_status !== 'refunded' &&
  ['failed_confirmed', 'cancelled_confirmed', 'review', 'waiting_credential', 'ready'].includes(task.value.status))

const formatQuote = (row: { quote_amount_minor: number; quote_exponent: number; quote_currency: string }) => {
  if (!row.quote_amount_minor) return '-'
  return `${(row.quote_amount_minor / 10 ** (row.quote_exponent ?? 2)).toFixed(row.quote_exponent ?? 2)} ${row.quote_currency}`
}

const formatMinor = (amount: number, currency: string, exponent = 2) => {
  if (!amount) return '-'
  return `${(amount / 10 ** exponent).toFixed(exponent)} ${currency}`
}

const statusClass = (status: string) => {
  switch (status) {
    case 'succeeded': return 'text-emerald-700 border-emerald-200 bg-emerald-50'
    case 'failed_confirmed': case 'cancelled_confirmed': return 'text-red-700 border-red-200 bg-red-50'
    case 'review': case 'submit_unknown': case 'requires_action': return 'text-orange-700 border-orange-200 bg-orange-50'
    case 'waiting_credential': return 'text-amber-700 border-amber-200 bg-amber-50'
    case 'processing': case 'submitting': case 'preflighting': case 'ready': return 'text-blue-700 border-blue-200 bg-blue-50'
    default: return 'text-muted-foreground border-border bg-muted/30'
  }
}

onMounted(() => fetchTasks(1))
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <h1 class="text-2xl font-semibold">{{ t('directCharge.tasks.title') }}</h1>
        <p class="mt-1 text-sm text-muted-foreground">{{ t('directCharge.tasks.subtitle') }}</p>
      </div>
      <Button size="sm" variant="outline" class="w-full sm:w-auto" :disabled="loading" @click="fetchTasks(pagination.page)">
        {{ t('admin.common.refresh') }}
      </Button>
    </div>

    <div class="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-end">
      <div class="w-full sm:w-auto">
        <label class="mb-1.5 block text-xs font-medium text-muted-foreground">{{ t('directCharge.columns.status') }}</label>
        <Select v-model="filters.status" @update:model-value="handleSearch">
          <SelectTrigger class="h-9 w-full sm:w-44"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="__all__">{{ t('directCharge.filters.allStatus') }}</SelectItem>
            <SelectItem v-for="s in statuses" :key="s" :value="s">{{ t('directCharge.status.' + s) }}</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div class="w-full sm:w-auto">
        <label class="mb-1.5 block text-xs font-medium text-muted-foreground">{{ t('directCharge.columns.refund') }}</label>
        <Select v-model="filters.refund_status" @update:model-value="handleSearch">
          <SelectTrigger class="h-9 w-full sm:w-36"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="__all__">{{ t('directCharge.filters.allRefund') }}</SelectItem>
            <SelectItem value="requested">{{ t('directCharge.refundStatus.requested') }}</SelectItem>
            <SelectItem value="refunded">{{ t('directCharge.refundStatus.refunded') }}</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div class="w-full sm:w-auto">
        <label class="mb-1.5 block text-xs font-medium text-muted-foreground">{{ t('directCharge.columns.orderNo') }}</label>
        <Input v-model="filters.order_no" class="h-9 w-full sm:w-52" :placeholder="t('directCharge.filters.orderNoPlaceholder')" @update:model-value="debouncedSearch" @keyup.enter="handleSearch" />
      </div>
    </div>

    <div class="space-y-3">
      <div v-if="loading" class="overflow-hidden rounded-xl border border-border bg-card">
        <TableSkeleton :columns="6" :rows="5" />
      </div>
      <div v-else-if="tasks.length === 0" class="rounded-xl border border-border bg-card p-12 text-center text-muted-foreground">
        {{ t('directCharge.tasks.empty') }}
      </div>
      <div
        v-for="row in tasks"
        :key="row.id"
        class="cursor-pointer rounded-xl border border-border bg-card p-4 transition-all hover:shadow-md"
        @click="openDetail(row)"
      >
        <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div class="flex flex-wrap items-center gap-2">
            <span class="inline-flex rounded-full border px-2 py-0.5 text-[11px] font-medium" :class="statusClass(row.status)">
              {{ t('directCharge.status.' + row.status) }}
            </span>
            <span v-if="row.refund_status" class="inline-flex rounded-full border border-purple-200 bg-purple-50 px-2 py-0.5 text-[11px] font-medium text-purple-700">
              {{ t('directCharge.refundStatus.' + row.refund_status) }}
            </span>
            <span class="font-mono text-sm font-semibold">{{ row.parent_order_no }}</span>
            <span class="font-mono text-xs text-muted-foreground">#{{ row.id }}</span>
          </div>
          <span class="text-xs text-muted-foreground">{{ formatDate(row.created_at) }}</span>
        </div>
        <div class="mt-3 grid grid-cols-2 gap-x-6 gap-y-1.5 text-xs md:grid-cols-5">
          <div><span class="text-muted-foreground">{{ t('directCharge.columns.account') }}</span><div class="mt-0.5 break-all">{{ row.account_masked }}</div></div>
          <div><span class="text-muted-foreground">{{ t('directCharge.columns.plan') }}</span><div class="mt-0.5 font-mono">{{ row.product }} / {{ row.plan }}</div></div>
          <div><span class="text-muted-foreground">{{ t('directCharge.columns.quote') }}</span><div class="mt-0.5">{{ formatQuote(row) }}</div></div>
          <div><span class="text-muted-foreground">{{ t('directCharge.columns.attempts') }}</span><div class="mt-0.5">{{ row.attempt_count }}</div></div>
          <div><span class="text-muted-foreground">{{ t('directCharge.columns.userId') }}</span><div class="mt-0.5 font-mono">{{ row.user_id }}</div></div>
        </div>
        <div v-if="row.error_message" class="mt-3 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 break-words">
          {{ row.error_message }}
        </div>
      </div>
    </div>

    <ListPagination
      :page="pagination.page"
      :total-page="pagination.total_page"
      :total="pagination.total"
      :page-size="pagination.page_size"
      :page-size-options="pageSizeOptions"
      @change-page="changePage"
      @change-page-size="changePageSize"
    />

    <Dialog v-model:open="showDetail">
      <DialogScrollContent class="w-[calc(100vw-1rem)] max-w-4xl p-4 sm:p-6" @open-auto-focus="(e: Event) => e.preventDefault()">
        <DialogHeader>
          <DialogTitle class="flex flex-wrap items-center gap-2">
            {{ t('directCharge.detail.title') }}
            <span v-if="task" class="font-mono text-sm text-muted-foreground">{{ task.parent_order_no }} · #{{ task.id }}</span>
          </DialogTitle>
        </DialogHeader>

        <div v-if="task" class="space-y-5">
          <div class="rounded-lg border px-4 py-3" :class="statusClass(task.status)">
            <div class="text-sm font-semibold">
              {{ t('directCharge.status.' + task.status) }}
              <span v-if="task.refund_status"> · {{ t('directCharge.refundStatus.' + task.refund_status) }}</span>
            </div>
            <div v-if="task.error_message" class="mt-0.5 break-words text-xs opacity-80">{{ task.error_message }}</div>
            <div v-if="task.refund_remark" class="mt-0.5 break-words text-xs opacity-80">{{ t('directCharge.detail.refundRemark') }}: {{ task.refund_remark }}</div>
          </div>

          <div class="flex flex-wrap gap-2">
            <Button v-if="showReconcile" size="sm" variant="outline" :disabled="reconciling" @click="reconcile">
              {{ reconciling ? t('admin.common.loading') : t('directCharge.actions.reconcile') }}
            </Button>
            <Button v-if="showRetry" size="sm" variant="outline" @click="openAction('retry')">{{ t('directCharge.actions.retry') }}</Button>
            <Button v-if="showResolve" size="sm" variant="outline" @click="openAction('resolveSucceeded')">{{ t('directCharge.actions.resolveSucceeded') }}</Button>
            <Button v-if="showResolve" size="sm" variant="outline" @click="openAction('resolveFailed')">{{ t('directCharge.actions.resolveFailed') }}</Button>
            <Button v-if="showRefund" size="sm" variant="destructive" @click="openAction('refund')">{{ t('directCharge.actions.refund') }}</Button>
          </div>

          <div v-if="actionKind" class="space-y-2 rounded-lg border border-border p-3">
            <div class="text-sm font-medium">{{ t('directCharge.actions.' + actionKind) }}</div>
            <p class="text-xs text-muted-foreground">{{ t('directCharge.actionHints.' + actionKind) }}</p>
            <Textarea v-model="actionText" rows="3" :placeholder="t(actionKind === 'refund' ? 'directCharge.actions.remarkPlaceholder' : 'directCharge.actions.reasonPlaceholder')" />
            <div class="flex justify-end gap-2">
              <Button size="sm" variant="outline" @click="actionKind = null">{{ t('admin.common.cancel') }}</Button>
              <Button size="sm" :disabled="actionSubmitting || !actionText.trim()" @click="submitAction">{{ t('admin.common.confirm') }}</Button>
            </div>
          </div>

          <div class="grid grid-cols-2 gap-x-6 gap-y-2 text-xs md:grid-cols-4">
            <div><span class="text-muted-foreground">{{ t('directCharge.columns.childOrderNo') }}</span><div class="mt-0.5 font-mono">{{ task.order_no }}</div></div>
            <div><span class="text-muted-foreground">{{ t('directCharge.columns.account') }}</span><div class="mt-0.5 break-all">{{ task.account_masked }}</div></div>
            <div><span class="text-muted-foreground">{{ t('directCharge.columns.plan') }}</span><div class="mt-0.5 font-mono">{{ task.product }} / {{ task.plan }}</div></div>
            <div><span class="text-muted-foreground">{{ t('directCharge.columns.quote') }}</span><div class="mt-0.5">{{ formatQuote(task) }}</div></div>
            <div><span class="text-muted-foreground">{{ t('directCharge.detail.paidAt') }}</span><div class="mt-0.5">{{ formatDate(task.paid_at) || '-' }}</div></div>
            <div><span class="text-muted-foreground">{{ t('directCharge.detail.submittedAt') }}</span><div class="mt-0.5">{{ formatDate(task.submitted_at) || '-' }}</div></div>
            <div><span class="text-muted-foreground">{{ t('directCharge.detail.completedAt') }}</span><div class="mt-0.5">{{ formatDate(task.completed_at) || '-' }}</div></div>
            <div><span class="text-muted-foreground">{{ t('directCharge.detail.nextCheckAt') }}</span><div class="mt-0.5">{{ formatDate(task.next_check_at) || '-' }}</div></div>
          </div>

          <div>
            <h3 class="mb-2 text-sm font-semibold">{{ t('directCharge.detail.attempts') }}</h3>
            <div v-if="detailLoading" class="text-xs text-muted-foreground">{{ t('admin.common.loading') }}</div>
            <div v-else-if="!detail?.attempts.length" class="text-xs text-muted-foreground">{{ t('directCharge.detail.noAttempts') }}</div>
            <div v-for="attempt in detail?.attempts" :key="attempt.id" class="mb-2 rounded-lg border border-border p-3 text-xs">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-semibold">#{{ attempt.attempt_no }}</span>
                <span class="rounded border px-1.5 py-0.5">{{ t('directCharge.attemptStatus.' + attempt.status) }}</span>
                <span class="font-mono text-muted-foreground">{{ attempt.client_request_id }}</span>
              </div>
              <div class="mt-2 grid grid-cols-2 gap-x-6 gap-y-1 md:grid-cols-4">
                <div><span class="text-muted-foreground">{{ t('directCharge.detail.upstreamOrderId') }}</span><div class="font-mono">{{ attempt.upstream_order_id || '-' }}</div></div>
                <div><span class="text-muted-foreground">{{ t('directCharge.detail.upstreamStatus') }}</span><div class="font-mono">{{ attempt.upstream_status || '-' }}</div></div>
                <div><span class="text-muted-foreground">{{ t('directCharge.detail.finalAmount') }}</span><div>{{ formatMinor(attempt.final_amount_minor, attempt.currency) }}</div></div>
                <div><span class="text-muted-foreground">{{ t('directCharge.detail.serviceFee') }}</span><div>{{ formatMinor(attempt.service_fee_minor, 'USD') }}</div></div>
              </div>
              <div v-if="attempt.error_kind || attempt.upstream_message" class="mt-1 break-words text-muted-foreground">
                {{ attempt.error_kind }} {{ attempt.error_message }} {{ attempt.upstream_message }}
              </div>
            </div>
          </div>

          <div>
            <h3 class="mb-2 text-sm font-semibold">{{ t('directCharge.detail.logs') }}</h3>
            <div class="space-y-1">
              <div v-for="log in detail?.logs" :key="log.id" class="flex flex-col gap-0.5 border-l-2 border-border pl-3 text-xs sm:flex-row sm:gap-3">
                <span class="shrink-0 text-muted-foreground">{{ formatDate(log.created_at) }}</span>
                <span class="shrink-0 font-mono">{{ log.from_status || '-' }} → {{ log.to_status }}</span>
                <span class="shrink-0 text-muted-foreground">{{ log.actor }}</span>
                <span class="break-words">{{ log.reason }}</span>
              </div>
            </div>
          </div>

          <div v-if="detail?.events.length">
            <h3 class="mb-2 text-sm font-semibold">{{ t('directCharge.detail.events') }}</h3>
            <div v-for="event in detail?.events" :key="event.id" class="text-xs">
              <span class="text-muted-foreground">{{ formatDate(event.received_at) }}</span>
              <span class="ml-2 font-mono">{{ event.event_type }} · {{ event.upstream_status }} · {{ event.status }}</span>
            </div>
          </div>
        </div>
      </DialogScrollContent>
    </Dialog>
  </div>
</template>
