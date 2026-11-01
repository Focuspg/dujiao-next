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

/** ChatGPT 登录状态接口：已登录时返回含 accessToken 的 JSON，未登录时返回空对象 */
export const CHATGPT_SESSION_URL = 'https://chatgpt.com/api/auth/session'

export type ChatGPTCredentialParse =
    | { kind: 'session' | 'access_token'; credential: string }
    | { kind: 'not_logged_in' }
    | { kind: 'unrecognized' }

const isJwtLike = (value: string, dots: number): boolean =>
    value.startsWith('eyJ') && !/\s/.test(value) && value.split('.').length === dots + 1

/**
 * 识别用户从 chatgpt.com 复制来的内容：
 * - /api/auth/session 返回的 JSON：有 sessionToken 按 Session 原样提交，否则取 accessToken；空对象视为未登录
 * - 单独的 Session Cookie（JWE，5 段）或 Access Token（JWT，3 段）
 */
export const parseChatGPTCredential = (raw: string): ChatGPTCredentialParse => {
    const text = raw.trim()
    if (!text) return { kind: 'unrecognized' }
    if (text.startsWith('{')) {
        let data: unknown
        try {
            data = JSON.parse(text)
        } catch {
            return { kind: 'unrecognized' }
        }
        if (!data || typeof data !== 'object' || Array.isArray(data)) return { kind: 'unrecognized' }
        const obj = data as Record<string, unknown>
        if (typeof obj.sessionToken === 'string' && obj.sessionToken.trim()) {
            return { kind: 'session', credential: text }
        }
        if (typeof obj.accessToken === 'string' && obj.accessToken.trim()) {
            return { kind: 'access_token', credential: obj.accessToken.trim() }
        }
        if (!obj.user) return { kind: 'not_logged_in' }
        return { kind: 'unrecognized' }
    }
    if (isJwtLike(text, 4)) return { kind: 'session', credential: text }
    if (isJwtLike(text, 2)) return { kind: 'access_token', credential: text }
    return { kind: 'unrecognized' }
}
