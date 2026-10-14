import { t } from '../i18n'
import { smsReceptionDetail, smsReceptionPending } from './smsReceptionPresentation'
import type { DeviceOverviewItem } from '../types/api'
import {
  createDashboardStages,
  formatDashboardSignal,
  hasDashboardSignal,
  VOWIFI_CORE_STAGE_COUNT
} from './dashboardPresentation'
import { displaySignalDbm } from './signalPresentation'
import { isNativeVoLTEMode, phoneModeCampsOnCell } from './phoneMode'
import { modemVoicePresentation, modemVoiceStages } from './modemVoicePresentation'
import {
  createVoLTEStages,
  volteRegistered,
  volteServiceState
} from './volteConnectionPresentation'

export type OverviewConnectionKind = 'wifi' | 'volte' | 'cellular'

export type OverviewConnectionStage = Readonly<{
  key: string
  pending?: boolean
  ready: boolean | undefined
}>

export type OverviewConnectionMetric = Readonly<{
  label: string
  value: string
  hint: string
}>

export type OverviewConnectionPresentation = Readonly<{
  kind: OverviewConnectionKind
  eyebrow: string
  title: string
  detail: string
  tone: 'is-idle' | 'is-pending' | 'is-ready' | 'is-failed'
  pathIsFlowing: boolean
  stages: readonly OverviewConnectionStage[]
  metrics: readonly OverviewConnectionMetric[]
}>



export function createOverviewConnectionPresentation(
  device: DeviceOverviewItem | null
): OverviewConnectionPresentation {
  if (device?.phone_mode === 'modem_voice') {
    const state = modemVoicePresentation(device.modem_voice)
    return Object.freeze({ ...state, kind: 'cellular', eyebrow: '模组直拨', pathIsFlowing: false,
      stages: modemVoiceStages(device.modem_voice), metrics: [metric('通话方式', '模组直拨'), metric('设备', device.id)] })
  }
  if (device && isNativeVoLTEMode(device.phone_mode)) {
    return createVoLTEPresentation(device)
  }
  return createVoWiFiOrCellularPresentation(device)
}

function createVoLTEPresentation(device: DeviceOverviewItem): OverviewConnectionPresentation {
  const status = device.native_volte
  const stages = createVoLTEStages(device.modem?.iccid ? true : undefined, status)
  const state = volteServiceState(device.vowifi_enabled === true, status)
  return Object.freeze({
    kind: 'volte',
    eyebrow: 'VOLTE',
    title: state.title,
    detail: state.detail,
    tone: state.tone,
    pathIsFlowing: device.healthy === true && volteRegistered(status) && state.tone !== 'is-failed',
    stages,
    metrics: Object.freeze([
      metric(t('overview.access'), 'VoLTE'),
      metric('PLMN', status?.plmn),
      metric('MBN', status?.mbn_name),
      metric('IMS PDN', status ? (status.ims_pdn_active ? t('overview.imsPdnOn') : t('overview.imsPdnOff')) : t('common.unavailable')),
      metric(t('devices.protocol'), device.backend_mode?.toUpperCase()),
      metric(t('devices.iface'), device.interface)
    ])
  })
}

function createVoWiFiOrCellularPresentation(
  device: DeviceOverviewItem | null
): OverviewConnectionPresentation {
  const runtime = device?.vowifi_runtime
  const stages = createDashboardStages(runtime)
  const coreStages = stages.slice(0, VOWIFI_CORE_STAGE_COUNT)
  const smsStages = stages.slice(VOWIFI_CORE_STAGE_COUNT)
  const hasFailedCoreStage = coreStages.some((stage) => stage.ready === false)
  const hasFailedSMSStage = smsStages.some((stage) => stage.ready === false)
  const hasReadyStage = stages.some((stage) => stage.ready === true)
  const coreStagesReady = coreStages.every((stage) => stage.ready === true)
  const smsStagesReady = smsStages.every((stage) => stage.ready === true)
  const runtimeReason = (hasFailedSMSStage ? runtime?.sms_ready_reason : '')
    || runtime?.last_reason
    || ''
  const protocol = metric(t('devices.protocol'), device?.backend_mode?.toUpperCase())
  const deviceInterface = metric(t('devices.iface'), device?.interface)

  if (!device?.vowifi_enabled) {
    if (!phoneModeCampsOnCell(device?.phone_mode)) {
      return Object.freeze({
        kind: 'wifi',
        eyebrow: 'WI-FI CALLING',
        title: t('overview.wifiOff'),
        detail: t('overview.wifiOffHint'),
        tone: 'is-idle',
        pathIsFlowing: false,
        stages,
        metrics: Object.freeze([
          metric(t('overview.access'), 'Wi-Fi Calling', 'Wi-Fi Calling'),
          protocol,
          deviceInterface
        ])
      })
    }
    const signal = displaySignalDbm(device?.modem?.signal_dbm, device?.modem?.signal_rsrp)
    return Object.freeze({
      kind: 'cellular',
      eyebrow: 'CELLULAR',
      title: t('overview.softphoneOff'),
      detail: t('overview.softphoneOffHint'),
      tone: 'is-idle',
      pathIsFlowing: false,
      stages,
      metrics: Object.freeze([
        {
          label: t('overview.cellularSignal'),
          value: formatDashboardSignal(device?.modem?.signal_dbm, device?.modem?.signal_rsrp),
          hint: hasDashboardSignal(signal) ? signalQuality(signal) : ''
        },
        metric(t('dashboard.publicV4'), device?.public_ip, t('common.unassigned')),
        metric(t('dashboard.publicV6'), device?.public_ipv6, t('common.unassigned')),
        deviceInterface
      ])
    })
  }

  let tone: OverviewConnectionPresentation['tone'] = 'is-idle'
  let title = t('overview.vowifiWait')
  let detail = runtimeReason || t('overview.noLink')
  if (hasFailedCoreStage) {
    tone = 'is-failed'
    title = t('overview.vowifiFailed')
    detail = runtimeReason || t('overview.checkFailed')
  } else if (device.vowifi_active && coreStagesReady && smsStagesReady) {
    tone = 'is-ready'
    title = t('overview.vowifiConnected')
    detail = smsReceptionDetail(runtime) || t('overview.vowifiConnectedHint')
  } else if (device.vowifi_active && coreStagesReady && smsReceptionPending(runtime) && !hasFailedSMSStage) {
    tone = 'is-pending'
    title = t('overview.vowifiConnected')
    detail = smsReceptionDetail(runtime)
  } else if (device.vowifi_active && coreStagesReady && hasFailedSMSStage) {
    tone = 'is-pending'
    title = t('overview.vowifiConnected')
    detail = runtimeReason || t('overview.vowifiSMSDegraded')
  } else if (hasReadyStage) {
    tone = 'is-pending'
    title = t('overview.vowifiBuilding')
    detail = runtimeReason || t('overview.waitStages')
  }

  return Object.freeze({
    kind: 'wifi',
    eyebrow: 'WI-FI CALLING',
    title,
    detail,
    tone,
    pathIsFlowing: device.healthy === true && device.vowifi_active === true && !hasFailedCoreStage,
    stages,
    metrics: Object.freeze([
      metric(t('overview.access'), 'Wi-Fi Calling', 'Wi-Fi Calling'),
      metric(t('overview.dataplane'), runtime?.dataplane_mode),
      protocol,
      deviceInterface,
      metric(t('overview.lastReason'), runtimeReason, t('common.none')),
      metric(t('overview.errorClass'), runtime?.last_error_class, t('common.none'))
    ])
  })
}

function metric(label: string, value?: string | null, empty?: string): OverviewConnectionMetric {
  const text = String(value || '').trim()
  return Object.freeze({ label, value: text || empty || t('common.unavailable'), hint: '' })
}

function signalQuality(value: number): string {
  if (value >= -75) return '优秀'
  if (value >= -90) return '良好'
  if (value >= -105) return '一般'
  return '较弱'
}
