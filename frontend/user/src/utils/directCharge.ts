/** 与后端一致的凭据长度上限（字节） */
export const DIRECT_CHARGE_MAX_CREDENTIAL_BYTES = 64 * 1024

export const isDirectChargeProduct = (product?: { fulfillment_type?: string } | null): boolean =>
    product?.fulfillment_type === 'direct_charge'

export const credentialByteLength = (value: string): number => new TextEncoder().encode(value.trim()).length

export const credentialTooLong = (value: string): boolean =>
    credentialByteLength(value) > DIRECT_CHARGE_MAX_CREDENTIAL_BYTES

/** 预检结果剩余有效分钟数，已过期返回 0 */
export const preflightMinutesLeft = (expiresAt: string | undefined, now: number = Date.now()): number => {
    if (!expiresAt) return 0
    const deadline = Date.parse(expiresAt)
    if (Number.isNaN(deadline) || deadline <= now) return 0
    return Math.max(1, Math.ceil((deadline - now) / 60000))
}

export type DirectChargeTone = 'success' | 'danger' | 'warning' | 'info' | 'neutral'

export const directChargeStatusTone = (status: string): DirectChargeTone => {
    switch (status) {
        case 'succeeded':
            return 'success'
        case 'failed':
            return 'danger'
        case 'credential_required':
        case 'action_required':
        case 'verifying':
            return 'warning'
        case 'queued':
        case 'processing':
            return 'info'
        default:
            return 'neutral'
    }
}

/** 进行中的状态需要轮询本地进度（只读本地状态，不触发上游调用） */
export const directChargeShouldPoll = (status: string): boolean =>
    ['queued', 'processing', 'verifying'].includes(status)
