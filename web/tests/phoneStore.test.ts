import assert from 'node:assert/strict'
import test from 'node:test'
import { createPinia, setActivePinia } from 'pinia'
import { phoneService, type PhoneCall, type PhoneEvent, type PhoneRecord } from '../src/services/phone'
import { usePhoneStore } from '../src/stores/phone'
import {
  normalizeCallOwnership,
  phoneCallStatusLabel,
  phoneErrorMessage,
  phoneRecordStatusLabel,
  shouldRefreshCallMedia
} from '../src/utils/phone'

function call(mediaId: string, overrides: Partial<PhoneCall> = {}): PhoneCall {
  return {
    call_id: 'call-1',
    device_id: 'wwan1',
    direction: 'outbound',
    peer: '888',
    status: 'connected',
    media_id: mediaId,
    started_at: '2026-08-13T12:00:00Z',
    read_only: false,
    ...overrides
  }
}

function event(id: number, value: PhoneCall): PhoneEvent {
  return { id, type: 'call_connected', call: value, time: '2026-08-13T12:00:00Z' }
}

test('normalizes broadcast ownership against the tab media session', () => {
  assert.equal(normalizeCallOwnership(call('other'), 'ours').read_only, true)
  assert.equal(normalizeCallOwnership(call('ours', { read_only: true }), 'ours').read_only, false)
  assert.equal(normalizeCallOwnership(call('', { read_only: false }), 'ours').read_only, false)
})

test('failed dial keeps the broadcast call visible and requires takeover after media release', () => {
  setActivePinia(createPinia())
  const store = usePhoneStore()
  store.mediaId = 'ours'
  store.lease = 'lease'
  store.handleEvent(event(1, call('ours', { status: 'calling' })))
  assert.equal(store.currentCall?.read_only, false)
  store.releaseMedia()
  assert.equal(store.calls.length, 1)
  assert.equal(store.currentCall?.read_only, true)
  assert.equal(store.lease, '')
})

test('store ignores replayed events and never grants another media session control', () => {
  setActivePinia(createPinia())
  const store = usePhoneStore()
  store.mediaId = 'ours'
  store.handleEvent(event(10, call('other')))
  assert.equal(store.calls[0].read_only, true)
  store.handleEvent(event(10, call('ours')))
  assert.equal(store.calls[0].media_id, 'other')
  store.handleEvent(event(11, call('ours')))
  assert.equal(store.calls[0].read_only, false)
})

test('removes an incoming call even when call_ended still says ringing', () => {
  setActivePinia(createPinia())
  const store = usePhoneStore()
  store.mediaId = 'media-1'
  store.handleEvent({
    id: 1,
    type: 'incoming_call',
    call: call('media-1', { direction: 'inbound', status: 'ringing', peer: '+15550001' }),
    time: '2026-08-13T12:00:00Z'
  })
  assert.equal(store.calls.length, 1)
  store.handleEvent({
    id: 2,
    type: 'call_ended',
    call: call('media-1', { direction: 'inbound', status: 'ringing', peer: '+15550001' }),
    time: '2026-08-13T12:00:20Z'
  })
  assert.equal(store.calls.length, 0)
})

test('applies call_ended after the event stream restarts with lower ids', () => {
  setActivePinia(createPinia())
  const store = usePhoneStore()
  store.mediaId = 'media-1'
  store.calls = [call('media-1')]
  store.endingCallIds = ['call-1']
  store.lastEventId = 9
  store.handleEvent({
    id: 1,
    type: 'call_ended',
    call: call('media-1', { status: 'completed' }),
    time: '2026-08-13T12:01:00Z'
  })
  assert.equal(store.calls.length, 0)
  assert.equal(store.lastEventId, 1)
  assert.equal(store.isCallEnding('call-1'), false)
})

test('toggleHold keeps mute local and only talks to IMS hold APIs', async () => {
  const originalHold = phoneService.hold
  const originalResume = phoneService.resume
  const calls: string[] = []
  phoneService.hold = async (callId) => { calls.push(`hold:${callId}`) }
  phoneService.resume = async (callId) => { calls.push(`resume:${callId}`) }
  try {
    setActivePinia(createPinia())
    const store = usePhoneStore()
    store.lease = 'lease-1'
    store.mediaId = 'media-1'
    store.mediaMode = 'two-way'
    store.calls = [call('media-1')]
    await store.toggleHold()
    assert.equal(store.currentCall?.held, true)
    assert.deepEqual(calls, ['hold:call-1'])
    await store.toggleHold()
    assert.equal(store.currentCall?.held, false)
    assert.deepEqual(calls, ['hold:call-1', 'resume:call-1'])
    store.toggleMute()
    assert.equal(store.muted, true)
  } finally {
    phoneService.hold = originalHold
    phoneService.resume = originalResume
  }
})

test('listen-only mode cannot be presented as a muteable microphone', () => {
  setActivePinia(createPinia())
  const store = usePhoneStore()
  store.mediaMode = 'listen-only'
  store.toggleMute()
  assert.equal(store.muted, false)
  store.mediaMode = 'two-way'
  store.toggleMute()
  assert.equal(store.muted, true)
})

test('media reuse requires the requested privacy mode to match', () => {
  setActivePinia(createPinia())
  const store = usePhoneStore()
  store.mediaState = 'connected'
  store.mediaId = 'media-1'
  store.lease = 'lease-1'
  store.mediaMode = 'two-way'
  assert.equal(store.hasReusableMedia('two-way'), true)
  assert.equal(store.hasReusableMedia('listen-only'), false)
  store.mediaMode = 'listen-only'
  assert.equal(store.hasReusableMedia('listen-only'), true)
  assert.equal(store.hasReusableMedia('two-way'), false)
})

test('outbound calls prepare the explicitly selected media mode', async () => {
  const originalStartCall = phoneService.startCall
  const mediaRequests: string[] = []
  phoneService.startCall = async (_deviceId, callee, mediaId) => {
    mediaRequests.push(mediaId)
    return call(mediaId, { call_id: `call-${mediaId}`, peer: callee, status: 'calling' })
  }

  try {
    setActivePinia(createPinia())
    const listenStore = usePhoneStore()
    let listenPreparations = 0
    let microphonePreparations = 0
    listenStore.prepareReceiveOnlyMedia = async () => {
      listenPreparations += 1
      return { mediaId: 'listen-media', lease: 'listen-lease' }
    }
    listenStore.prepareMedia = async () => {
      microphonePreparations += 1
      return { mediaId: 'two-way-media', lease: 'two-way-lease' }
    }
    await listenStore.startListenOnlyCall('wwan1', '888')
    assert.equal(listenPreparations, 1)
    assert.equal(microphonePreparations, 0)

    setActivePinia(createPinia())
    const twoWayStore = usePhoneStore()
    twoWayStore.prepareReceiveOnlyMedia = async () => {
      listenPreparations += 1
      return { mediaId: 'unexpected-listen', lease: 'unexpected-lease' }
    }
    twoWayStore.prepareMedia = async () => {
      microphonePreparations += 1
      return { mediaId: 'two-way-media', lease: 'two-way-lease' }
    }
    await twoWayStore.startCall('wwan1', '888')
    assert.equal(listenPreparations, 1)
    assert.equal(microphonePreparations, 1)
    assert.deepEqual(mediaRequests, ['listen-media', 'two-way-media'])
  } finally {
    phoneService.startCall = originalStartCall
  }
})

test('listen-only outbound releases its media when call creation fails', async () => {
  const originalStartCall = phoneService.startCall
  phoneService.startCall = async () => {
    throw new Error('IMS rejected the call')
  }

  try {
    setActivePinia(createPinia())
    const store = usePhoneStore()
    let releases = 0
    store.prepareReceiveOnlyMedia = async () => ({ mediaId: 'listen-media', lease: 'listen-lease' })
    store.releaseMedia = () => { releases += 1 }

    await assert.rejects(store.startListenOnlyCall('wwan1', '888'), /IMS rejected the call/)
    assert.equal(releases, 1)
    assert.equal(store.calls.length, 0)
  } finally {
    phoneService.startCall = originalStartCall
  }
})

test('surfaces API error messages without hiding the underlying failure', () => {
  const error = { response: { data: { message: 'phone: media session is unavailable' } } }
  assert.equal(phoneErrorMessage(error, 'fallback'), 'phone: media session is unavailable')
  assert.equal(phoneErrorMessage(new Error('network down'), 'fallback'), 'network down')
  assert.equal(
    phoneErrorMessage(new Error('voice: hold is not aligned to 24.229/24.610 on the live network'), '保持失败'),
    '保持未对齐，暂不可用'
  )
})

test('hangup success drops the local call without waiting for events and ignores a second click', async () => {
  const originalHangup = phoneService.hangup
  let requests = 0
  let hangupStarted: ((value?: unknown) => void) | undefined
  phoneService.hangup = () => {
    requests += 1
    return new Promise((resolve) => { hangupStarted = resolve })
  }

  try {
    setActivePinia(createPinia())
    const store = usePhoneStore()
    const active = call('media-1')
    store.calls = [active]
    store.mediaId = 'media-1'
    store.lease = 'lease-1'
    let releases = 0
    store.releaseMedia = () => { releases += 1 }
    store.reloadHistory = async () => {}

    const first = store.hangup(active)
    await store.hangup(active)
    assert.equal(requests, 1)
    assert.equal(store.isCallEnding(active.call_id), true)
    hangupStarted?.()
    await first
    assert.equal(store.isCallEnding(active.call_id), false)
    assert.equal(store.calls.length, 0)
    assert.equal(releases, 1)
  } finally {
    phoneService.hangup = originalHangup
  }
})

test('clears the ending state but keeps the call when hangup fails', async () => {
  const originalHangup = phoneService.hangup
  phoneService.hangup = async () => { throw new Error('BYE rejected') }

  try {
    setActivePinia(createPinia())
    const store = usePhoneStore()
    const active = call('media-1')
    store.calls = [active]

    await assert.rejects(store.hangup(active), /BYE rejected/)
    assert.equal(store.isCallEnding(active.call_id), false)
    assert.equal(store.calls.length, 1)
  } finally {
    phoneService.hangup = originalHangup
  }
})

test('labels pending and ended calls from their real lifecycle data', () => {
  const active = call('media-1')
  const record = (endReason?: string): PhoneRecord => ({
    id: 1,
    call_id: active.call_id,
    device_id: active.device_id,
    direction: active.direction,
    peer: active.peer,
    status: 'completed',
    started_at: active.started_at,
    ended_at: '2026-08-13T12:01:00Z',
    duration_seconds: 60,
    end_reason: endReason
  })

  assert.equal(phoneCallStatusLabel(active), '通话中')
  assert.equal(phoneCallStatusLabel({ ...active, status: 'waiting' }), '呼叫等待')
  assert.equal(phoneCallStatusLabel({ ...active, held: true }), '保持中')
  assert.equal(phoneCallStatusLabel(active, true), '挂断中')
  assert.equal(phoneRecordStatusLabel(record('local_hangup')), '已挂断')
  assert.equal(phoneRecordStatusLabel(record('remote_bye')), '对方已挂断')
  assert.equal(phoneRecordStatusLabel(record()), '已结束')
})

test('keeps a waiting second call in the active list', () => {
  setActivePinia(createPinia())
  const store = usePhoneStore()
  store.mediaId = 'media-1'
  store.handleEvent(event(1, call('media-1')))
  store.handleEvent({
    id: 2,
    type: 'call_waiting',
    call: call('', { call_id: 'wait-1', status: 'waiting', direction: 'inbound', peer: '+15550002' }),
    time: '2026-08-13T12:01:00Z'
  })
  assert.equal(store.calls.some((item) => item.call_id === 'wait-1' && item.status === 'waiting'), true)
})

test('refreshes ringing outbound media but leaves an unclaimed incoming call unattached', () => {
  assert.equal(shouldRefreshCallMedia(call('old', { status: 'ringing' }), 'new'), true)
  assert.equal(shouldRefreshCallMedia(call('', { direction: 'inbound', status: 'ringing' }), 'new'), false)
  assert.equal(shouldRefreshCallMedia(call('old', { direction: 'inbound', status: 'ringing' }), 'new'), true)
  assert.equal(shouldRefreshCallMedia(call('new'), 'new'), false)
})
