import type { VoWiFiRuntimeState } from '../types/api'

export function smsReceptionPending(runtime?: VoWiFiRuntimeState): boolean {
  return runtime?.ims_ready === true && runtime.sms_ready !== true && runtime.sms_rx_pending === true
}

export function smsReceptionDetail(runtime?: VoWiFiRuntimeState): string {
  if (smsReceptionPending(runtime)) return 'IMS 已注册，短信接收待验证'
  if (runtime?.sms_ready && runtime.sms_ready_reason === 'IMS SMS receiver verified on registered TCP flow') {
    return '短信接收已验证，复用注册连接'
  }
  return ''
}
