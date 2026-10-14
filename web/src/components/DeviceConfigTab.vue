<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ArrowSync24Regular,
  Router24Regular,
  Delete24Regular,
  Save24Regular
} from '@vicons/fluent'
import type { DeviceConfigDTO, DeviceOverviewItem } from '../types/api'
import { normalizeManagedDeviceBackend } from '../utils/deviceBackend'
import MBNPanel from './MBNPanel.vue'

const props = defineProps<{
  editConfig: DeviceConfigDTO | null
  deviceStatus?: DeviceOverviewItem | null
  saving: boolean
  deleting: boolean
  retryingPin: boolean
}>()

const emit = defineEmits<{
  save: []
  delete: []
  retryPin: []
}>()

const activeControlDevice = computed(() => props.deviceStatus?.control_device || props.editConfig?.control_device)
const activeInterface = computed(() => props.deviceStatus?.interface || props.editConfig?.interface)
const activeATPort = computed(() => props.deviceStatus?.at_port || props.editConfig?.at_port)
const activeUsbPath = computed(() => props.deviceStatus?.usb_path || props.editConfig?.usb_path)

const configuredBackend = computed(() => String(props.editConfig?.device_backend || '').toLowerCase())
const isManagedBackend = computed(
  () => normalizeManagedDeviceBackend(configuredBackend.value) !== null
)
const isPCSCBackend = computed(() => configuredBackend.value === 'pcsc')
const expandedSettings = ref<string[]>([])
</script>

<template>
  <section class="config-workspace">
    <header class="config-workspace-header">
      <div class="config-heading-icon" aria-hidden="true">
        <el-icon><Router24Regular /></el-icon>
      </div>
      <div>
        <span>DEVICE CONFIGURATION</span>
        <h2>设备配置</h2>
        <p>修改设备名称和连接协议，端口由系统自动识别。</p>
      </div>
    </header>

    <div v-if="editConfig" class="config-form-surface">
      <div class="config-columns">
        <section>
          <h3>基本信息</h3>
          <div class="config-field">
            <label>设备名称</label>
            <el-input v-model="editConfig.name" placeholder="显示名称" />
          </div>
          <div class="config-field">
            <label>设备 ID</label>
            <el-input v-model="editConfig.id" disabled />
          </div>
          <div class="config-field">
            <label>{{ isPCSCBackend ? '读卡器名称' : 'IMEI 绑定' }}</label>
            <el-input :model-value="isPCSCBackend ? editConfig.pcsc_reader_name : editConfig.modem_imei" disabled placeholder="自动识别（添加时绑定）" />
          </div>

          <h3>自动探测端口（只读）</h3>
          <div class="config-field">
            <label>控制设备</label>
            <el-input :model-value="activeControlDevice || ''" disabled placeholder="由系统自动探测" />
          </div>
          <div v-if="!isPCSCBackend" class="config-field">
            <label>AT 端口</label>
            <el-input :model-value="activeATPort || ''" disabled placeholder="由系统自动探测" />
          </div>
        </section>

        <section>
          <h3>连接协议</h3>
          <div class="config-protocol-field">
          <div>
              <strong>设备运行模式</strong>
              <small>切换协议后，设备会重新连接。</small>
          </div>
          <el-radio-group
            v-if="isManagedBackend"
            v-model="editConfig.device_backend"
            size="small"
            :disabled="saving"
            aria-label="设备运行模式"
          >
            <el-radio-button value="qmi">QMI</el-radio-button>
            <el-radio-button value="mbim">MBIM</el-radio-button>
          </el-radio-group>
          <el-select v-else v-model="editConfig.device_backend" style="width: 120px" disabled>
            <el-option v-if="isPCSCBackend" label="PC/SC" value="pcsc" />
            <el-option label="AT" value="at" />
          </el-select>
        </div>

          <div v-if="!isPCSCBackend" class="config-field">
            <label>网络接口</label>
            <el-input :model-value="activeInterface || ''" disabled placeholder="由系统自动探测" />
          </div>
          <div class="config-field">
            <label>USB 路径</label>
            <el-input :model-value="activeUsbPath || ''" disabled placeholder="由系统自动探测" />
          </div>
          <div v-if="isPCSCBackend" class="config-field">
            <label>SIM PIN 环境变量名</label>
            <el-input v-model="editConfig.sim_pin_env" placeholder="例如 HIDECK_SIM_PIN_READER1" />
          </div>
        </section>
      </div>

      <p class="config-notice">更改连接协议后，设备会短暂离线。</p>
      <footer class="config-actions">
        <el-button type="danger" plain :loading="deleting" @click="emit('delete')">
          <el-icon><Delete24Regular /></el-icon>
          删除设备
        </el-button>
        <div class="config-primary-actions">
          <el-button v-if="isPCSCBackend" type="warning" plain :loading="retryingPin" :disabled="saving" @click="emit('retryPin')">
            <el-icon><ArrowSync24Regular /></el-icon>
            重新尝试 SIM PIN
          </el-button>
          <el-button type="primary" :loading="saving" :disabled="retryingPin" @click="emit('save')" class="!border-0">
            <el-icon><Save24Regular /></el-icon>
            保存配置
          </el-button>
        </div>
      </footer>
    </div>
    <el-collapse v-if="editConfig && !isPCSCBackend" v-model="expandedSettings" class="mbn-settings">
      <el-collapse-item name="mbn">
        <template #title>
          <span class="mbn-entry">
            <span class="mbn-entry-copy">
              <strong>运营商配置（MBN）</strong>
              <small>切换模组配置、上传文件或绑定 SIM。</small>
            </span>
            <span class="mbn-entry-action">{{ expandedSettings.includes('mbn') ? '收起' : '展开' }}</span>
          </span>
        </template>
        <MBNPanel :device-id="editConfig.id" />
      </el-collapse-item>
    </el-collapse>
  </section>
</template>

<style scoped>
.config-workspace-header { min-height: 82px; display: flex; align-items: center; gap: 12px; }
.config-heading-icon { width: 44px; height: 44px; display: grid; place-items: center; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-md); color: var(--ui-primary); font-size: 22px; }
.config-workspace-header span { color: var(--ui-primary); font: 700 var(--ui-font-caption) "v-mono", ui-monospace, monospace; letter-spacing: .15em; }
.config-workspace-header h2 { margin: 3px 0 0; color: var(--ui-text); font-size: 20px; font-weight: 650; }
.config-workspace-header p { margin: 3px 0 0; color: var(--ui-text-muted); font-size: var(--ui-font-body-sm); }

.config-form-surface { overflow: hidden; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-xl); background: var(--ui-surface-strong); }
.config-columns { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
.config-columns > section { min-width: 0; padding: 22px; }
.config-columns > section + section { border-left: 1px solid var(--ui-border); }
.config-columns h3 { margin: 0 0 14px; color: var(--ui-text); font-size: var(--ui-font-body); font-weight: 650; }
.config-columns h3:not(:first-child) { margin-top: 26px; padding-top: 18px; border-top: 1px solid var(--ui-border-muted); }
.config-field { margin-top: 13px; }
.config-field label { display: block; margin-bottom: 5px; color: var(--ui-text-muted); font: 700 var(--ui-font-caption) "v-mono", ui-monospace, monospace; letter-spacing: .08em; }
.config-protocol-field { min-height: 62px; padding: 11px 12px; display: flex; align-items: center; justify-content: space-between; gap: 12px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-md); background: var(--ui-surface-muted); }
.config-protocol-field strong { display: block; color: var(--ui-text); font-size: var(--ui-font-body-sm); }
.config-protocol-field small { display: block; margin-top: 2px; color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.config-notice { margin: 0; padding: 11px 22px; border-top: 1px solid var(--ui-border); border-bottom: 1px solid var(--ui-border); background: color-mix(in srgb, var(--ui-warning) 6%, var(--ui-surface)); color: var(--ui-text-muted); font-size: var(--ui-font-body-sm); }
.mbn-settings { margin-top: 18px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-xl); overflow: hidden; background: var(--ui-surface-strong); }
.mbn-settings :deep(.el-collapse-item__header) { height: auto; min-height: 76px; padding: 18px 22px; line-height: 1.5; border: 0; background: var(--ui-surface-strong); color: var(--ui-text); }
.mbn-settings :deep(.el-collapse-item__header:hover) { background: var(--ui-surface-muted); }
.mbn-settings :deep(.el-collapse-item__header:focus-visible) { outline: 2px solid var(--ui-accent); outline-offset: -3px; }
.mbn-settings :deep(.el-collapse-item__title) { min-width: 0; flex: 1; }
.mbn-settings :deep(.el-collapse-item__wrap) { border: 0; background: var(--ui-surface-strong); }
.mbn-settings :deep(.el-collapse-item__content) { padding: 22px; border-top: 1px solid var(--ui-border); }
.mbn-entry { display: flex; flex: 1; align-items: center; gap: 12px; min-width: 0; text-align: left; }
.mbn-entry-copy { display: grid; flex: 1; gap: 4px; min-width: 0; }
.mbn-entry-copy strong { color: var(--ui-text); font-size: var(--ui-font-body); font-weight: 650; }
.mbn-entry-copy small { color: var(--ui-text-muted); font-size: var(--ui-font-body-sm); font-weight: 400; }
.mbn-entry-action { margin: 0 12px; color: var(--ui-text-muted); font-size: var(--ui-font-body-sm); font-weight: 400; }
.config-actions { min-height: 76px; padding: 16px 22px; display: flex; align-items: center; justify-content: space-between; gap: 12px; border-top: 1px solid var(--ui-border); }
.config-actions :deep(.el-button) { min-height: 44px; }
.config-actions :deep(.el-icon) { margin-right: 6px; }
.config-primary-actions { display: flex; align-items: center; gap: 10px; }

@media (max-width: 760px) {
  .config-columns { grid-template-columns: minmax(0, 1fr); }
  .config-columns > section { padding: 18px 16px; }
  .config-columns > section + section { border-top: 1px solid var(--ui-border); border-left: 0; }
  .mbn-settings :deep(.el-collapse-item__header), .mbn-settings :deep(.el-collapse-item__content) { padding: 18px 16px; }
}

@media (max-width: 520px) {
  .config-workspace-header { align-items: flex-start; }
  .config-protocol-field { align-items: stretch; flex-direction: column; }
  .config-actions { align-items: stretch; flex-direction: column-reverse; }
  .config-actions :deep(.el-button) { width: 100%; min-height: 44px; margin-left: 0; }
  .config-primary-actions { width: 100%; flex-direction: column-reverse; }
  .mbn-entry-action { display: none; }
}
</style>
