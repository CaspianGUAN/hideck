import { api } from '../stores/auth'

export type MBNProfile = { index: number; name: string; version: string; date: string; selected: boolean; activated: boolean; origin: string }
export type MBNOwner = { device_id: string; imei: string; iccid: string; model: string; firmware: string; generation: number; identity_generation: number }
export type MBNSnapshot = { owner: MBNOwner; autosel: boolean; profiles: MBNProfile[]; capabilities: { list: boolean; select: boolean; autosel: boolean; add: boolean; delete: boolean; export: boolean } }
export type MBNAsset = { id: string; name: string; sha256: string; size: number; source: string; model: string; firmware: string }
export type MBNPolicy = { key: string; mode: string; model: string; firmware: string; profile: string; version: string; asset_id: string; allow_import: boolean }
export type MBNRequest = {
  expected_owner?: MBNOwner;
  request_id: string; kind: 'apply' | 'import' | 'delete' | 'trial' | 'restore'; profile?: string; autosel?: boolean; asset_id?: string;
  confirm_name?: string; accept_no_backup?: boolean; accept_unknown_compatibility?: boolean; replacement?: string;
  candidates?: string[]; goal?: string; target_plmn?: string; recovery_seconds?: number; registration_seconds?: number; stability_seconds?: number; restore_operation?: string;
  probe_url?: string; allow_data?: boolean; allow_native_ims?: boolean
}
export type MBNOperation = {
  id: string; device_id: string; status: string; stage: string; error?: string; warning?: string; cancel_requested: boolean; created_at: string;
  original: MBNSnapshot; current: MBNSnapshot; request: MBNRequest;
  results: { profile: string; passed: boolean; error?: string; plmn: string }[]
}
const base = (id: string) => `/devices/${encodeURIComponent(id)}/mbn`
export const mbnService = {
  async status(id: string) { return (await api.get<MBNSnapshot>(base(id))).data },
  async operations(id: string) { return (await api.get<{ items: MBNOperation[] | null }>(`${base(id)}/operations`)).data.items || [] },
  async start(id: string, request: MBNRequest) { return (await api.post<{ operation: MBNOperation }>(`${base(id)}/operations`, request)).data.operation },
  async cancel(id: string, operation: string) { await api.post(`${base(id)}/operations/${encodeURIComponent(operation)}/cancel`) },
  async files() { return (await api.get<{ items: MBNAsset[] | null }>('/mbn/files')).data.items || [] },
  async upload(file: File, metadata: { model: string; firmware: string; source: string }) {
    const data = new FormData(); data.append('file', file)
    Object.entries(metadata).forEach(([key, value]) => data.append(key, value))
    return (await api.post<MBNAsset>('/mbn/files', data)).data
  },
  async download(id: string) { return (await api.get<Blob>(`/mbn/files/${encodeURIComponent(id)}`, { responseType: 'blob' })).data },
  async deleteFile(id: string) { await api.delete(`/mbn/files/${encodeURIComponent(id)}`) },
  async policies(id: string) { return (await api.get<{ items: MBNPolicy[] }>(`${base(id)}/policies`)).data.items },
  async savePolicy(id: string, policy: MBNPolicy) { await api.put(`${base(id)}/policies`, policy) }
}

export const mbnRunning = (op: MBNOperation) => ['queued', 'running', 'cancelling'].includes(op.status)
export const mbnStatusLabel: Record<string, string> = {
  queued: '排队中', running: '执行中', cancelling: '正在取消', succeeded: '已完成', failed: '失败', cancelled: '已取消', needs_attention: '需要处理'
}
export const mbnKindLabel: Record<string, string> = { apply: '应用配置', import: '导入模组', delete: '删除配置', trial: '自动试配', restore: '恢复配置' }
export const mbnStageLabel: Record<string, string> = {
  queued: '等待执行', reading_original: '读取原配置', original_saved: '已记录原配置', preparing: '暂停通话和数据服务', applying: '写入配置', waiting_reboot: '等待模组重启',
  verifying: '确认配置已生效', uploading_to_modem: '传输文件', importing: '导入配置', deleting: '删除配置', restoring: '恢复原配置',
  observing_registration: '等待稳定驻网', candidate_complete: '当前配置测试结束', restored: '原配置已恢复', resuming: '恢复通话和数据服务'
}
