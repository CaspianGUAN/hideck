import type { UpstreamProxyHealth } from './upstreamProxyHealth'
import type { ProxyPresentationTone } from './proxyPresentation'

export type ProxyEgressPresentation = Readonly<{
  label: string
  tone: ProxyPresentationTone
  ip: string
  detail: string
  checkedAt: string
  change: string
}>

function formatTime(value?: string): string {
  if (!value || !Number.isFinite(Date.parse(value))) return ''
  return new Date(value).toLocaleString()
}

function countryName(code: string): string {
  if (!/^[A-Z]{2}$/.test(code)) return '国家未知'
  return `${new Intl.DisplayNames(['zh-CN'], { type: 'region' }).of(code) || code} · ${code}`
}

export function presentProxyEgress(enabled: boolean, health?: UpstreamProxyHealth): ProxyEgressPresentation {
  const empty = { ip: '', checkedAt: '', change: '' }
  if (!enabled) return { ...empty, label: '未检测', tone: 'neutral', detail: '代理未启用' }
  if (!health || health.state === 'checking') {
    return { ...empty, label: '检测中', tone: 'warning', detail: '正在通过 SOCKS5 检测 HTTPS 出口' }
  }
  const egress = health.egress
  const last = health.lastKnownEgress
  const history = last ? `；上次成功：${last.country_code} ${last.ip}（${formatTime(last.checked_at)}）` : ''
  const common = {
    ip: egress?.ip || '',
    checkedAt: formatTime(egress?.checked_at || health.checkedAt),
    change: health.egressChange || ''
  }
  if (!egress?.reachable || egress.error || !egress.country_code) {
    return {
      ...common, label: egress?.ip ? '国家未知' : '出口未确认', tone: 'warning',
      detail: (egress?.error || '出口检测未完成，请检查代理上游节点、转发链路或检测服务') + history
    }
  }
  return {
    ...common, label: countryName(egress.country_code), tone: 'success',
    detail: `${egress.source} 检测结果；不代表运营商 UDP 流量的出口`
  }
}
