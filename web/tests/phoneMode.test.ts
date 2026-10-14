import assert from 'node:assert/strict'
import test from 'node:test'
import { deviceSupportsUt, isNativeVoLTEMode, isWifiCallingEnabled, phoneModeCampsOnCell, phoneModeLabel } from '../src/utils/phoneMode'

test('native volte is only the volte phone mode', () => {
  assert.equal(isNativeVoLTEMode('volte'), true)
  assert.equal(isNativeVoLTEMode('cellular'), false)
  assert.equal(isNativeVoLTEMode('wifi'), false)
})

test('volte and cellular camp on cell; wifi does not', () => {
  assert.equal(phoneModeCampsOnCell('volte'), true)
  assert.equal(phoneModeCampsOnCell('cellular'), true)
  assert.equal(phoneModeCampsOnCell('wifi'), false)
  assert.equal(phoneModeCampsOnCell(undefined), false)
})

test('wifi calling is only wifi mode with phone on', () => {
  assert.equal(isWifiCallingEnabled('wifi', true), true)
  assert.equal(isWifiCallingEnabled('volte', true), false)
  assert.equal(isWifiCallingEnabled('cellular', true), false)
  assert.equal(isWifiCallingEnabled('wifi', false), false)
})

test('phone mode labels', () => {
  assert.equal(phoneModeLabel('modem_voice'), '模组直拨')
  assert.equal(phoneModeLabel('volte'), 'VoLTE')
  assert.equal(phoneModeLabel('cellular'), '蜂窝数据')
  assert.equal(phoneModeLabel('wifi'), 'WiFi calling')
})

test('modem voice camps on cell without exposing software IMS controls', () => {
  assert.equal(phoneModeCampsOnCell('modem_voice'), true)
  assert.equal(isWifiCallingEnabled('modem_voice', true), false)
  assert.equal(isNativeVoLTEMode('modem_voice'), false)
  assert.equal(deviceSupportsUt({ phone_mode: 'modem_voice', vowifi_enabled: true }), false)
})

test('Ut is only for software IMS WiFi calling devices', () => {
  assert.equal(deviceSupportsUt({ vowifi_enabled: true, phone_mode: 'wifi' }), true)
  assert.equal(deviceSupportsUt({ vowifi_active: true, phone_mode: 'wifi' }), true)
  assert.equal(deviceSupportsUt({ vowifi_enabled: true, phone_mode: 'cellular' }), true)
  assert.equal(deviceSupportsUt({ vowifi_enabled: true, phone_mode: 'volte' }), false)
  assert.equal(deviceSupportsUt({ vowifi_enabled: false, phone_mode: 'wifi' }), false)
  assert.equal(deviceSupportsUt({ phone_mode: 'wifi' }), false)
  assert.equal(deviceSupportsUt(undefined), false)
})
