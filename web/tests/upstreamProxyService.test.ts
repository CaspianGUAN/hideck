import assert from 'node:assert/strict'
import test from 'node:test'
import { upstreamProxyService } from '../src/services/upstream-proxy.ts'
import { api } from '../src/stores/auth.ts'
import type { UpstreamProxyProbeResponse } from '../src/types/api.ts'

test('probe service posts to the encoded action path without a request body', async () => {
  const originalPost = api.post
  const calls: unknown[][] = []
  const response: UpstreamProxyProbeResponse = {
    status: 'ok',
    message: '前置代理探测成功',
    result: {
      proxy_addr: '198.51.100.10:1080',
      stage: 'ok',
      reachable: true,
      handshake_ok: true,
      udp_associate_ok: true,
      udp_relay_ok: true,
      duration_ms: 4
    }
  }
  api.post = (async (...args: unknown[]) => {
    calls.push(args)
    return { data: response }
  }) as typeof api.post

  try {
    const result = await upstreamProxyService.probe('uk / primary')

    assert.equal(result.ok, true)
    assert.deepEqual(calls, [[
      '/upstream-proxies/uk%20%2F%20primary/actions/probe'
    ]])
  } finally {
    api.post = originalPost
  }
})

test('create service preserves a saved proxy warning response', async () => {
  const originalPost = api.post
  const response: UpstreamProxyProbeResponse = {
    status: 'warning',
    message: '前置代理已保存，但公共 DNS UDP 往返探测失败',
    result: {
      proxy_addr: '198.51.100.10:1080',
      stage: 'udp_relay',
      reachable: true,
      handshake_ok: true,
      udp_associate_ok: true,
      udp_relay_ok: false,
      duration_ms: 5000
    }
  }
  api.post = (async () => ({ data: response })) as typeof api.post

  try {
    const result = await upstreamProxyService.create({
      id: 'uk-primary',
      name: 'UK primary',
      addr: '198.51.100.10:1080',
      username: '',
      enabled: true
    })

    assert.equal(result.ok, true)
    if (result.ok) {
      assert.equal(result.data.status, 'warning')
      assert.equal(result.data.result.udp_relay_ok, false)
    }
  } finally {
    api.post = originalPost
  }
})

test('probe service preserves both observations from a diagnostic 502', async () => {
  const originalPost = api.post
  const data = { status: 'error', message: 'UDP timeout', result: {
    udp_relay_ok: false, egress: { reachable: true, ip: '203.0.113.1', country_code: 'GB' }
  } }
  api.post = (async () => { throw { isAxiosError: true, response: { status: 502, data } } }) as typeof api.post
  try {
    const result = await upstreamProxyService.probe('route')
    assert.equal(result.ok, true)
    if (result.ok) {
      assert.equal(result.data.status, 'error')
      assert.equal(result.data.result.udp_relay_ok, false)
      assert.equal(result.data.result.egress?.country_code, 'GB')
    }
  } finally { api.post = originalPost }
})

test('an unrelated gateway 502 remains a request failure', async () => {
  const originalPost = api.post
  api.post = (async () => { throw { isAxiosError: true, message: 'Bad Gateway', response: { status: 502, data: '<html>Bad Gateway</html>' } } }) as typeof api.post
  try {
    assert.equal((await upstreamProxyService.probe('route')).ok, false)
  } finally { api.post = originalPost }
})
