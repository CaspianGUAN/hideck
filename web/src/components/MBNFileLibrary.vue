<script setup lang="ts">
import { ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import { ArrowUpload24Regular, Document24Regular } from '@vicons/fluent'
import { mbnService, type MBNAsset } from '../services/mbn'
const props = defineProps<{ assets: MBNAsset[]; blocked: boolean; canImport: boolean }>()
const emit = defineEmits<{ refresh: []; error: [unknown]; import: [string] }>()
const uploading = ref(false), model = ref(''), firmware = ref(''), source = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
const fileName = ref(''), fileSaved = ref(false)
async function upload(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]
  if (!file || props.blocked || uploading.value) return
  fileName.value = file.name
  fileSaved.value = false
  uploading.value = true
  try { await mbnService.upload(file, { model: model.value, firmware: firmware.value, source: source.value }); fileSaved.value = true; emit('refresh') }
  catch (err) { emit('error', err) }
  finally { uploading.value = false; input.value = '' }
}
async function download(asset: MBNAsset) {
  try {
    const blob = await mbnService.download(asset.id), url = URL.createObjectURL(blob)
    const link = document.createElement('a'); link.href = url; link.download = asset.name; link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err) { emit('error', err) }
}
async function remove(asset: MBNAsset) {
  try {
    await ElMessageBox.confirm(`删除 ${asset.name}？只删除 HiDeck 保存的文件，不影响模组内的配置。正在使用的文件无法删除。`, '删除文件', { type: 'warning' })
    await mbnService.deleteFile(asset.id); emit('refresh')
  } catch (err) { if (err !== 'cancel' && err !== 'close') emit('error', err) }
}
</script>
<template>
  <div class="mbn-files">
    <p class="mbn-file-note">上传只保存文件。要写入模组，请在上传后点击“导入模组”。</p>
    <el-form label-position="top" class="mbn-file-form" :disabled="blocked || uploading">
      <el-form-item label="适用型号（可选）"><el-input v-model="model" placeholder="例如 EC20F" /></el-form-item>
      <el-form-item label="适用固件（可选）"><el-input v-model="firmware" placeholder="文件适用的固件版本" /></el-form-item>
      <el-form-item label="文件来源" class="mbn-source-field"><el-input v-model="source" placeholder="下载地址或提供方" /></el-form-item>
      <div class="mbn-upload">
        <el-icon class="mbn-upload-icon" aria-hidden="true"><ArrowUpload24Regular /></el-icon>
        <div class="mbn-upload-copy">
          <strong>上传 MBN 文件</strong>
          <p>选择 .mbn 文件，保留原始内容。</p>
          <span v-if="fileName" class="mbn-selected-file" aria-live="polite">{{ uploading ? '正在上传：' : fileSaved ? '已保存：' : '上次选择：' }}{{ fileName }}</span>
        </div>
        <el-button :loading="uploading" :disabled="blocked || uploading" @click="fileInput?.click()">{{ uploading ? '正在上传' : '选择文件' }}</el-button>
        <input ref="fileInput" class="mbn-file-input" type="file" accept=".mbn" :disabled="blocked || uploading" @change="upload" />
      </div>
    </el-form>
    <p class="mbn-file-hint">请自行确认型号和固件是否匹配。这里不能导出模组内置的 MBN。</p>
    <div v-if="!assets.length" class="mbn-files-empty"><el-icon aria-hidden="true"><Document24Regular /></el-icon><strong>暂无文件</strong><p>点击“选择文件”上传 MBN。</p></div>
    <article v-for="asset in assets" :key="asset.id"><strong>{{ asset.name }}</strong><span>{{ asset.size.toLocaleString() }} 字节 · {{ asset.model || '型号未声明' }} · {{ asset.firmware || '固件未声明' }}</span><small>SHA-256：{{ asset.sha256 }}</small><span>来源：{{ asset.source || '未提供' }}</span><div class="mbn-file-actions"><el-button @click="download(asset)">下载原文件</el-button><el-button :disabled="blocked || !canImport" @click="emit('import', asset.id)">导入模组</el-button><el-button type="danger" plain :disabled="blocked" @click="remove(asset)">删除文件</el-button></div></article>
  </div>
</template>
<style scoped>
.mbn-files, article { display: grid; gap: 16px; min-width: 0; }
p { margin: 0; line-height: 1.6; color: var(--ui-text-muted); }
.mbn-file-note { font-size: 13px; max-width: 70ch; }
.mbn-file-form { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0 16px; }
.mbn-source-field, .mbn-upload { grid-column: 1 / -1; }
.mbn-upload { display: flex; align-items: center; gap: 12px; padding: 16px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-md); background: var(--ui-surface-muted); }
.mbn-upload-icon { width: 24px; height: 24px; flex-shrink: 0; color: var(--ui-text-muted); font-size: 24px; }
.mbn-upload-copy { flex: 1; min-width: 0; }
.mbn-upload-copy strong { font-size: var(--ui-font-body-sm); font-weight: 650; }
.mbn-upload-copy p { margin-top: 4px; font-size: var(--ui-font-body-sm); }
.mbn-selected-file { display: block; margin-top: 8px; font-size: 12px; overflow-wrap: anywhere; color: var(--ui-text); }
.mbn-file-input { display: none; }
.mbn-file-hint { font-size: 12px; }
.mbn-files-empty { display: grid; justify-items: center; gap: 8px; padding: 24px 16px; border-radius: var(--ui-radius-md); background: var(--ui-surface-muted); text-align: center; }
.mbn-files-empty .el-icon { color: var(--ui-text-muted); font-size: 24px; }
.mbn-files-empty strong { font-size: var(--ui-font-body-sm); font-weight: 650; }
.mbn-files-empty p { max-width: 42ch; font-size: 13px; }
article { padding: 16px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-md); overflow-wrap: anywhere; }
.mbn-file-actions { display: flex; gap: 8px; flex-wrap: wrap; }
small { font-size: 13px; color: var(--ui-text-muted); }
:deep(.el-button) { min-height: 44px; margin-left: 0; }
@media (max-width: 600px) {
  .mbn-file-form { grid-template-columns: minmax(0, 1fr); }
  .mbn-upload { display: grid; grid-template-columns: 24px minmax(0, 1fr); gap: 12px; }
  .mbn-upload > .el-button { grid-column: 1 / -1; }
  .mbn-file-actions .el-button { flex: 1 1 120px; }
}
</style>
