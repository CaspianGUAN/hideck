import type { ModemVoiceStatus } from '../types/api'

export function modemVoicePresentation(status?: ModemVoiceStatus) {
  if (status?.phase === 'restarting') return { title: '正在重启目标模组', detail: '为使 ADB 生效执行一次重启，设备重新连接后将按当前模式继续准备。', tone: 'is-pending' as const }
  if (status?.last_error) return { title: '模组直拨准备失败', detail: status.last_error, tone: 'is-failed' as const }
  if (status?.ready) return { title: '模组直拨已就绪', detail: '使用模组驻网通话和 USB 音频，可在电话页拨打或接听。', tone: 'is-ready' as const }
  if (status?.phase === 'preparing') return { title: '正在准备模组直拨', detail: '正在检查设备、加载语音运行时和准备音频。', tone: 'is-pending' as const }
  return { title: '模组直拨未就绪', detail: '请在电话页启用模组直拨并查看准备状态。', tone: 'is-idle' as const }
}

export function modemVoiceStages(status?: ModemVoiceStatus) {
  if (status?.phase === 'restarting') return [{ key: '音频准备', ready: undefined }]
  return [{ key: '音频准备', ready: status?.last_error ? false : status?.ready ? true : undefined }]
}
