import assert from 'node:assert/strict'
import test from 'node:test'
import { dialNumberError } from '../src/utils/phone'

test('UK international format rejects the domestic trunk prefix without changing the number', () => {
  assert.match(dialNumberError('+4407912345678', 'GB'), /去掉本地号码开头的 0/)
  assert.equal(dialNumberError('+447912345678', 'GB'), '')
  assert.equal(dialNumberError('07912345678', 'GB'), '')
  assert.equal(dialNumberError('10086', 'GB'), '')
})

test('other regions keep their own international dialing conventions', () => {
  assert.equal(dialNumberError('+390612345678', 'IT'), '')
  assert.equal(dialNumberError('+4407912345678', 'CN'), '')
  assert.match(dialNumberError('+44 079', 'GB'), /只能包含/)
})
