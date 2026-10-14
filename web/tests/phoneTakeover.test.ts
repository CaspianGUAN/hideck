import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { createPinia, setActivePinia } from 'pinia'
import { phoneService, type PhoneCall } from '../src/services/phone'
import { usePhoneStore } from '../src/stores/phone'

const active: PhoneCall = {
  call_id: 'modemvoice-retained', device_id: 'wwan1', direction: 'inbound',
  peer: '10010', status: 'connected', started_at: '2026-09-22T12:00:00Z', read_only: true
}

test('listen-only takeover uses receive-only media without a secure context or microphone', async () => {
  const original = phoneService.refreshMedia
  try {
    setActivePinia(createPinia())
    const store = usePhoneStore()
    assert.equal(store.secureContext, false)
    store.calls = [active]
    store.prepareMedia = async () => { throw new Error('microphone must not be requested') }
    store.prepareReceiveOnlyMedia = async () => {
      store.mediaId = 'listen-media'
      store.mediaMode = 'listen-only'
      return { mediaId: 'listen-media', lease: 'listen-lease' }
    }
    phoneService.refreshMedia = async (callId, mediaId, lease, takeover) => {
      assert.deepEqual([callId, mediaId, lease, takeover], [active.call_id, 'listen-media', '', true])
      return { call: { ...active, media_id: mediaId, read_only: false }, lease: 'new-lease' }
    }
    await store.takeOver(active, 'listen-only')
    assert.equal(store.currentCall?.read_only, false)
    assert.equal(store.mediaMode, 'listen-only')
    assert.equal(store.lease, 'new-lease')
  } finally {
    phoneService.refreshMedia = original
  }
})

test('default takeover retains two-way preparation and exposes failed takeover', async () => {
  const original = phoneService.refreshMedia
  try {
    setActivePinia(createPinia())
    const store = usePhoneStore()
    store.calls = [active]
    let microphonePreparations = 0
    store.prepareReceiveOnlyMedia = async () => { throw new Error('unexpected receive-only preparation') }
    store.prepareMedia = async () => {
      microphonePreparations++
      store.mediaId = 'two-way-media'
      return { mediaId: 'two-way-media', lease: 'lease' }
    }
    phoneService.refreshMedia = async () => { throw new Error('audio route unavailable') }
    await assert.rejects(store.takeOver(active), /audio route unavailable/)
    assert.equal(microphonePreparations, 1)
    assert.equal(store.mediaId, '')
    assert.equal(store.calls.length, 1)
  } finally {
    phoneService.refreshMedia = original
  }
})

test('takeover UI enables only the receive-only option on HTTP', () => {
  const source = readFileSync(new URL('../src/views/Phone.vue', import.meta.url), 'utf8')
  const listenButton = source.match(/<button\b[^>]*@click="takeOver\(call, 'listen-only'\)"[^>]*>/)?.[0]
  const twoWayButton = source.match(/<button\b[^>]*@click="takeOver\(call, 'two-way'\)"[^>]*>/)?.[0]
  assert.ok(listenButton)
  assert.ok(twoWayButton)
  assert.doesNotMatch(listenButton, /secureContext/)
  assert.match(twoWayButton, /!phone\.secureContext/)
})
