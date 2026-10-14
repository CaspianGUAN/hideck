import assert from 'node:assert/strict'
import test from 'node:test'
import { modemVoicePresentation, modemVoiceStages } from '../src/utils/modemVoicePresentation'

test('ADB reboot remains pending rather than failed or ready', () => {
  const state = { ready: false, phase: 'restarting', last_error: '模组重启准备中' }
  assert.equal(modemVoicePresentation(state).tone, 'is-pending')
  assert.match(modemVoicePresentation(state).title, /正在重启/)
  assert.equal(modemVoiceStages(state)[0].ready, undefined)
  assert.equal(modemVoicePresentation({ ...state, phase: 'failed' }).tone, 'is-failed')
})
