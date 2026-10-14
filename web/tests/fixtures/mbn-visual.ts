// Visual-only fixture. No real HTTP request or modem mutation is performed.
import { createApp, h } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import '../../src/style.css'
import { api } from '../../src/stores/auth'
import MBNPanel from '../../src/components/MBNPanel.vue'
import DeviceConfigTab from '../../src/components/DeviceConfigTab.vue'
import type { DeviceConfigDTO } from '../../src/types/api'

const query = new URLSearchParams(location.search)
if (query.has('dark')) document.documentElement.classList.add('dark')
const owner = { device_id: 'fixture', imei: 'fixture-imei', iccid: 'fixture-123456', model: 'EC20F', firmware: 'EC20CEFAR02A11M4G', generation: 1, identity_generation: 1 }
const profiles = ['Volte_OpenMkt-Commercial-CMCC', 'OpenMkt-Commercial-CU', 'OpenMkt-Commercial-CT'].map((name, index) => ({ index, name, selected: index === 0, activated: index === 0, version: '0x05011501', date: '000000000', origin: 'unknown' }))
api.defaults.adapter = async config => {
  if (config.method !== 'get') throw new Error('只读视觉夹具：没有连接模组，禁止写入')
  const data = config.url?.endsWith('/mbn') ? { owner, profiles, autosel: true, capabilities: { list: true, select: true, autosel: true, add: true, delete: true, export: false } } : { items: [] }
  return { config, data, status: 200, statusText: 'fixture', headers: {} }
}
const editConfig: DeviceConfigDTO = { id: 'fixture', name: '测试模组', modem_imei: 'fixture-imei', device_backend: 'qmi', interface: 'wwan0', at_port: '/dev/ttyUSB2', control_device: '/dev/cdc-wdm0', usb_path: '3-2.2' }
createApp({ render: () => h('main', { style: { maxWidth: query.has('mobile') ? '375px' : '960px', margin: '24px auto', padding: '16px', background: 'var(--el-bg-color)' } }, [h('p', { style: 'margin-bottom:16px' }, '只读 UI 测试数据 · 未连接真实模组'), query.has('config') ? h(DeviceConfigTab, { editConfig, saving: false, deleting: false, retryingPin: false }) : h(MBNPanel, { deviceId: 'fixture' })]) }).use(ElementPlus).mount('#app')
