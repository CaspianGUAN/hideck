import type {
  WiFiCallingHealthEvent,
  WiFiCallingHealthSnapshot,
  WiFiCallingHealthState
} from '../types/api'
import { formatDeviceDateTime } from './deviceTime'

export type WiFiCallingHealthTone = 'neutral' | 'success' | 'warning' | 'danger' | 'stopped'

const stateLabels: Readonly<Record<WiFiCallingHealthState, string>> = Object.freeze({
  checking: '等待首次连接',
  pending: '短信接收待验证',
  healthy: '运行稳定',
  recovering: '正在恢复',
  unavailable: '当前不可用',
  stopped: '已主动关闭'
})

const eventLabels: Readonly<Record<WiFiCallingHealthEvent['kind'], string>> = Object.freeze({
  started: '开始监测',
  interrupted: '连接中断',
  recovered: '连接恢复',
  failed: '启动失败',
  stopped: '主动关闭'
})

export function wifiCallingHealthTone(state?: WiFiCallingHealthState): WiFiCallingHealthTone {
  switch (state) {
    case 'healthy': return 'success'
    case 'recovering': return 'warning'
    case 'unavailable': return 'danger'
    case 'stopped': return 'stopped'
    default: return 'neutral'
  }
}

export function wifiCallingHealthLabel(health?: WiFiCallingHealthSnapshot): string {
  if (!health) return '暂无监测数据'
  return wifiCallingHealthStateLabel(health.state)
}

export function wifiCallingHealthStateLabel(state?: WiFiCallingHealthState): string {
  if (!state) return '状态未知'
  return stateLabels[state] || '状态未知'
}

export function wifiCallingHealthDetail(health?: WiFiCallingHealthSnapshot): string {
  if (!health) return '本次服务启动后尚无 WiFi Calling 运行记录'
  if (!health.measured && health.state === 'unavailable') return health.last_reason || 'WiFi Calling 启动失败'
  if (!health.active) return health.last_reason ? `关闭原因：${health.last_reason}` : '当前会话已结束'
  if (health.state === 'pending') return 'IMS 已注册，尚无短信下行证据；待验证期间不计为正常或中断'
  if (!health.measured) return 'IMS 注册成功且接收状态可判定后开始统计可用率'
  if (health.state === 'healthy') return `已稳定 ${formatHealthDuration(health.stable_seconds)}`
  return health.last_reason || '等待 IMS 连接恢复'
}

export function formatHealthDuration(value: number | undefined): string {
  const seconds = Math.max(0, Math.floor(Number(value) || 0))
  if (seconds < 60) return `${seconds}秒`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}分钟`
  const hours = Math.floor(minutes / 60)
  const remainderMinutes = minutes % 60
  if (hours < 24) return remainderMinutes ? `${hours}小时${remainderMinutes}分` : `${hours}小时`
  const days = Math.floor(hours / 24)
  const remainderHours = hours % 24
  return remainderHours ? `${days}天${remainderHours}小时` : `${days}天`
}

export function formatHealthAvailability(health?: WiFiCallingHealthSnapshot): string {
  if (!health?.measured) return '--'
  const availability = Math.max(0, Math.min(100, Number(health.availability) || 0))
  return `${availability.toFixed(availability === 100 ? 0 : 1)}%`
}

export function wifiCallingHealthEventLabel(event: WiFiCallingHealthEvent): string {
  return eventLabels[event.kind] || '状态变更'
}

export function formatHealthEventTime(value?: string): string {
  if (!value) return '--'
  return formatDeviceDateTime(value, { fallback: value })
}

export function healthSegmentDuration(startedAt: string, endedAt: string): number {
  const start = Date.parse(startedAt)
  const end = Date.parse(endedAt)
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return 1
  return Math.max(1, Math.round((end - start) / 1000))
}
