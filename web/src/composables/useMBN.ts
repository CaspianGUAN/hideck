import { computed, onUnmounted, ref, watch, type Ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { mbnService, mbnRunning, type MBNSnapshot, type MBNOperation, type MBNRequest, type MBNAsset } from '../services/mbn'
import { toAppError } from '../services/http'

export function useMBN(deviceID: Ref<string>) {
  const snapshot = ref<MBNSnapshot | null>(null)
  const operations = ref<MBNOperation[]>([])
  const assets = ref<MBNAsset[]>([])
  const error = ref('')
  const loading = ref(false)
  const submitting = ref(false)
  const active = computed(() => operations.value.find(mbnRunning))
  let generation = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let disposed = false
  function report(err: unknown) { error.value = toAppError(err).message }
  function schedule(epoch: number) {
    clearTimeout(timer)
    if (!disposed && epoch === generation && active.value) timer = setTimeout(() => { void refreshJobs(epoch) }, 2000)
  }
  async function refreshJobs(epoch = generation) {
    const id = deviceID.value
    try {
      const jobs = await mbnService.operations(id)
      if (disposed || epoch !== generation) return
      const wasActive = active.value
      operations.value = jobs
      if (wasActive && !active.value) await inspect(epoch)
    } catch (err) { if (epoch === generation) report(err) }
    finally { schedule(epoch) }
  }
  async function inspect(epoch = generation) {
    loading.value = true
    try {
      const result = await mbnService.status(deviceID.value)
      if (epoch === generation && !disposed) { snapshot.value = result; error.value = '' }
    } catch (err) { if (epoch === generation) report(err) }
    finally { if (epoch === generation) loading.value = false }
  }
  async function refreshFiles() {
    const epoch = generation
    try { const result = await mbnService.files(); if (epoch === generation) assets.value = result }
    catch (err) { if (epoch === generation) report(err) }
  }
  async function start(request: Omit<MBNRequest, 'request_id'>, message: string) {
    if (submitting.value || active.value) return
    const id = deviceID.value, epoch = generation
    const owner = snapshot.value ? { ...snapshot.value.owner } : undefined
    const payload = { ...request, candidates: request.candidates?.slice() }
    submitting.value = true
    try {
      await ElMessageBox.confirm(message, '确认 MBN 操作', { type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '取消' })
      if (epoch !== generation) return
      const bytes = crypto.getRandomValues(new Uint8Array(16))
      const requestID = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
      const operation = await mbnService.start(id, { ...payload, request_id: requestID, expected_owner: owner })
      if (epoch !== generation) return
      operations.value = [operation, ...operations.value.filter(item => item.id !== operation.id)]
      error.value = ''; schedule(epoch)
    } catch (err) {
      if (err !== 'cancel' && err !== 'close' && epoch === generation) {
        await refreshJobs(epoch)
        report(err)
      }
    }
    finally { if (epoch === generation) submitting.value = false }
  }
  async function cancel() {
    if (!active.value) return
    const epoch = generation
    try { await mbnService.cancel(deviceID.value, active.value.id); if (epoch === generation) await refreshJobs(epoch) }
    catch (err) { if (epoch === generation) report(err) }
  }
  async function bindCurrent() {
    const current = snapshot.value
    if (!current) return
    const epoch = generation, id = deviceID.value
    const profile = current.profiles.find(item => item.selected && item.activated)
    if (!current.autosel && !profile) { error.value = '尚未确认生效配置，请刷新配置后再绑定。'; return }
    try {
      await ElMessageBox.confirm('记住当前配置，下次使用这张卡时自动应用。仅绑定此卡；型号或固件不匹配时不会应用。', '绑定当前配置', { type: 'warning' })
      if (epoch !== generation) return
      await mbnService.savePolicy(id, { key: `sim:${current.owner.iccid}`, mode: current.autosel ? 'autosel' : 'fixed', model: current.owner.model, firmware: current.owner.firmware, profile: profile?.name || '', version: profile?.version || '', asset_id: '', allow_import: false })
      if (epoch !== generation) return
      ElMessage.success('当前配置已绑定到此卡')
    } catch (err) { if (err !== 'cancel' && err !== 'close' && epoch === generation) report(err) }
  }
  watch(deviceID, () => {
    generation++; clearTimeout(timer); snapshot.value = null; operations.value = []; assets.value = []; error.value = ''; loading.value = false; submitting.value = false
    void refreshJobs(); void refreshFiles()
  }, { immediate: true })
  onUnmounted(() => { disposed = true; generation++; clearTimeout(timer) })
  return { snapshot, operations, assets, error, loading, submitting, active, inspect, refreshJobs, refreshFiles, start, cancel, bindCurrent, report }
}
