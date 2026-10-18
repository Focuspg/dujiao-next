<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn } from '@vueuse/core'
import { adminAPI } from '@/api/admin'
import type { AdminResellerAgentPriceProduct, AdminResellerAgentPriceSKU } from '@/api/types'
import ListPagination from '@/components/ListPagination.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { getLocalizedText } from '@/utils/format'
import { notifyError, notifySuccess } from '@/utils/notify'

// 不传 resellerId 时编辑统一代理价（对所有分销商生效）
const props = defineProps<{ resellerId?: number }>()

const isDefaultScope = computed(() => !props.resellerId)

const { t } = useI18n()

const loading = ref(false)
const saving = ref(false)
const keyword = ref('')
const products = ref<AdminResellerAgentPriceProduct[]>([])
// 以 sku_id 为键的编辑值；saved 为加载时的值，用来只提交改动过的行
const drafts = reactive<Record<number, string>>({})
const saved = reactive<Record<number, string>>({})
const pagination = ref({ page: 1, page_size: 20, total: 0, total_page: 1 })

const resetDrafts = () => {
  Object.keys(drafts).forEach((key) => delete drafts[Number(key)])
  Object.keys(saved).forEach((key) => delete saved[Number(key)])
  products.value.forEach((product) => {
    product.skus.forEach((sku) => {
      drafts[sku.id] = sku.agent_price_amount || ''
      saved[sku.id] = sku.agent_price_amount || ''
    })
  })
}

const fetchRows = async (page = 1) => {
  loading.value = true
  try {
    const params = {
      page,
      page_size: pagination.value.page_size,
      keyword: keyword.value.trim() || undefined,
    }
    const response = props.resellerId
      ? await adminAPI.getResellerAgentPrices(props.resellerId, params)
      : await adminAPI.getDefaultResellerAgentPrices(params)
    products.value = (response.data.data || []) as AdminResellerAgentPriceProduct[]
    pagination.value = response.data.pagination || pagination.value
    resetDrafts()
  } catch (err: any) {
    notifyError(err?.message || t('admin.resellerProfileDetail.agentPrices.loadFailed'))
  } finally {
    loading.value = false
  }
}

const debouncedSearch = useDebounceFn(() => fetchRows(1), 300)

const changePage = (page: number) => {
  if (page < 1 || page > pagination.value.total_page) return
  fetchRows(page)
}

const changePageSize = (size: number) => {
  if (size === pagination.value.page_size) return
  pagination.value.page_size = size
  fetchRows(1)
}

const skuLabel = (sku: AdminResellerAgentPriceSKU) => {
  const specs = Object.values(sku.spec_values || {})
    .map((value) => String(value).trim())
    .filter(Boolean)
    .join(' / ')
  return specs || sku.sku_code || `#${sku.id}`
}

const draftInvalid = (sku: AdminResellerAgentPriceSKU) => {
  const raw = (drafts[sku.id] || '').trim()
  if (!raw) return false
  const value = Number(raw)
  return !Number.isFinite(value) || value <= 0 || value > Number(sku.price_amount)
}

const hasDefaultPrice = (sku: AdminResellerAgentPriceSKU) => Number(sku.default_agent_price_amount) > 0

const placeholderFor = (sku: AdminResellerAgentPriceSKU) => {
  if (isDefaultScope.value) return t('admin.resellerProfileDetail.agentPrices.defaultPlaceholder')
  if (hasDefaultPrice(sku)) return t('admin.resellerProfileDetail.agentPrices.placeholderDefault', { price: sku.default_agent_price_amount })
  return t('admin.resellerProfileDetail.agentPrices.placeholder')
}

const save = async () => {
  const items: Array<{ product_id: number; sku_id: number; agent_price_amount: string }> = []
  for (const product of products.value) {
    for (const sku of product.skus) {
      const value = (drafts[sku.id] || '').trim()
      if (value === (saved[sku.id] || '').trim()) continue
      if (draftInvalid(sku)) {
        notifyError(t('admin.resellerProfileDetail.agentPrices.invalid', { sku: skuLabel(sku) }))
        return
      }
      items.push({ product_id: product.id, sku_id: sku.id, agent_price_amount: value })
    }
  }
  if (!items.length) {
    notifySuccess(t('admin.resellerProfileDetail.agentPrices.noChanges'))
    return
  }
  saving.value = true
  try {
    if (props.resellerId) {
      await adminAPI.updateResellerAgentPrices(props.resellerId, { items })
    } else {
      await adminAPI.updateDefaultResellerAgentPrices({ items })
    }
    notifySuccess(t('admin.resellerProfileDetail.agentPrices.saveSuccess'))
    await fetchRows(pagination.value.page)
  } catch (err: any) {
    notifyError(err?.message || t('admin.resellerProfileDetail.agentPrices.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(() => fetchRows())
</script>

<template>
  <div class="rounded-xl border border-border bg-card">
    <div class="flex flex-col gap-3 border-b border-border px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <div class="text-sm font-medium">
          {{ isDefaultScope ? t('admin.resellerProfileDetail.agentPrices.defaultTitle') : t('admin.resellerProfileDetail.agentPrices.title') }}
        </div>
        <p class="mt-1 text-xs text-muted-foreground">
          {{ isDefaultScope ? t('admin.resellerProfileDetail.agentPrices.defaultHint') : t('admin.resellerProfileDetail.agentPrices.hint') }}
        </p>
      </div>
      <div class="flex gap-2">
        <Input v-model="keyword" class="h-9 w-full sm:w-56" :placeholder="t('admin.resellerProfileDetail.agentPrices.search')" @update:modelValue="debouncedSearch" />
        <Button :disabled="saving || loading" @click="save">{{ t('admin.resellerProfileDetail.agentPrices.save') }}</Button>
      </div>
    </div>
    <div class="overflow-x-auto">
      <Table class="min-w-[760px]">
        <TableHeader>
          <TableRow>
            <TableHead class="px-4 py-3">{{ t('admin.resellerProfileDetail.agentPrices.product') }}</TableHead>
            <TableHead class="px-4 py-3">{{ t('admin.resellerProfileDetail.agentPrices.sku') }}</TableHead>
            <TableHead class="px-4 py-3">{{ t('admin.resellerProfileDetail.agentPrices.retailPrice') }}</TableHead>
            <TableHead class="px-4 py-3">{{ t('admin.resellerProfileDetail.agentPrices.costPrice') }}</TableHead>
            <TableHead v-if="!isDefaultScope" class="px-4 py-3">{{ t('admin.resellerProfileDetail.agentPrices.defaultAgentPrice') }}</TableHead>
            <TableHead class="px-4 py-3">
              {{ isDefaultScope ? t('admin.resellerProfileDetail.agentPrices.defaultAgentPrice') : t('admin.resellerProfileDetail.agentPrices.agentPrice') }}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-for="product in products" :key="product.id">
            <TableRow v-for="(sku, index) in product.skus" :key="sku.id">
              <TableCell class="px-4 py-3 text-sm">
                <span v-if="index === 0">{{ getLocalizedText(product.title) || `#${product.id}` }}</span>
              </TableCell>
              <TableCell class="px-4 py-3 text-xs">{{ skuLabel(sku) }}</TableCell>
              <TableCell class="px-4 py-3 font-mono text-xs">{{ sku.price_amount }}</TableCell>
              <TableCell class="px-4 py-3 font-mono text-xs text-muted-foreground">{{ Number(sku.cost_price_amount) > 0 ? sku.cost_price_amount : '-' }}</TableCell>
              <TableCell v-if="!isDefaultScope" class="px-4 py-3 font-mono text-xs text-muted-foreground">
                {{ hasDefaultPrice(sku) ? sku.default_agent_price_amount : '-' }}
              </TableCell>
              <TableCell class="px-4 py-3">
                <Input
                  v-model="drafts[sku.id]"
                  inputmode="decimal"
                  class="h-9 w-32 font-mono"
                  :class="draftInvalid(sku) ? 'border-destructive' : ''"
                  :placeholder="placeholderFor(sku)"
                />
              </TableCell>
            </TableRow>
          </template>
          <TableRow v-if="!loading && products.length === 0">
            <TableCell :colspan="isDefaultScope ? 5 : 6" class="px-4 py-8 text-center text-muted-foreground">{{ t('admin.resellerProfileDetail.agentPrices.empty') }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
    <div class="px-4 pb-3">
      <ListPagination
        :page="pagination.page"
        :total-page="pagination.total_page"
        :total="pagination.total"
        :page-size="pagination.page_size"
        @change-page="changePage"
        @change-page-size="changePageSize"
      />
    </div>
  </div>
</template>
