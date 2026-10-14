import assert from 'node:assert/strict'
import test from 'node:test'
import { presentProxyEgress } from '../src/utils/proxyEgressPresentation.ts'

const observed = {
  source: 'Cloudflare HTTPS', reachable: true, ip: '203.0.113.1', country_code: 'GB',
  checked_at: '2026-09-23T12:00:00Z'
}

test('shows actual country independently of UDP status', () => {
  const view = presentProxyEgress(true, { state: 'unhealthy', detail: 'UDP timeout', egress: observed })
  assert.match(view.label, /英国.*GB/)
  assert.equal(view.ip, '203.0.113.1')
  assert.match(view.detail, /不代表运营商 UDP/)
})

test('failed checks do not present a previously known country as current', () => {
  const view = presentProxyEgress(true, { state: 'unhealthy', detail: 'failed', lastKnownEgress: observed })
  assert.equal(view.label, '出口未确认')
  assert.equal(view.tone, 'warning')
  assert.equal(view.ip, '')
  assert.match(view.detail, /上次成功：GB/)
})

test('UDP success does not invent an exit country', () => {
  const view = presentProxyEgress(true, { state: 'healthy', detail: 'UDP OK' })
  assert.equal(view.label, '出口未确认')
  assert.equal(view.tone, 'warning')
})
