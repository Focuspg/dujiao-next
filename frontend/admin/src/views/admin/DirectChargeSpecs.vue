<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AdminDirectChargeSpec, AdminDirectChargeSpecInput } from '@/api/types'
import TableSkeleton from '@/components/TableSkeleton.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogScrollContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { notifySuccess } from '@/utils/notify'

const { t } = useI18n()
const loading = ref(true)
const saving = ref(false)
const specs = ref<AdminDirectChargeSpec[]>([])
const showForm = ref(false)
const editingId = ref<number | null>(null)

const credentialModeOptions = ['session', 'access_token']

const emptyForm = () => ({
  product_id: '',
  sku_id: '',
  upstream_product: 'gpt',
  upstream_plan: '',
  payment_country: '',
  payment_currency: '',
  credential_modes: ['session'] as string[],
  allowed_current_plans: 'free',
  card_id: '',
  max_cost_usd_minor: '0',
  max_quote_minor: '0',
  concurrency_limit: '1',
  enabled: false,
  submit_paused: false,
})

const form = reactive(emptyForm())

const fetchSpecs = async () => {
  loading.value = true
  try {
    const res = await adminAPI.getDirectChargeSpecs()
    specs.value = (res.data.data as AdminDirectChargeSpec[]) || []
  } catch {
    specs.value = []
  } finally {
    loading.value = false
  }
}

const openCreate = () => {
  editingId.value = null
  Object.assign(form, emptyForm())
  showForm.value = true
}

const openEdit = (spec: AdminDirectChargeSpec) => {
  editingId.value = spec.id
  Object.assign(form, {
    product_id: String(spec.product_id),
    sku_id: String(spec.sku_id),
    upstream_product: spec.upstream_product,
    upstream_plan: spec.upstream_plan,
    payment_country: spec.payment_country,
    payment_currency: spec.payment_currency,
    credential_modes: [...(spec.credential_modes || [])],
    allowed_current_plans: (spec.allowed_current_plans || []).join(', '),
    card_id: String(spec.card_id),
    max_cost_usd_minor: String(spec.max_cost_usd_minor),
    max_quote_minor: String(spec.max_quote_minor),
    concurrency_limit: String(spec.concurrency_limit),
    enabled: spec.enabled,
    submit_paused: spec.submit_paused,
  })
  showForm.value = true
}

const toggleMode = (mode: string, checked: boolean | 'indeterminate') => {
  const set = new Set(form.credential_modes)
  if (checked === true) set.add(mode)
  else set.delete(mode)
  form.credential_modes = credentialModeOptions.filter((m) => set.has(m))
}

const toInt = (value: string) => {
  const n = Number.parseInt(String(value).trim(), 10)
  return Number.isFinite(n) ? n : 0
}

const buildPayload = (): AdminDirectChargeSpecInput => ({
  product_id: toInt(form.product_id),
  sku_id: toInt(form.sku_id),
  upstream_product: form.upstream_product.trim(),
  upstream_plan: form.upstream_plan.trim(),
  payment_country: form.payment_country.trim(),
  payment_currency: form.payment_currency.trim(),
  credential_modes: form.credential_modes,
  allowed_current_plans: form.allowed_current_plans.split(/[,，\s]+/).map((p) => p.trim()).filter(Boolean),
  card_id: toInt(form.card_id),
  max_cost_usd_minor: toInt(form.max_cost_usd_minor),
  max_quote_minor: toInt(form.max_quote_minor),
  concurrency_limit: toInt(form.concurrency_limit) || 1,
  enabled: form.enabled,
  submit_paused: form.submit_paused,
})

const save = async () => {
  saving.value = true
  try {
    const payload = buildPayload()
    if (editingId.value) {
      await adminAPI.updateDirectChargeSpec(editingId.value, payload)
    } else {
      await adminAPI.createDirectChargeSpec(payload)
    }
    notifySuccess(t('directCharge.specs.saved'))
    showForm.value = false
    fetchSpecs()
  } catch {
    // 错误已由请求层提示
  } finally {
    saving.value = false
  }
}

const quickToggle = async (spec: AdminDirectChargeSpec, field: 'enabled' | 'submit_paused', value: boolean) => {
  const { id, supplier: _supplier, version: _version, created_at: _created, updated_at: _updated, ...input } = spec
  try {
    await adminAPI.updateDirectChargeSpec(id, { ...input, [field]: value })
    notifySuccess(t('directCharge.specs.saved'))
  } catch {
    // 错误已由请求层提示
  } finally {
    fetchSpecs()
  }
}

onMounted(fetchSpecs)
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <h1 class="text-2xl font-semibold">{{ t('directCharge.specs.title') }}</h1>
        <p class="mt-1 text-sm text-muted-foreground">{{ t('directCharge.specs.subtitle') }}</p>
      </div>
      <div class="flex gap-2">
        <Button size="sm" variant="outline" class="w-full sm:w-auto" :disabled="loading" @click="fetchSpecs">
          {{ t('admin.common.refresh') }}
        </Button>
        <Button size="sm" class="w-full sm:w-auto" @click="openCreate">{{ t('directCharge.specs.create') }}</Button>
      </div>
    </div>

    <div class="overflow-x-auto rounded-xl border border-border bg-card">
      <TableSkeleton v-if="loading" :columns="7" :rows="3" />
      <div v-else-if="specs.length === 0" class="p-12 text-center text-muted-foreground">{{ t('directCharge.specs.empty') }}</div>
      <Table v-else>
        <TableHeader>
          <TableRow>
            <TableHead>ID</TableHead>
            <TableHead>{{ t('directCharge.specs.columns.sku') }}</TableHead>
            <TableHead>{{ t('directCharge.specs.columns.upstream') }}</TableHead>
            <TableHead>{{ t('directCharge.specs.columns.region') }}</TableHead>
            <TableHead>{{ t('directCharge.specs.columns.card') }}</TableHead>
            <TableHead>{{ t('directCharge.specs.columns.concurrency') }}</TableHead>
            <TableHead>{{ t('directCharge.specs.columns.enabled') }}</TableHead>
            <TableHead>{{ t('directCharge.specs.columns.submitPaused') }}</TableHead>
            <TableHead class="text-right">{{ t('directCharge.columns.actions') }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="spec in specs" :key="spec.id">
            <TableCell class="font-mono text-xs">#{{ spec.id }}<div class="text-muted-foreground">v{{ spec.version }}</div></TableCell>
            <TableCell class="text-xs">
              <div>{{ t('directCharge.specs.fields.productId') }}: <span class="font-mono">{{ spec.product_id }}</span></div>
              <div>{{ t('directCharge.specs.fields.skuId') }}: <span class="font-mono">{{ spec.sku_id }}</span></div>
            </TableCell>
            <TableCell class="font-mono text-xs">{{ spec.upstream_product }} / {{ spec.upstream_plan }}</TableCell>
            <TableCell class="font-mono text-xs">{{ spec.payment_country || '-' }} / {{ spec.payment_currency || '-' }}</TableCell>
            <TableCell class="font-mono text-xs">{{ spec.card_id }}</TableCell>
            <TableCell class="text-xs">{{ spec.concurrency_limit }}</TableCell>
            <TableCell>
              <Switch :model-value="spec.enabled" @update:model-value="(v: boolean) => quickToggle(spec, 'enabled', v)" />
            </TableCell>
            <TableCell>
              <Switch :model-value="spec.submit_paused" @update:model-value="(v: boolean) => quickToggle(spec, 'submit_paused', v)" />
            </TableCell>
            <TableCell class="text-right">
              <Button size="sm" variant="outline" class="h-7 text-xs" @click="openEdit(spec)">{{ t('admin.common.edit') }}</Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <Dialog v-model:open="showForm">
      <DialogScrollContent class="w-[calc(100vw-1rem)] max-w-2xl p-4 sm:p-6" @interact-outside="(e: Event) => e.preventDefault()">
        <DialogHeader>
          <DialogTitle>{{ editingId ? t('directCharge.specs.edit') : t('directCharge.specs.create') }}</DialogTitle>
        </DialogHeader>
        <form class="grid grid-cols-1 gap-4 sm:grid-cols-2" @submit.prevent="save">
          <div>
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.productId') }}</Label>
            <Input v-model="form.product_id" inputmode="numeric" required />
          </div>
          <div>
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.skuId') }}</Label>
            <Input v-model="form.sku_id" inputmode="numeric" required />
          </div>
          <div>
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.upstreamProduct') }}</Label>
            <Input v-model="form.upstream_product" required />
          </div>
          <div>
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.upstreamPlan') }}</Label>
            <Input v-model="form.upstream_plan" required />
          </div>
          <div>
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.paymentCountry') }}</Label>
            <Input v-model="form.payment_country" placeholder="PH" />
          </div>
          <div>
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.paymentCurrency') }}</Label>
            <Input v-model="form.payment_currency" placeholder="PHP" />
          </div>
          <div>
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.cardId') }}</Label>
            <Input v-model="form.card_id" inputmode="numeric" required />
          </div>
          <div>
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.concurrency') }}</Label>
            <Input v-model="form.concurrency_limit" inputmode="numeric" />
          </div>
          <div>
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.maxQuoteMinor') }}</Label>
            <Input v-model="form.max_quote_minor" inputmode="numeric" />
            <p class="mt-1 text-[11px] text-muted-foreground">{{ t('directCharge.specs.hints.minorUnits') }}</p>
          </div>
          <div>
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.maxCostUsdMinor') }}</Label>
            <Input v-model="form.max_cost_usd_minor" inputmode="numeric" />
            <p class="mt-1 text-[11px] text-muted-foreground">{{ t('directCharge.specs.hints.usdCents') }}</p>
          </div>
          <div class="sm:col-span-2">
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.allowedCurrentPlans') }}</Label>
            <Input v-model="form.allowed_current_plans" placeholder="free" />
            <p class="mt-1 text-[11px] text-muted-foreground">{{ t('directCharge.specs.hints.allowedCurrentPlans') }}</p>
          </div>
          <div class="sm:col-span-2">
            <Label class="mb-1.5 block text-xs">{{ t('directCharge.specs.fields.credentialModes') }}</Label>
            <div class="flex flex-wrap gap-4">
              <label v-for="mode in credentialModeOptions" :key="mode" class="flex items-center gap-2 text-sm">
                <Checkbox :model-value="form.credential_modes.includes(mode)" @update:model-value="(v: boolean | 'indeterminate') => toggleMode(mode, v)" />
                {{ t('directCharge.credentialModes.' + mode) }}
              </label>
            </div>
          </div>
          <label class="flex items-center justify-between gap-3 rounded-lg border border-border px-3 py-2 text-sm">
            {{ t('directCharge.specs.fields.enabled') }}
            <Switch v-model="form.enabled" />
          </label>
          <label class="flex items-center justify-between gap-3 rounded-lg border border-border px-3 py-2 text-sm">
            {{ t('directCharge.specs.fields.submitPaused') }}
            <Switch v-model="form.submit_paused" />
          </label>
          <p class="text-[11px] text-muted-foreground sm:col-span-2">{{ t('directCharge.specs.hints.versionBump') }}</p>
          <div class="flex justify-end gap-2 sm:col-span-2">
            <Button type="button" variant="outline" @click="showForm = false">{{ t('admin.common.cancel') }}</Button>
            <Button type="submit" :disabled="saving">{{ t('admin.common.save') }}</Button>
          </div>
        </form>
      </DialogScrollContent>
    </Dialog>
  </div>
</template>
