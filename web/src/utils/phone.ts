import type { PhoneCall, PhoneRecord } from '../services/phone'

const DIAL_NUMBER_PATTERN = /^\+?[0-9]{1,32}$/

export function dialNumberError(number: string, region?: string): string {
  if (!number) return ''
  if (!DIAL_NUMBER_PATTERN.test(number)) return '号码只能包含可选的前导 + 和 1–32 位数字'
  if (region === 'GB' && number.startsWith('+440')) {
    return '英国国际号码需去掉本地号码开头的 0，例如 079… 应填写 +4479…'
  }
  return ''
}

export const phoneStatusLabels: Record<string, string> = {
  calling: '呼叫中',
  ringing: '响铃中',
  connected: '通话中',
  completed: '已结束',
  missed: '未接',
  rejected: '已拒接',
  busy: '忙线',
  failed: '失败',
  waiting: '呼叫等待'
}

export function phoneStatusLabel(status: string) {
  return phoneStatusLabels[status] || status
}

export function phoneCallStatusLabel(call: Readonly<PhoneCall>, ending = false) {
  if (ending) return '挂断中'
  if (call.status === 'connected' && call.held) return '保持中'
  return phoneStatusLabel(call.status)
}

export function phoneRecordStatusLabel(record: Readonly<PhoneRecord>) {
  if (record.end_reason === 'local_hangup') return '已挂断'
  if (record.end_reason === 'remote_bye') return '对方已挂断'
  return phoneStatusLabel(record.status)
}

export function formatCallDuration(call: PhoneCall, now = Date.now()) {
  const start = call.answered_at || call.started_at
  const end = call.ended_at || new Date(now).toISOString()
  const seconds = Math.max(0, Math.floor((Date.parse(end) - Date.parse(start)) / 1000))
  return formatDuration(seconds)
}

export function formatRecordDuration(record: PhoneRecord) {
  return formatDuration(record.duration_seconds)
}

export function formatCallTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  }).format(date)
}

export function normalizeCallOwnership(call: PhoneCall, mediaId: string): PhoneCall {
  if (!call.media_id) return call
  return { ...call, read_only: call.media_id !== mediaId }
}

export function shouldRefreshCallMedia(call: PhoneCall | undefined, mediaId: string) {
  if (!call || call.media_id === mediaId) return false
  return !(call.direction === 'inbound' && call.status === 'ringing' && !call.media_id)
}

export function phoneErrorMessage(error: unknown, fallback: string) {
  const responseMessage = (error as { response?: { data?: { message?: unknown } } })?.response?.data?.message
  if (typeof responseMessage === 'string' && responseMessage) return localizeHoldError(responseMessage)
  if (error instanceof Error && error.message) return localizeHoldError(error.message)
  return fallback
}

function localizeHoldError(message: string) {
  if (message.includes('hold is not aligned')) return '保持未对齐，暂不可用'
  return message
}

function formatDuration(seconds: number) {
  const whole = Math.max(0, Math.floor(seconds))
  const hours = Math.floor(whole / 3600)
  const minutes = Math.floor((whole % 3600) / 60)
  const remaining = whole % 60
  const parts = [minutes, remaining]
  if (hours > 0) parts.unshift(hours)
  return parts.map((part) => String(part).padStart(2, '0')).join(':')
}
