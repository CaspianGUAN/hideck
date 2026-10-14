import { ref, watch } from 'vue'
import type { PhoneDevice } from '../services/phone'

const STORAGE_KEY = 'hideck_phone_device'

type Options = {
  devices: () => PhoneDevice[]
  isReady: (device: PhoneDevice) => boolean
  storage: () => Pick<Storage, 'getItem' | 'setItem'>
  onStorageError: (error: unknown) => void
}

export function usePhoneDeviceSelection(options: Options) {
  let preferredDevice = ''
  try {
    preferredDevice = options.storage().getItem(STORAGE_KEY) || ''
  } catch (error) {
    options.onStorageError(error)
  }
  const selectedDevice = ref('')

  watch(options.devices, (devices) => {
    if (devices.some((device) => device.id === selectedDevice.value)) return
    selectedDevice.value = devices.find((device) => device.id === preferredDevice)?.id
      || devices.find(options.isReady)?.id || devices[0]?.id || ''
  }, { immediate: true })

  // Persist explicit choices only: an empty list or a disconnected device must
  // not erase the preference while the server is restarting.
  function rememberDevice(value: unknown) {
    if (typeof value !== 'string' || !options.devices().some((device) => device.id === value)) return
    preferredDevice = value
    selectedDevice.value = value
    try {
      options.storage().setItem(STORAGE_KEY, value)
    } catch (error) {
      options.onStorageError(error)
    }
  }

  return { selectedDevice, rememberDevice }
}
