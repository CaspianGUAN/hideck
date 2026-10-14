import assert from 'node:assert/strict'
import test from 'node:test'
import { effectScope, nextTick, ref } from 'vue'
import { usePhoneDeviceSelection } from '../src/composables/usePhoneDeviceSelection'
import type { PhoneDevice } from '../src/services/phone'

const device = (id: string, ready = true): PhoneDevice => ({ id, name: id, iccid: '', voice: { ready } })

function fixture(saved = '') {
  const values = new Map(saved ? [['hideck_phone_device', saved]] : [])
  const devices = ref<PhoneDevice[]>([])
  const errors: unknown[] = []
  const scope = effectScope()
  const selection = scope.run(() => usePhoneDeviceSelection({
    devices: () => devices.value,
    isReady: (item) => item.voice.ready === true,
    storage: () => ({ getItem: (key) => values.get(key) || null, setItem: (key, value) => { values.set(key, value) } }),
    onStorageError: (error) => { errors.push(error) }
  }))!
  return { ...selection, devices, values, errors, scope }
}

test('restores second device after async loading even when it is not ready', async () => {
  const state = fixture('wwan1')
  try {
    assert.equal(state.selectedDevice.value, '')
    state.devices.value = [device('wwan0'), device('wwan1', false)]
    await nextTick()
    assert.equal(state.selectedDevice.value, 'wwan1')
    state.devices.value = [device('wwan1'), device('wwan0')]
    await nextTick()
    assert.equal(state.selectedDevice.value, 'wwan1')
  } finally { state.scope.stop() }
})

test('explicit choice survives remount and empty device refreshes', async () => {
  const state = fixture()
  try {
    state.devices.value = [device('wwan0'), device('wwan1')]
    await nextTick()
    state.rememberDevice('wwan1')
    assert.equal(state.values.get('hideck_phone_device'), 'wwan1')
    state.devices.value = []
    await nextTick()
    assert.equal(state.values.get('hideck_phone_device'), 'wwan1')
    state.devices.value = [device('wwan0'), device('wwan1')]
    await nextTick()
    assert.equal(state.selectedDevice.value, 'wwan1')
    const reloaded = fixture(state.values.get('hideck_phone_device'))
    try {
      reloaded.devices.value = [...state.devices.value]
      await nextTick()
      assert.equal(reloaded.selectedDevice.value, 'wwan1')
    } finally { reloaded.scope.stop() }
  } finally { state.scope.stop() }
})

test('missing device uses available device without overwriting preference', async () => {
  const state = fixture('removed')
  try {
    state.devices.value = [device('wwan0', false), device('wwan1')]
    await nextTick()
    assert.equal(state.selectedDevice.value, 'wwan1')
    assert.equal(state.values.get('hideck_phone_device'), 'removed')
    state.rememberDevice(undefined)
    state.rememberDevice('missing')
    assert.equal(state.values.get('hideck_phone_device'), 'removed')
  } finally { state.scope.stop() }
})

test('restricted storage reports failure without preventing device selection', () => {
  const scope = effectScope()
  const errors: unknown[] = []
  try {
    const selection = scope.run(() => usePhoneDeviceSelection({
      devices: () => [device('wwan0'), device('wwan1')],
      isReady: () => true,
      storage: () => { throw new Error('storage denied') },
      onStorageError: (error) => { errors.push(error) }
    }))!
    assert.equal(errors.length, 1)
    selection.rememberDevice('wwan1')
    assert.equal(selection.selectedDevice.value, 'wwan1')
    assert.equal(errors.length, 2)
  } finally { scope.stop() }
})
