/** 通话方式：wifi 软件 VoWiFi；cellular 软件 IMS 走蜂窝数据；volte 模组原生 IMS。 */
export function normalizePhoneMode(mode?: string | null): string {
  const value = (mode ?? 'wifi').trim()
  return value || 'wifi'
}

export function isNativeVoLTEMode(mode?: string | null): boolean {
  return normalizePhoneMode(mode) === 'volte'
}

export function phoneModeCampsOnCell(mode?: string | null): boolean {
  const value = normalizePhoneMode(mode)
  return value === 'cellular' || value === 'volte' || value === 'modem_voice'
}

export function isWifiCallingEnabled(mode?: string | null, phoneEnabled?: boolean): boolean {
  return !!phoneEnabled && !phoneModeCampsOnCell(mode)
}

/** Ut/XCAP 只挂在软件 IMS 上；原生 VoLTE 和未开 WiFi calling 的设备不展示。 */
export function deviceSupportsUt(device?: {
  vowifi_enabled?: boolean
  vowifi_active?: boolean
  phone_mode?: string
} | null): boolean {
  if (!device || isNativeVoLTEMode(device.phone_mode) || device.phone_mode === 'modem_voice') return false
  return device.vowifi_enabled === true || device.vowifi_active === true
}

export function softwareIMSBlocked(device?: { software_ims_blocked?: boolean } | null): boolean {
  return device?.software_ims_blocked === true
}

export function phoneModeLabel(mode?: string | null): string {
  const value = normalizePhoneMode(mode)
  if (value === 'volte') return 'VoLTE'
  if (value === 'modem_voice') return '模组直拨'
  if (value === 'cellular') return '蜂窝数据'
  return 'WiFi calling'
}
