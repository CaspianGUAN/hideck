<script setup lang="ts">
import { computed, ref, toRef, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { ArrowSync24Regular, Document24Regular } from '@vicons/fluent'
import { useMBN } from '../composables/useMBN'
import { mbnStatusLabel, mbnStageLabel, mbnKindLabel, type MBNProfile } from '../services/mbn'
import MBNFileLibrary from './MBNFileLibrary.vue'
import MBNPolicyEditor from './MBNPolicyEditor.vue'

const props = defineProps<{ deviceId: string }>()
const { snapshot, operations, assets, error, loading, submitting, active, inspect, refreshJobs, refreshFiles, start, cancel, bindCurrent, report } = useMBN(toRef(props, 'deviceId'))
const selected = ref(''), auto = ref(false), candidates = ref<string[]>([]), goal = ref('registration'), target = ref('')
const recovery = ref(180), registration = ref(120), stability = ref(30), page = ref(1)
const probeURL = ref(''), allowData = ref(false), allowNativeIMS = ref(false)
const blocked = computed(() => !!active.value || submitting.value || loading.value)
const current = computed(() => snapshot.value?.profiles.find(item => item.activated)?.name || '未确认')
const history = computed(() => operations.value.slice((page.value - 1) * 8, page.value * 8))
watch(() => props.deviceId, () => { selected.value = ''; candidates.value = []; page.value = 1 })
async function remove(profile: MBNProfile) {
  if (blocked.value) return
  const original = snapshot.value, id = props.deviceId, replacement = selected.value
  try {
    const result = await ElMessageBox.prompt(`将从模组中删除 ${profile.name}，删除后可能无法恢复。若正在使用，会先切换到你选的替代配置。请输入完整配置名确认。`, '删除模组配置', { inputValidator: value => value === profile.name || '请输入完整配置名', confirmButtonText: '确认删除', cancelButtonText: '取消', type: 'warning' })
    if (props.deviceId !== id || snapshot.value !== original) return
    await start({ kind: 'delete', profile: profile.name, confirm_name: result.value, accept_no_backup: true, replacement }, '操作记录只保存配置选择，不是 MBN 文件备份。确认删除？删除后可能无法恢复。')
  } catch (err) { if (err !== 'cancel' && err !== 'close') report(err) }
}
function move(index: number, delta: number) {
  const next = [...candidates.value], other = index + delta
  if (other < 0 || other >= next.length) return
  ;[next[index], next[other]] = [next[other]!, next[index]!]; candidates.value = next
}
</script>

<template>
  <section class="mbn-panel" aria-label="运营商配置 MBN">
    <header class="mbn-panel-header"><p>MBN 用于模组驻网和原生 VoLTE，不是 WiFi calling 配置。</p><el-button :loading="loading" :disabled="!!active" @click="inspect()"><el-icon><ArrowSync24Regular /></el-icon>{{ snapshot ? '刷新配置' : '读取配置' }}</el-button></header>
    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" />
    <el-alert v-if="active" :title="`${mbnStatusLabel[active.status]} · ${mbnStageLabel[active.stage] || active.stage}`" type="info" :closable="false"><p>关闭页面后任务仍会继续。取消时，会先等待当前命令结束。</p><el-button :disabled="active.cancel_requested" @click="cancel">{{ active.cancel_requested ? '正在取消' : '取消任务' }}</el-button></el-alert>
    <div v-if="!snapshot" class="mbn-empty"><el-icon aria-hidden="true"><Document24Regular /></el-icon><div><strong>尚未读取配置</strong><p>点击“读取配置”查看模组内的 MBN，不会修改设备。</p></div></div>
    <template v-if="snapshot">
      <div class="mbn-summary">
        <div class="mbn-summary-current"><span>当前生效配置</span><strong>{{ current }}</strong></div>
        <div><span>模组与固件</span><strong>{{ snapshot.owner.model }}</strong><small>{{ snapshot.owner.firmware }}</small></div>
        <div><span>模组自动选择</span><strong>{{ snapshot.autosel ? '已开启' : '已关闭' }}</strong><small>AutoSel</small></div>
      </div>
      <el-tabs>
        <el-tab-pane label="模组配置">
          <el-form label-position="top" class="mbn-controls" :disabled="blocked">
            <el-form-item label="使用配置"><el-select v-model="selected" filterable placeholder="选择 MBN 配置"><el-option v-for="p in snapshot.profiles" :key="p.name" :label="p.name" :value="p.name" /></el-select><p class="mbn-field-hint">删除正在使用的配置前，请在这里选择替代配置。</p></el-form-item>
            <el-form-item label="自动选择（AutoSel）"><el-switch v-model="auto" /><span>由模组按 SIM 选择 MBN，不会切换运营商网络。</span></el-form-item>
            <el-button type="primary" :disabled="!snapshot.capabilities.autosel || (!auto && (!selected || !snapshot.capabilities.select))" :loading="submitting" @click="start({ kind: 'apply', profile: selected, autosel: auto }, '应用配置会重启模组，通话、短信和数据将暂时中断。不会解除飞行模式或射频限制。继续？')">应用并重启</el-button>
          </el-form>
          <div class="mbn-list-heading"><h4>模组内配置</h4><span>{{ snapshot.profiles.length }} 项</span></div>
          <ul class="mbn-profiles"><li v-for="p in snapshot.profiles" :key="p.name"><div><strong>{{ p.name }}</strong><small>版本 {{ p.version || '未知' }} · 日期 {{ p.date || '未知' }}</small><div class="mbn-profile-status"><span v-if="p.activated" class="is-current">正在使用</span><span v-if="p.selected">已选择</span><span v-if="!p.selected && !p.activated">未启用</span></div></div><el-button type="danger" plain :disabled="blocked || !snapshot.capabilities.delete" @click="remove(p)">删除</el-button></li></ul>
        </el-tab-pane>
        <el-tab-pane label="文件库"><MBNFileLibrary :assets="assets" :blocked="blocked" :can-import="snapshot.capabilities.add" @refresh="refreshFiles" @error="report" @import="id => start({ kind: 'import', asset_id: id, accept_unknown_compatibility: true }, '请确认文件适用于此模组和固件。不兼容的 MBN 可能导致无法驻网。导入后不会自动启用，继续？')" /></el-tab-pane>
        <el-tab-pane label="SIM 绑定"><MBNPolicyEditor :device-id="deviceId" :snapshot="snapshot" :assets="assets" :blocked="blocked" @error="report" /><el-button :disabled="blocked" @click="bindCurrent">绑定当前配置到此卡</el-button></el-tab-pane>
        <el-tab-pane label="自动试配">
          <p>按顺序尝试所选配置，找到符合条件的就停止并保留。不自动绑定 SIM；失败或取消会尝试恢复原配置。</p>
          <el-form label-position="top" :disabled="blocked">
            <el-form-item label="待测试配置（按勾选顺序尝试）"><el-checkbox-group v-model="candidates"><el-checkbox v-for="p in snapshot.profiles" :key="p.name" :value="p.name">{{ p.name }}</el-checkbox></el-checkbox-group></el-form-item>
            <ol class="mbn-order"><li v-for="(name, index) in candidates" :key="name"><span>{{ name }}</span><el-button :disabled="index === 0" @click="move(index, -1)">上移</el-button><el-button :disabled="index === candidates.length - 1" @click="move(index, 1)">下移</el-button></li></ol>
            <el-form-item label="测试目标"><el-select v-model="goal"><el-option label="稳定驻网" value="registration" /><el-option label="数据连接与网页访问" value="data" /><el-option label="原生 IMS 注册（需处于 VoLTE 模式）" value="ims" /></el-select></el-form-item>
            <template v-if="goal === 'data'"><el-form-item label="测试网址（使用此卡的蜂窝数据访问）"><el-input v-model="probeURL" placeholder="https://…" /></el-form-item><el-checkbox v-model="allowData">允许使用蜂窝数据测试，会产生流量。</el-checkbox></template>
            <el-checkbox v-if="goal === 'ims'" v-model="allowNativeIMS">允许测试原生 IMS 注册，不发短信、不拨号。</el-checkbox>
            <el-form-item label="限定 PLMN（可选，如 46001）"><el-input v-model="target" /></el-form-item>
            <div class="mbn-times"><el-form-item label="模组恢复（秒）"><el-input-number v-model="recovery" :min="1" /></el-form-item><el-form-item label="驻网等待（秒）"><el-input-number v-model="registration" :min="1" /></el-form-item><el-form-item label="连续稳定（秒）"><el-input-number v-model="stability" :min="1" /></el-form-item></div>
            <el-button type="primary" :disabled="!candidates.length || (goal === 'data' && (!allowData || !probeURL)) || (goal === 'ims' && !allowNativeIMS)" @click="start({ kind: 'trial', candidates, goal, target_plmn: target, recovery_seconds: recovery, registration_seconds: registration, stability_seconds: stability, probe_url: probeURL, allow_data: allowData, allow_native_ims: allowNativeIMS }, '测试会逐个切换配置并重启模组，期间设备会离线。只验证所选目标，不测试通话和短信。确认开始？')">开始试配</el-button>
          </el-form>
        </el-tab-pane>
      </el-tabs>
    </template>
    <section class="mbn-history"><header><h4>操作记录</h4><el-button @click="refreshJobs()">刷新记录</el-button></header><p v-if="!operations.length">暂无操作记录</p><article v-for="op in history" :key="op.id"><header><strong>{{ mbnKindLabel[op.request.kind] || op.request.kind }} · {{ mbnStatusLabel[op.status] || op.status }}</strong><time>{{ new Date(op.created_at).toLocaleString() }}</time></header><p>{{ mbnStageLabel[op.stage] || op.stage }}</p><p v-if="op.error" role="alert" class="mbn-error">{{ op.error }}</p><p v-if="op.warning">{{ op.warning }}</p><ul v-if="op.results?.length"><li v-for="result in op.results" :key="result.profile">{{ result.profile }}：{{ result.passed ? '达到目标' : result.error }} {{ result.plmn }}</li></ul><el-button v-if="op.original?.profiles?.length && !['queued', 'running', 'cancelling'].includes(op.status)" :disabled="blocked" @click="start({ kind: 'restore', restore_operation: op.id }, '仅在同一模组、同一 SIM 上恢复此任务之前的 MBN 和 AutoSel。可能需要重启；不能恢复已删除的二进制。继续？')">恢复此任务前配置</el-button></article><el-pagination v-if="operations.length > 8" v-model:current-page="page" :page-size="8" :total="operations.length" layout="prev, pager, next" /></section>
  </section>
</template>

<style scoped>
.mbn-panel { display: grid; grid-template-columns: minmax(0, 1fr); gap: 22px; color: var(--ui-text); min-width: 0; }
header { display: flex; gap: 12px; flex-wrap: wrap; align-items: center; justify-content: space-between; }
h4, p { margin: 0; } h4 { font-size: var(--ui-font-body); font-weight: 650; } p { line-height: 1.6; color: var(--ui-text-muted); }
.mbn-panel-header > p { flex: 1; min-width: 200px; max-width: 60ch; font-size: var(--ui-font-body-sm); }
.mbn-panel-header :deep(.el-icon) { margin-right: 6px; }
.mbn-empty { display: flex; align-items: center; gap: 12px; padding: 16px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-md); background: var(--ui-surface-muted); }
.mbn-empty > .el-icon { font-size: 24px; color: var(--ui-text-muted); }
.mbn-empty strong { font-size: var(--ui-font-body-sm); }
.mbn-empty p { margin-top: 4px; font-size: var(--ui-font-body-sm); }
.mbn-summary { display: grid; grid-template-columns: minmax(0, 1.5fr) minmax(0, 1fr) minmax(0, .6fr); border: 1px solid var(--ui-border); border-radius: var(--ui-radius-md); background: var(--ui-surface-muted); overflow-wrap: anywhere; }
.mbn-summary > div { display: grid; align-content: start; gap: 6px; padding: 16px; min-width: 0; }
.mbn-summary > div + div { border-left: 1px solid var(--ui-border); }
.mbn-summary span { font-size: var(--ui-font-caption); color: var(--ui-text-muted); }
.mbn-summary strong { font-size: var(--ui-font-body-sm); font-weight: 650; }
.mbn-controls { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0 22px; margin: 0 0 26px; }
.mbn-controls > .el-button { grid-column: 1 / -1; justify-self: start; }
.mbn-controls span { margin-left: 12px; color: var(--ui-text-muted); font-size: var(--ui-font-body-sm); }
.mbn-field-hint { margin-top: 6px; font-size: 12px; }
.mbn-list-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.mbn-list-heading span { font-size: var(--ui-font-caption); color: var(--ui-text-muted); }
.mbn-profiles, .mbn-order { padding: 0; list-style: none; } .mbn-profiles li, .mbn-order li { display: flex; align-items: center; gap: 12px; padding: 16px 0; border-bottom: 1px solid var(--ui-border-muted); }
.mbn-profiles { margin: 12px 0 0; }
.mbn-profiles li:last-child { border-bottom: 0; }
.mbn-profiles li > div { display: grid; gap: 6px; flex: 1; overflow-wrap: anywhere; min-width: 0; } small { font-size: var(--ui-font-caption); color: var(--ui-text-muted); }
.mbn-profiles strong { font-size: var(--ui-font-body-sm); font-weight: 650; }
.mbn-profile-status { display: flex; flex-wrap: wrap; gap: 6px; }
.mbn-profile-status span { font-size: var(--ui-font-caption); color: var(--ui-text-muted); }
.mbn-profile-status span + span::before { content: '·'; margin-right: 6px; color: var(--ui-text-muted); }
.mbn-profile-status .is-current { color: var(--ui-success); }
.mbn-order li span { flex: 1; overflow-wrap: anywhere; } .mbn-times { display: flex; gap: 16px; flex-wrap: wrap; }
.mbn-history { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; padding-top: 22px; border-top: 1px solid var(--ui-border); } article { display: grid; gap: 8px; padding: 16px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-md); overflow-wrap: anywhere; }
.mbn-error { color: var(--ui-danger); } time { font-size: var(--ui-font-caption); color: var(--ui-text-muted); }
:deep(.el-form-item__label) { height: auto; margin-bottom: 6px; padding: 0; color: var(--ui-text-muted); font: 700 var(--ui-font-caption) "v-mono", ui-monospace, monospace; line-height: 1.5; letter-spacing: .08em; }
:deep(.el-form-item__content) { min-width: 0; }
:deep(.el-button) { min-height: 44px; margin-left: 0; } :deep(.el-checkbox) { height: auto; min-height: 44px; max-width: 100%; } :deep(.el-checkbox__label) { white-space: normal; overflow-wrap: anywhere; }
:deep(.el-tabs__item.is-active) { color: var(--ui-text); font-weight: 650; }
:deep(.el-tabs) { min-width: 0; }
:deep(.el-tabs__active-bar) { background-color: var(--ui-accent); }
:deep(.el-tab-pane > .el-form) { margin-top: 16px; }
@media (max-width: 600px) {
  .mbn-summary { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); }
  .mbn-summary-current { grid-column: 1 / -1; }
  .mbn-summary > div:nth-child(2) { border-left: 0; }
  .mbn-summary > div:not(:first-child) { border-top: 1px solid var(--ui-border); }
  .mbn-empty { padding: 16px; align-items: flex-start; }
  .mbn-panel-header { flex-direction: column; align-items: stretch; }
  .mbn-panel-header > p { min-width: 0; }
  .mbn-controls { grid-template-columns: minmax(0, 1fr); }
  .mbn-times { display: grid; grid-template-columns: minmax(0, 1fr); gap: 0; }
  .mbn-times :deep(.el-input-number) { width: 100%; }
  :deep(.el-tabs__nav) { display: flex; width: 100%; }
  :deep(.el-tabs__item) { flex: 1; min-width: 0; height: 44px; padding: 0 6px; font-size: 13px; }
  :deep(.el-form-item__content > .el-button) { width: 100%; }
}
</style>
