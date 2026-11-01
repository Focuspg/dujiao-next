<template>
  <div class="space-y-2 rounded-xl border border-dashed bg-muted/30 p-3">
    <p class="text-xs text-muted-foreground">{{ t('directCharge.autoFill.steps') }}</p>
    <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
      <Button type="button" variant="outline" size="sm" :disabled="disabled" @click="openSessionPage">
        <ExternalLink />
        {{ t('directCharge.autoFill.open') }}
      </Button>
      <Button type="button" size="sm" :disabled="disabled || reading" @click="pasteFromClipboard">
        <Loader2 v-if="reading" class="animate-spin" />
        <ClipboardPaste v-else />
        {{ t('directCharge.autoFill.paste') }}
      </Button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ClipboardPaste, ExternalLink, Loader2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import type { DirectChargeMode } from '../../api/directCharge'
import { CHATGPT_SESSION_URL, parseChatGPTCredential } from '../../utils/directCharge'

defineProps<{ disabled?: boolean }>()

const emit = defineEmits<{
  fill: [mode: DirectChargeMode, credential: string]
  error: [message: string]
}>()

const { t } = useI18n()
const reading = ref(false)

const openSessionPage = () => {
  window.open(CHATGPT_SESSION_URL, '_blank', 'noopener,noreferrer')
}

// 识别并填入凭据；返回 false 表示内容无法识别，调用方按普通文本处理
const apply = (text: string): boolean => {
  const result = parseChatGPTCredential(text)
  if (result.kind === 'not_logged_in') {
    emit('error', t('directCharge.autoFill.notLoggedIn'))
    return true
  }
  if (result.kind === 'unrecognized') return false
  emit('fill', result.kind, result.credential)
  return true
}

const pasteFromClipboard = async () => {
  if (!navigator.clipboard?.readText) {
    emit('error', t('directCharge.autoFill.clipboardUnavailable'))
    return
  }
  reading.value = true
  try {
    const text = await navigator.clipboard.readText()
    if (!text.trim()) {
      emit('error', t('directCharge.autoFill.empty'))
    } else if (!apply(text)) {
      emit('error', t('directCharge.autoFill.unrecognized'))
    }
  } catch {
    emit('error', t('directCharge.autoFill.clipboardUnavailable'))
  } finally {
    reading.value = false
  }
}

// 用户直接在输入框里粘贴时也自动识别；识别不了就走浏览器默认粘贴
const handlePaste = (event: ClipboardEvent) => {
  const text = event.clipboardData?.getData('text') || ''
  if (text && apply(text)) event.preventDefault()
}

defineExpose({ handlePaste })
</script>
