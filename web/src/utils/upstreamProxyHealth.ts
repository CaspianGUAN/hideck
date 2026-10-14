import type { ServiceResult } from '../types/domain'
import type {
  UpstreamProxy,
  UpstreamProxyEgressProbe,
  UpstreamProxyProbeResponse,
  UpstreamProxyProbeResult
} from '../types/api'

export type UpstreamProxyHealthState = 'checking' | 'disabled' | 'healthy' | 'unhealthy'

export type UpstreamProxyHealth = Readonly<{
  state: UpstreamProxyHealthState
  detail: string
  durationMs?: number
  checkedAt?: string
  egress?: UpstreamProxyEgressProbe
  lastKnownEgress?: UpstreamProxyEgressProbe
  egressChange?: string
}>

export type UpstreamProxyHealthMap = Readonly<Record<string, UpstreamProxyHealth>>

type ProbeRunnerConfig = Readonly<{
  probe: (id: string) => Promise<ServiceResult<UpstreamProxyProbeResponse>>
  publish: (snapshot: UpstreamProxyHealthMap) => void
}>

export function createUpstreamProbeRunner(config: ProbeRunnerConfig) {
  let activeRun = 0
  let snapshot: UpstreamProxyHealthMap = Object.freeze({})
  const observations = new Map<string, {
    fingerprint: string
    last?: UpstreamProxyEgressProbe
    change?: string
  }>()

  function observe(proxy: UpstreamProxy, health: UpstreamProxyHealth): UpstreamProxyHealth {
    const fingerprint = JSON.stringify([proxy.addr, proxy.username, proxy.updated_at])
    const stored = observations.get(proxy.id)
    const previous = stored?.fingerprint === fingerprint ? stored : undefined
    const current = health.egress
    let change = previous?.change
    if (current?.reachable && current.ip && current.country_code && !current.error) {
      const last = previous?.last
      if (last && (last.ip !== current.ip || last.country_code !== current.country_code)) {
        change = `${last.country_code} ${last.ip} → ${current.country_code} ${current.ip}（${new Date(current.checked_at).toLocaleString()}）`
      }
      observations.set(proxy.id, { fingerprint, last: current, change })
      return Object.freeze({ ...health, egressChange: change })
    }
    observations.set(proxy.id, { fingerprint, last: previous?.last, change })
    return Object.freeze({ ...health, lastKnownEgress: previous?.last, egressChange: change })
  }

  async function run(proxies: readonly UpstreamProxy[]): Promise<void> {
    const runId = ++activeRun
    const enabledIDs = new Set(proxies.filter(proxy => proxy.enabled).map(proxy => proxy.id))
    for (const id of observations.keys()) {
      if (!enabledIDs.has(id)) observations.delete(id)
    }
    snapshot = initialHealthMap(proxies)
    config.publish(snapshot)

    await Promise.all(proxies.filter(proxy => proxy.enabled).map(async (proxy) => {
      let result: ServiceResult<UpstreamProxyProbeResponse>
      try {
        result = await config.probe(proxy.id)
      } catch (error) {
        result = { ok: false, error: { message: error instanceof Error ? error.message : '代理探测请求失败' } }
      }
      if (runId !== activeRun) return
      snapshot = Object.freeze({ ...snapshot, [proxy.id]: observe(proxy, healthFromProbe(result)) })
      config.publish(snapshot)
    }))
  }

  function invalidate(): void {
    activeRun += 1
  }

  return Object.freeze({ invalidate, run })
}

function initialHealthMap(proxies: readonly UpstreamProxy[]): UpstreamProxyHealthMap {
  return Object.freeze(Object.fromEntries(proxies.map(proxy => [
    proxy.id,
    Object.freeze(proxy.enabled
      ? { state: 'checking', detail: '正在检测公共 DNS UDP 数据往返' }
      : { state: 'disabled', detail: '代理未启用，未执行健康探测' })
  ])))
}

function healthFromProbe(
  result: ServiceResult<UpstreamProxyProbeResponse>
): UpstreamProxyHealth {
  if (!result.ok) {
    return Object.freeze({ state: 'unhealthy', detail: result.error.message || '健康探测失败', checkedAt: new Date().toISOString() })
  }

  const probe = result.data.result
  const observation = { checkedAt: probe?.checked_at, egress: probe?.egress }
  if (!probe?.udp_relay_ok) {
    return Object.freeze({ ...observation, state: 'unhealthy', detail: probeFailureDetail(probe) })
  }

  return Object.freeze({
    ...observation,
    state: 'healthy',
    detail: probe.diagnosis || '代理公共 DNS UDP 数据往返正常',
    durationMs: validDuration(probe.duration_ms)
  })
}

function probeFailureDetail(result: UpstreamProxyProbeResult | undefined): string {
  if (!result) return '探测响应未包含公共 DNS UDP 往返结果'
  return [result.diagnosis, result.hint, result.error]
    .map(value => value?.trim())
    .filter(Boolean)
    .join('；') || '公共 DNS UDP 往返探测失败'
}

function validDuration(value: number | undefined): number | undefined {
  return Number.isFinite(value) && Number(value) >= 0 ? Math.round(Number(value)) : undefined
}
