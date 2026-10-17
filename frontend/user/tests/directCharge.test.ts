import test from 'node:test'
import assert from 'node:assert/strict'
import {
  DIRECT_CHARGE_MAX_CREDENTIAL_BYTES,
  credentialTooLong,
  directChargeShouldPoll,
  directChargeStatusTone,
  isDirectChargeProduct,
  preflightMinutesLeft,
} from '../src/utils/directCharge.ts'

test('direct charge products are detected by fulfillment type', () => {
  assert.equal(isDirectChargeProduct({ fulfillment_type: 'direct_charge' }), true)
  assert.equal(isDirectChargeProduct({ fulfillment_type: 'manual' }), false)
  assert.equal(isDirectChargeProduct(null), false)
})

test('credential length is measured in bytes after trimming', () => {
  assert.equal(credentialTooLong('a'.repeat(DIRECT_CHARGE_MAX_CREDENTIAL_BYTES)), false)
  assert.equal(credentialTooLong(`  ${'a'.repeat(DIRECT_CHARGE_MAX_CREDENTIAL_BYTES)}  `), false)
  assert.equal(credentialTooLong('a'.repeat(DIRECT_CHARGE_MAX_CREDENTIAL_BYTES + 1)), true)
  assert.equal(credentialTooLong('中'.repeat(DIRECT_CHARGE_MAX_CREDENTIAL_BYTES / 3 + 1)), true)
})

test('preflight minutes left rounds up and stops at expiry', () => {
  const now = Date.parse('2026-10-17T10:00:00Z')
  assert.equal(preflightMinutesLeft('2026-10-17T10:09:30Z', now), 10)
  assert.equal(preflightMinutesLeft('2026-10-17T10:00:10Z', now), 1)
  assert.equal(preflightMinutesLeft('2026-10-17T09:59:59Z', now), 0)
  assert.equal(preflightMinutesLeft(undefined, now), 0)
  assert.equal(preflightMinutesLeft('not a date', now), 0)
})

test('status tone and polling', () => {
  assert.equal(directChargeStatusTone('succeeded'), 'success')
  assert.equal(directChargeStatusTone('failed'), 'danger')
  assert.equal(directChargeStatusTone('credential_required'), 'warning')
  assert.equal(directChargeStatusTone('pending_payment'), 'neutral')
  assert.equal(directChargeShouldPoll('processing'), true)
  assert.equal(directChargeShouldPoll('pending_payment'), false)
  assert.equal(directChargeShouldPoll('succeeded'), false)
})
