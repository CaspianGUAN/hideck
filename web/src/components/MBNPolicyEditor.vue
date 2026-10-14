<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { mbnService, type MBNAsset, type MBNPolicy, type MBNSnapshot } from '../services/mbn'
const props = defineProps<{ deviceId: string; snapshot: MBNSnapshot; assets: MBNAsset[]; blocked: boolean }>()
const emit = defineEmits<{ error: [unknown] }>()
const scope = ref('sim'), mode = ref('inherit'), profile = ref(''), asset = ref(''), allowImport = ref(false), saving = ref(false)
const policies = ref<MBNPolicy[]>([])
const key = computed(() => scope.value === 'sim' ? `sim:${props.snapshot.owner.iccid}` : `device:${props.snapshot.owner.imei}`)
const simTail = computed(() => props.snapshot.owner.iccid.slice(-6))
let epoch = 0
function populate() {
  const policy = policies.value.find(item => item.key === key.value)
  mode.value = policy?.mode || 'inherit'; profile.value = policy?.profile || ''; asset.value = policy?.asset_id || ''; allowImport.value = policy?.allow_import || false
}
watch(() => [props.deviceId, props.snapshot.owner.iccid, props.snapshot.owner.imei], async () => {
  const current = ++epoch
  saving.value = false; policies.value = []; populate()
  try { const result = await mbnService.policies(props.deviceId); if (current === epoch) { policies.value = result; populate() } }
  catch (err) { if (current === epoch) emit('error', err) }
}, { immediate: true })
watch(key, populate)
async function save() {
  if (saving.value || props.blocked) return
  const id = props.deviceId, current = epoch, owner = props.snapshot.owner
  const selected = props.snapshot.profiles.find(item => item.name === profile.value)
  const policy = { key: key.value, mode: mode.value, model: owner.model, firmware: owner.firmware, profile: profile.value, version: selected?.version || '', asset_id: asset.value, allow_import: allowImport.value }
  saving.value = true
  try {
    await ElMessageBox.confirm('下次识别到这张卡或模组时会应用此设置，可能重启模组。固定配置不适用于当前型号或固件时会报错，不会切换到其他配置。', '保存绑定设置', { type: 'warning' })
    if (current !== epoch) return
    await mbnService.savePolicy(id, policy)
    if (current !== epoch) return
    policies.value = [...policies.value.filter(item => item.key !== policy.key), policy]
    ElMessage.success('绑定设置已保存')
  } catch (err) { if (err !== 'cancel' && err !== 'close' && current === epoch) emit('error', err) }
  finally { if (current === epoch) saving.value = false }
}
onUnmounted(() => { epoch++ })
</script>
<template>
  <el-form label-position="top" :disabled="blocked || saving">
    <p>{{ scope === 'sim' ? `仅对当前 SIM 生效（尾号 ${simTail}），其他卡不受影响。` : '没有单独绑定的 SIM 使用此设置。' }}</p>
    <el-form-item label="应用范围"><el-radio-group v-model="scope"><el-radio-button value="sim">当前 SIM</el-radio-button><el-radio-button value="device">模组默认</el-radio-button></el-radio-group></el-form-item>
    <el-form-item label="配置方式"><el-select v-model="mode"><el-option :label="scope === 'sim' ? '使用模组默认设置' : '保持模组现有设置'" value="inherit" /><el-option label="由模组自动选择（AutoSel）" value="autosel" /><el-option label="固定使用一个 MBN" value="fixed" /></el-select></el-form-item>
    <template v-if="mode === 'fixed'"><el-form-item label="固定配置"><el-select v-model="profile" filterable placeholder="选择 MBN 配置"><el-option v-for="item in snapshot.profiles" :key="item.name" :label="item.name" :value="item.name" /></el-select></el-form-item><el-form-item label="备用 MBN 文件（可选）"><el-select v-model="asset" clearable placeholder="从文件库选择"><el-option v-for="item in assets" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item><el-checkbox v-model="allowImport" :disabled="!asset">模组缺少此配置时允许导入备用文件；我已确认文件兼容。</el-checkbox></template>
    <el-button type="primary" :loading="saving" :disabled="mode === 'fixed' && !profile" @click="save">保存绑定设置</el-button>
  </el-form>
</template>
<style scoped>
.el-form { display: grid; gap: 12px; margin-bottom: 20px; } .el-button { min-height: 44px; justify-self: start; } :deep(.el-checkbox__label) { white-space: normal; line-height: 1.6; }
</style>
