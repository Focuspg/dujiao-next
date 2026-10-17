<template>
  <div class="space-y-4 rounded-2xl border bg-card p-5">
    <div>
      <h2 class="text-sm font-bold uppercase tracking-widest text-muted-foreground">{{ t('directCharge.title') }}</h2>
      <p class="mt-2 text-sm text-muted-foreground">{{ t('directCharge.intro') }}</p>
    </div>

    <template v-if="!preflight">
      <div class="flex flex-wrap gap-2" role="radiogroup" :aria-label="t('directCharge.modeLabel')">
        <button
          v-for="option in modes"
          :key="option"
          type="button"
          role="radio"
          :aria-checked="mode === option"
          class="rounded-lg border px-3 py-1.5 text-sm transition-colors"
          :class="mode === option ? 'border-primary/40 bg-primary/10 text-foreground' : 'bg-secondary text-muted-foreground'"
          @click="mode = option"
        >
          {{ t(`directCharge.modes.${option}`) }}
        </button>
      </div>

      <div class="space-y-2">
        <div class="flex items-center justify-between">
          <label for="direct-charge-credential" class="text-sm font-medium text-foreground">{{ t('directCharge.credentialLabel') }}</label>
          <button type="button" class="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground" @click="revealed = !revealed">
            <EyeOff v-if="revealed" class="h-3.5 w-3.5" />
            <Eye v-else class="h-3.5 w-3.5" />
            {{ revealed ? t('directCharge.hide') : t('directCharge.show') }}
          </button>
        </div>
        <Textarea
          id="direct-charge-credential"
          v-model="credential"
          rows="4"
          autocomplete="off"
          autocorrect="off"
          autocapitalize="off"
          spellcheck="false"
          data-private="true"
          :placeholder="t('directCharge.credentialPlaceholder')"
          class="font-mono text-xs break-all"
          :class="{ 'credential-masked': !revealed }"
          :disabled="verifying"
        />
        <p class="text-xs text-muted-foreground">{{ t(`directCharge.credentialHint.${mode}`) }}</p>
        <p class="flex items-start gap-1 text-xs text-muted-foreground">
          <ShieldCheck class="mt-0.5 h-3.5 w-3.5 shrink-0" />
          {{ t('directCharge.privacy') }}
        </p>
      </div>

      <Alert v-if="error" variant="destructive">
        <AlertDescription class="font-semibold">{{ error }}</AlertDescription>
      </Alert>

      <Button class="h-12 w-full font-bold" :disabled="disabled || verifying" @click="verify">
        <Loader2 v-if="verifying" class="animate-spin" />
        {{ verifying ? t('directCharge.verifying') : t('directCharge.verify') }}
      </Button>
    </template>

    <template v-else>
      <div class="rounded-xl border bg-muted/40 p-4">
        <h3 class="mb-3 text-sm font-semibold text-foreground">{{ t('directCharge.resultTitle') }}</h3>
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
          <dt class="text-muted-foreground">{{ t('directCharge.account') }}</dt>
          <dd class="font-mono font-semibold text-foreground break-all">{{ preflight.account_masked }}</dd>
          <dt class="text-muted-foreground">{{ t('directCharge.currentPlan') }}</dt>
          <dd class="text-foreground">{{ preflight.current_plan || '-' }}</dd>
          <dt class="text-muted-foreground">{{ t('directCharge.targetPlan') }}</dt>
          <dd class="font-semibold text-foreground">{{ preflight.plan }}</dd>
        </dl>
        <p class="mt-3 text-xs" :class="minutesLeft > 0 ? 'text-muted-foreground' : 'text-destructive'">
          {{ minutesLeft > 0 ? t('directCharge.expiresIn', { minutes: minutesLeft }) : t('directCharge.expired') }}
        </p>
      </div>

      <label class="flex cursor-pointer items-start gap-2 text-sm text-foreground">
        <input v-model="confirmed" type="checkbox" class="mt-1 h-4 w-4 accent-[hsl(var(--primary))]" :disabled="minutesLeft <= 0 || creating" />
        <span>{{ t('directCharge.confirmAccount') }}</span>
      </label>

      <Alert v-if="error" variant="destructive">
        <AlertDescription class="font-semibold">{{ error }}</AlertDescription>
      </Alert>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Button variant="secondary" class="h-12 font-bold" :disabled="creating" @click="reset">
          {{ t('directCharge.reverify') }}
        </Button>
        <Button class="h-12 font-bold" :disabled="!confirmed || minutesLeft <= 0 || creating" @click="createOrder">
          <Loader2 v-if="creating" class="animate-spin" />
          {{ creating ? t('directCharge.creating') : t('directCharge.createOrder') }}
        </Button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { Eye, EyeOff, Loader2, ShieldCheck } from 'lucide-vue-next'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { directChargeAPI, type DirectChargeMode, type DirectChargePreflight } from '../../api/directCharge'
import { credentialTooLong, preflightMinutesLeft } from '../../utils/directCharge'

const props = defineProps<{
  skuId: number | null
  disabled?: boolean
}>()

const { t } = useI18n()
const router = useRouter()

const modes: DirectChargeMode[] = ['session', 'access_token']
const mode = ref<DirectChargeMode>('session')
// 凭据只保存在组件内存中：不写入 store、URL 或浏览器存储，验证成功后立即清空
const credential = ref('')
const revealed = ref(false)
const verifying = ref(false)
const creating = ref(false)
const confirmed = ref(false)
const error = ref('')
const preflight = ref<DirectChargePreflight | null>(null)
const now = ref(Date.now())
const timer = window.setInterval(() => { now.value = Date.now() }, 15000)
onUnmounted(() => {
  window.clearInterval(timer)
  credential.value = ''
})

const minutesLeft = computed(() => preflightMinutesLeft(preflight.value?.expires_at, now.value))

watch(() => props.skuId, () => reset())

const reset = () => {
  preflight.value = null
  confirmed.value = false
  error.value = ''
}

const verify = async () => {
  error.value = ''
  if (!props.skuId) {
    error.value = t('directCharge.skuRequired')
    return
  }
  const value = credential.value.trim()
  if (!value) {
    error.value = t('directCharge.required')
    return
  }
  if (credentialTooLong(value)) {
    error.value = t('directCharge.tooLong')
    return
  }
  verifying.value = true
  try {
    const response = await directChargeAPI.preflight({ sku_id: props.skuId, mode: mode.value, credential: value })
    preflight.value = response.data.data as DirectChargePreflight
    now.value = Date.now()
    credential.value = ''
    revealed.value = false
  } catch (err: any) {
    error.value = err?.message || t('common.api.networkError')
  } finally {
    verifying.value = false
  }
}

const createOrder = async () => {
  if (!preflight.value || !confirmed.value) return
  error.value = ''
  creating.value = true
  try {
    const response = await directChargeAPI.createOrder(preflight.value.preflight_ref)
    const orderNo = response.data.data?.order_no
    if (!orderNo) throw new Error(t('checkout.errors.submitFailed'))
    router.push(`/pay?order_no=${encodeURIComponent(orderNo)}`)
  } catch (err: any) {
    error.value = err?.message || t('checkout.errors.submitFailed')
  } finally {
    creating.value = false
  }
}
</script>

<style scoped>
.credential-masked {
  -webkit-text-security: disc;
}
@supports not (-webkit-text-security: disc) {
  .credential-masked {
    color: transparent;
    text-shadow: 0 0 6px hsl(var(--foreground) / 0.6);
  }
}
</style>
