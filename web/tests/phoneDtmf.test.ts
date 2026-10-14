import assert from 'node:assert/strict'
import test from 'node:test'
import { createPinia, setActivePinia } from 'pinia'
import { phoneService, type PhoneCall } from '../src/services/phone'
import { usePhoneStore } from '../src/stores/phone'

function connectedCall(): PhoneCall {
  return { call_id: 'modemvoice-test', device_id: 'wwan1', direction: 'outbound',
    peer: '10010', status: 'connected', media_id: 'media', started_at: '', read_only: false }
}

test('modem voice and IMS use the same DTMF API without requiring microphone audio', async () => {
  const original = phoneService.dtmf
  const sent: unknown[] = []
  phoneService.dtmf = async (...args) => { sent.push(args) }
  try {
    setActivePinia(createPinia())
    const store = usePhoneStore()
    store.mediaId = 'media'
    store.lease = 'lease'
    store.mediaMode = 'listen-only'
    for (const id of ['modemvoice-test', 'ims-call']) {
      store.calls = [{ ...connectedCall(), call_id: id }]
      assert.equal(store.canSendDTMF, true)
      await store.sendDTMF('#')
      assert.deepEqual(sent.at(-1), [id, '#', 'lease'])
    }
    phoneService.dtmf = async () => { throw new Error('AT+VTS rejected') }
    await assert.rejects(store.sendDTMF('1'), /AT\+VTS rejected/)
  } finally {
    phoneService.dtmf = original
  }
})

test('DTMF is disabled for ringing, held, read-only, ending and missing calls', async () => {
  const original = phoneService.dtmf
  let requests = 0
  phoneService.dtmf = async () => { requests++ }
  try {
    setActivePinia(createPinia())
    const store = usePhoneStore()
    for (const change of [{ status: 'ringing' as const }, { held: true }, { read_only: true }]) {
      store.calls = [{ ...connectedCall(), ...change }]
      assert.equal(store.canSendDTMF, false)
      await assert.rejects(store.sendDTMF('1'))
    }
    store.calls = [connectedCall()]
    store.endingCallIds = ['modemvoice-test']
    assert.equal(store.canSendDTMF, false)
    await assert.rejects(store.sendDTMF('1'))
    store.calls = []
    assert.equal(store.canSendDTMF, false)
    await assert.rejects(store.sendDTMF('1'))
    assert.equal(requests, 0)
  } finally {
    phoneService.dtmf = original
  }
})
