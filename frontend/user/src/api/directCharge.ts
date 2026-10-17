import { userApi } from './client'

export type DirectChargeMode = 'session' | 'access_token'

export interface DirectChargePreflightPayload {
    sku_id: number
    mode: DirectChargeMode
    credential: string
}

export interface DirectChargePreflight {
    preflight_ref: string
    sku_id: number
    product: string
    plan: string
    account_masked: string
    current_plan: string
    expires_at: string
}

export interface DirectChargeOrderCreated {
    order_no: string
    expires_at?: string
    replayed: boolean
}

export interface DirectChargeTimelineItem {
    status: string
    at: string
}

export interface DirectChargeOrder {
    order_no: string
    status: string
    product: string
    plan: string
    account_masked: string
    action_message?: string
    created_at: string
    paid_at?: string
    completed_at?: string
    timeline: DirectChargeTimelineItem[]
}

export const directChargeAPI = {
    preflight: (data: DirectChargePreflightPayload) => userApi.post('/direct-charge/preflights', data),
    createOrder: (preflightRef: string) => userApi.post('/direct-charge/orders', { preflight_ref: preflightRef }),
    order: (orderNo: string) => userApi.get(`/direct-charge/orders/${encodeURIComponent(orderNo)}`),
}
