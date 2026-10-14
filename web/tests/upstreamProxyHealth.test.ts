import assert from 'node:assert/strict'
import test from 'node:test'
import { fail, ok, type ServiceResult } from '../src/types/domain.ts'
import type {
  UpstreamProxy,
  UpstreamProxyProbeResponse
} from '../src/types/api.ts'
import {
  createUpstreamProbeRunner,
  type UpstreamProxyHealthMap
} from '../src/utils/upstreamProxyHealth.ts'

const enabledProxy: UpstreamProxy = {
  id: 'route-1',
  name: 'Route 1',
  addr: '198.51.100.10:1080',
  username: '',
  enabled: true
}

function successfulProbe(durationMs: number): ServiceResult<UpstreamProxyProbeResponse> {
  return ok({
    status: 'ok',
    message: '前置代理探测成功',
    result: {
      proxy_addr: enabledProxy.addr,
      stage: 'ok',
      reachable: true,
      handshake_ok: true,
      udp_associate_ok: true,
      udp_relay_ok: true,
      duration_ms: durationMs,
      diagnosis: '代理公共 DNS UDP 数据往返正常'
    }
  })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}

test('probe runner skips disabled proxies and exposes row failures', async () => {
  const snapshots: UpstreamProxyHealthMap[] = []
  const calls: string[] = []
  const runner = createUpstreamProbeRunner({
    probe: async (id) => {
      calls.push(id)
      return fail({ message: '代理明确拒绝了 UDP Associate' })
    },
    publish: snapshot => snapshots.push(snapshot)
  })

  await runner.run([
    enabledProxy,
    { ...enabledProxy, id: 'disabled', enabled: false }
  ])

  assert.deepEqual(calls, ['route-1'])
  assert.equal(snapshots[0]?.['route-1']?.state, 'checking')
  assert.equal(snapshots.at(-1)?.['route-1']?.state, 'unhealthy')
  assert.equal(snapshots.at(-1)?.['disabled']?.state, 'disabled')
  assert.equal(snapshots.at(-1)?.['route-1']?.detail, '代理明确拒绝了 UDP Associate')
})

test('UDP Associate alone is not presented as healthy', async () => {
  const snapshots: UpstreamProxyHealthMap[] = []
  const runner = createUpstreamProbeRunner({
    probe: async () => ok({
      status: 'error',
      message: 'UDP 数据转发失败',
      result: {
        proxy_addr: enabledProxy.addr,
        stage: 'udp_relay',
        reachable: true,
        handshake_ok: true,
        udp_associate_ok: true,
        udp_relay_ok: false,
        duration_ms: 5000,
        diagnosis: '代理接受 UDP Associate，但公共 DNS UDP 数据无法完成往返'
      }
    }),
    publish: snapshot => snapshots.push(snapshot)
  })

  await runner.run([enabledProxy])

  assert.equal(snapshots.at(-1)?.['route-1']?.state, 'unhealthy')
  assert.equal(
    snapshots.at(-1)?.['route-1']?.detail,
    '代理接受 UDP Associate，但公共 DNS UDP 数据无法完成往返'
  )
})

test('a stale probe run cannot overwrite a newer result', async () => {
  const first = deferred<ServiceResult<UpstreamProxyProbeResponse>>()
  const second = deferred<ServiceResult<UpstreamProxyProbeResponse>>()
  const queue = [first, second]
  const snapshots: UpstreamProxyHealthMap[] = []
  const runner = createUpstreamProbeRunner({
    probe: async () => queue.shift()!.promise,
    publish: snapshot => snapshots.push(snapshot)
  })

  const staleRun = runner.run([enabledProxy])
  const currentRun = runner.run([enabledProxy])
  second.resolve(successfulProbe(12))
  await currentRun
  first.resolve(fail({ message: '旧请求超时' }))
  await staleRun

  assert.equal(snapshots.at(-1)?.['route-1']?.state, 'healthy')
  assert.equal(snapshots.at(-1)?.['route-1']?.durationMs, 12)
})

function egressProbe(ip: string, country: string): ServiceResult<UpstreamProxyProbeResponse> {
  const response = successfulProbe(12)
  if (!response.ok) throw new Error('missing test response')
  response.data.result.egress = {
    source: 'Cloudflare HTTPS', reachable: true, ip, country_code: country,
    checked_at: '2026-09-23T12:00:00Z'
  }
  return response
}

test('exit changes remain visible across subsequent polls and failures', async () => {
  let reply = egressProbe('203.0.113.1', 'GB')
  let snapshot: UpstreamProxyHealthMap = {}
  const runner = createUpstreamProbeRunner({ probe: async () => reply, publish: value => { snapshot = value } })
  await runner.run([enabledProxy])
  reply = egressProbe('203.0.113.2', 'DE')
  await runner.run([enabledProxy])
  const change = snapshot['route-1']?.egressChange
  assert.match(change || '', /GB 203.0.113.1 → DE 203.0.113.2/)
  await runner.run([enabledProxy])
  assert.equal(snapshot['route-1']?.egressChange, change)
  reply = fail({ message: '代理未提供可用节点' })
  await runner.run([enabledProxy])
  assert.equal(snapshot['route-1']?.state, 'unhealthy')
  assert.equal(snapshot['route-1']?.egress, undefined)
  assert.equal(snapshot['route-1']?.lastKnownEgress?.country_code, 'DE')
  assert.equal(snapshot['route-1']?.egressChange, change)
})

test('configuration change discards the old exit observation', async () => {
  let reply = egressProbe('203.0.113.1', 'GB')
  let snapshot: UpstreamProxyHealthMap = {}
  const runner = createUpstreamProbeRunner({ probe: async () => reply, publish: value => { snapshot = value } })
  await runner.run([enabledProxy])
  reply = fail({ message: '新代理不可达' })
  await runner.run([{ ...enabledProxy, updated_at: '2026-09-23T12:00:01Z' }])
  assert.equal(snapshot['route-1']?.lastKnownEgress, undefined)
  assert.equal(snapshot['route-1']?.egressChange, undefined)
})

test('a failed UDP probe can retain an independent successful HTTPS observation', async () => {
  const reply = egressProbe('203.0.113.1', 'GB')
  if (!reply.ok) throw new Error('missing response')
  reply.data.status = 'error'
  reply.data.result.udp_relay_ok = false
  let snapshot: UpstreamProxyHealthMap = {}
  const runner = createUpstreamProbeRunner({ probe: async () => reply, publish: value => { snapshot = value } })
  await runner.run([enabledProxy])
  assert.equal(snapshot['route-1']?.state, 'unhealthy')
  assert.equal(snapshot['route-1']?.egress?.country_code, 'GB')
})

test('one rejected probe does not discard another proxy result', async () => {
  let snapshot: UpstreamProxyHealthMap = {}
  const runner = createUpstreamProbeRunner({ probe: async id => {
    if (id === 'broken') throw new Error('request failed')
    return egressProbe('203.0.113.1', 'GB')
  }, publish: value => { snapshot = value } })
  await runner.run([enabledProxy, { ...enabledProxy, id: 'broken' }])
  assert.equal(snapshot['route-1']?.state, 'healthy')
  assert.equal(snapshot['broken']?.state, 'unhealthy')
})
