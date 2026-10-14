import assert from 'node:assert/strict'
import test from 'node:test'
import type { WiFiCallingHealthSnapshot } from '../src/types/api.ts'
import {
  formatHealthAvailability,
  formatHealthDuration,
  healthSegmentDuration,
  wifiCallingHealthDetail,
  wifiCallingHealthEventLabel,
  wifiCallingHealthLabel,
  wifiCallingHealthTone
} from '../src/utils/wifiCallingHealth.ts'

function health(overrides: Partial<WiFiCallingHealthSnapshot> = {}): WiFiCallingHealthSnapshot {
  return {
    state: 'healthy',
    active: true,
    measured: true,
    session_seconds: 3600,
    healthy_seconds: 3540,
    interrupted_seconds: 60,
    stable_seconds: 1800,
    longest_interruption_seconds: 60,
    interruption_count: 1,
    availability: 98.3333,
    ...overrides
  }
}

test('formats health states and availability without color-only semantics', () => {
  const snapshot = health()

  assert.equal(wifiCallingHealthLabel(snapshot), '运行稳定')
  assert.equal(wifiCallingHealthTone(snapshot.state), 'success')
  assert.equal(formatHealthAvailability(snapshot), '98.3%')
  assert.equal(wifiCallingHealthDetail(snapshot), '已稳定 30分钟')
})

test('distinguishes an intentional stop from an outage', () => {
  const stopped = health({
    state: 'stopped',
    active: false,
    last_reason: 'disable',
    availability: 100
  })

  assert.equal(wifiCallingHealthLabel(stopped), '已主动关闭')
  assert.equal(wifiCallingHealthTone(stopped.state), 'stopped')
  assert.equal(wifiCallingHealthDetail(stopped), '关闭原因：disable')
  assert.equal(wifiCallingHealthEventLabel({
    kind: 'stopped', state: 'stopped', at: '2026-09-04T10:00:00Z'
  }), '主动关闭')
})

test('formats compact durations and validates timeline boundaries', () => {
  assert.equal(formatHealthDuration(42), '42秒')
  assert.equal(formatHealthDuration(60), '1分钟')
  assert.equal(formatHealthDuration(7260), '2小时1分')
  assert.equal(formatHealthDuration(90000), '1天1小时')
  assert.equal(healthSegmentDuration('2026-09-04T10:00:00Z', '2026-09-04T10:02:30Z'), 150)
  assert.equal(healthSegmentDuration('invalid', '2026-09-04T10:02:30Z'), 1)
})

test('does not report a percentage before the first IMS registration', () => {
  const checking = health({ state: 'checking', measured: false, availability: 0 })

  assert.equal(formatHealthAvailability(checking), '--')
  assert.equal(wifiCallingHealthDetail(checking), 'IMS 注册成功且接收状态可判定后开始统计可用率')
})

test('shows an explicit startup failure before the first IMS registration', () => {
  const failed = health({
    state: 'unavailable',
    active: false,
    measured: false,
    availability: 0,
    last_reason: 'SIM identity unavailable'
  })

  assert.equal(wifiCallingHealthDetail(failed), 'SIM identity unavailable')
  assert.equal(wifiCallingHealthEventLabel({
    kind: 'failed', state: 'unavailable', at: '2026-09-04T10:00:00Z'
  }), '启动失败')
})

test('unverified reception is neither downtime nor verified availability', () => {
  const pending = health({ state: 'pending', measured: false, availability: 0 })
  assert.equal(wifiCallingHealthLabel(pending), '短信接收待验证')
  assert.equal(wifiCallingHealthTone(pending.state), 'neutral')
  assert.equal(formatHealthAvailability(pending), '--')
  assert.match(wifiCallingHealthDetail(pending), /不计为正常或中断/)
})
