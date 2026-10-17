<template>
  <div class="space-y-3 rounded-xl border border-warning/40 bg-warning/5 p-4">
    <h3 class="text-sm font-semibold text-foreground">{{ t('directChargeOrder.resupply.title') }}</h3>
    <p class="text-xs text-muted-foreground">{{ t('directChargeOrder.resupply.hint') }}</p>
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
    <div class="flex items-center justify-between">
      <label for="direct-charge-resupply" class="text-sm font-medium text-foreground">{{ t('directCharge.credentialLabel') }}</label>
      <button type="button" class="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground" @click="revealed = !revealed">
        <EyeOff v-if="revealed" class="h-3.5 w-3.5" />
        <Eye v-else class="h-3.5 w-3.5" />
        {{ revealed ? t('directCharge.hide') : t('directCharge.show') }}
      </button>
    </div>
    <Textarea
      id="direct-charge-resupply"
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
      :disabled="submitting"
    />
    <p class="text-xs text-muted-foreground">{{ t(`directCharge.credentialHint.${mode}`) }}</p>
    <Alert v-if="error" variant="destructive">
      <AlertDescription class="font-semibold">{{ error }}</AlertDescription>
    </Alert>
    <Button class="h-11 w-full font-bold" :disabled="submitting" @click="submit">
      <Loader2 v-if="submitting" class="animate-spin" />
      {{ submitting ? t('directCharge.verifying') : t('directChargeOrder.resupply.submit') }}
    </Button>
  </div>
</template>

<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Eye, EyeOff, Loader2 } from 'lucide-vue-next'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { directChargeAPI, type DirectChargeMode, type DirectChargeOrder } from '../../api/directCharge'
import { credentialTooLong } from '../../utils/directCharge'

const props = defineProps<{ orderNo: string }>()
const emit = defineEmits<{ updated: [order: DirectChargeOrder] }>()

const { t } = useI18n()
const modes: DirectChargeMode[] = ['session', 'access_token']
const mode = ref<DirectChargeMode>('session')
// 凭据只保存在组件内存中，提交后立即清空
const credential = ref('')
const revealed = ref(false)
const submitting = ref(false)
const error = ref('')

onUnmounted(() => {
  credential.value = ''
})

const submit = async () => {
  error.value = ''
  const value = credential.value.trim()
  if (!value) {
    error.value = t('directCharge.required')
    return
  }
  if (credentialTooLong(value)) {
    error.value = t('directCharge.tooLong')
    return
  }
  submitting.value = true
  try {
    const response = await directChargeAPI.resupplyCredential(props.orderNo, { mode: mode.value, credential: value })
    credential.value = ''
    revealed.value = false
    emit('updated', response.data.data as DirectChargeOrder)
  } catch (err: any) {
    error.value = err?.message || t('common.api.networkError')
  } finally {
    submitting.value = false
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
