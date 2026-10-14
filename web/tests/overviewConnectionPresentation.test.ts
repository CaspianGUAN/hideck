import assert from 'node:assert/strict'
import test from 'node:test'
import type { DeviceOverviewItem } from '../src/types/api'
import { createOverviewConnectionPresentation } from '../src/utils/overviewConnectionPresentation'

function device(partial: Partial<DeviceOverviewItem> = {}): DeviceOverviewItem {
  return {
    id: 'wwan0',
    name: 'ct',
    running: true,
    healthy: true,
    network_connected: false,
    public_ip: '',
    network_enabled: false,
    modem: { iccid: '8986000000000000000' },
    backend_mode: 'qmi',
    interface: 'wwan1',
    ...partial
  }
}

test('registered IMS with unverified reception is not shown as building or failed', () => {
  const presentation = createOverviewConnectionPresentation(device({
    vowifi_enabled: true, vowifi_active: true,
    vowifi_runtime: { sim_ready: true, access_ready: true, tunnel_ready: true, ims_ready: true,
      sms_mo_ready: true, sms_ready: false, sms_rx_pending: true }
  }))
  assert.equal(presentation.tone, 'is-pending')
  assert.equal(presentation.detail, 'IMS 已注册，短信接收待验证')
  assert.equal(presentation.stages[5]?.ready, undefined)
})

test('volte registered is not shown as wifi calling waiting', () => {
  const presentation = createOverviewConnectionPresentation(device({
    phone_mode: 'volte',
    vowifi_enabled: true,
    vowifi_active: false,
    native_volte: {
      phase: 'registered',
      ims_registered: true,
      lte_registered: true,
      ims_pdn_active: true,
      voice_available: true,
      plmn: '460-11',
      mbn_name: 'VoLTE_OPNMKT_CT'
    }
  }))

  assert.equal(presentation.kind, 'volte')
  assert.equal(presentation.eyebrow, 'VOLTE')
  assert.equal(presentation.title, 'VoLTE 已注册')
  assert.equal(presentation.detail, '460-11 · VoLTE_OPNMKT_CT')
  assert.equal(presentation.tone, 'is-ready')
  assert.equal(presentation.pathIsFlowing, true)
  assert.deepEqual(presentation.metrics.find((item) => item.label === '接入方式'), {
    label: '接入方式',
    value: 'VoLTE',
    hint: ''
  })
  assert.deepEqual(presentation.stages.map((stage) => [stage.key, stage.ready]), [
    ['SIM', true],
    ['LTE', true],
    ['PDN', true],
    ['IMS', true],
    ['Voice', true]
  ])
})

test('volte still registering does not mark unfinished stages as failed', () => {
  const presentation = createOverviewConnectionPresentation(device({
    phone_mode: 'volte',
    vowifi_enabled: true,
    native_volte: {
      phase: 'registering',
      lte_registered: true,
      ims_pdn_active: false,
      ims_registered: false,
      provision_stage: 'verify'
    }
  }))

  assert.equal(presentation.title, 'VoLTE 正在注册')
  assert.equal(presentation.tone, 'is-pending')
  assert.deepEqual(presentation.stages.map((stage) => stage.ready), [true, true, undefined, undefined, undefined])
})

test('wifi calling presentation stays on the ePDG path', () => {
  const presentation = createOverviewConnectionPresentation(device({
    phone_mode: 'wifi',
    vowifi_enabled: true,
    vowifi_active: false
  }))

  assert.equal(presentation.kind, 'wifi')
  assert.equal(presentation.eyebrow, 'WI-FI CALLING')
  assert.equal(presentation.title, 'VoWiFi 等待连接')
  assert.equal(presentation.metrics.find((item) => item.label === '接入方式')?.value, 'Wi-Fi Calling')
})

test('wifi calling exposes degraded SMS receive readiness without failing the IMS link', () => {
  const presentation = createOverviewConnectionPresentation(device({
    phone_mode: 'wifi',
    vowifi_enabled: true,
    vowifi_active: true,
    vowifi_runtime: {
      sim_ready: true,
      access_ready: true,
      tunnel_ready: true,
      ims_ready: true,
      sms_ready: false,
      sms_mo_ready: true,
      sms_ready_reason: 'IMS SMS receiver is not ready'
    }
  }))

  assert.equal(presentation.title, 'VoWiFi 已连接')
  assert.equal(presentation.tone, 'is-pending')
  assert.equal(presentation.pathIsFlowing, true)
  assert.deepEqual(presentation.stages.map((stage) => stage.ready), [true, true, true, true, true, false])
  assert.equal(
    presentation.metrics.some((item) => item.value === 'IMS SMS receiver is not ready'), true
  )
  assert.equal(presentation.detail, 'IMS SMS receiver is not ready')
})

test('wifi calling stays on the wifi path when the service is off', () => {
  const presentation = createOverviewConnectionPresentation(device({
    phone_mode: 'wifi',
    vowifi_enabled: false
  }))

  assert.equal(presentation.kind, 'wifi')
  assert.equal(presentation.eyebrow, 'WI-FI CALLING')
  assert.equal(presentation.title, 'WiFi calling 未开启')
  assert.equal(presentation.detail, '打开启动开关后才会注册')
  assert.doesNotMatch(presentation.detail, /蜂窝网络/)
})

test('cellular with software phone off does not claim wifi calling', () => {
  const presentation = createOverviewConnectionPresentation(device({
    phone_mode: 'cellular',
    vowifi_enabled: false
  }))

  assert.equal(presentation.kind, 'cellular')
  assert.equal(presentation.title, '软件电话未开启')
  assert.match(presentation.detail, /蜂窝驻网/)
})
