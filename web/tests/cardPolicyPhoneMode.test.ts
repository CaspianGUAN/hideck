import assert from 'node:assert/strict'
import test from 'node:test'
import { readFile } from 'node:fs/promises'
import { nextTick, ref } from 'vue'
import { useCardPolicyToggles, type PolicyMirror } from '../src/composables/useCardPolicyToggles'

function mirror(partial: Partial<PolicyMirror> = {}): PolicyMirror {
  return {
    network_enabled: false,
    vowifi_enabled: false,
    airplane_enabled: false,
    phone_mode: 'wifi',
    data_strategy: 'on_demand',
    ...partial
  }
}

test('changing call mode turns software phone on instead of only saving the preference', async () => {
  const source = ref<PolicyMirror | null>(mirror())
  let applied: PolicyMirror | null = null
  const toggles = useCardPolicyToggles(source, {
    applyNetwork: async () => ({ ok: true }),
    applyVoWiFi: async () => ({ ok: true }),
    applyAirplane: async () => ({ ok: true }),
    applyPhoneMode: async (next) => {
      applied = next
      return { ok: true }
    }
  })
  await nextTick()

  await toggles.onPhoneModeChange('wifi')

  assert.equal(applied?.vowifi_enabled, true)
  assert.equal(applied?.phone_mode, 'wifi')
  assert.equal(applied?.network_enabled, false)
  assert.equal(applied?.airplane_enabled, true)
  assert.equal(toggles.local.value.vowifi_enabled, true)
})

test('switching to cellular on_demand arms software phone without forcing data on', async () => {
  const source = ref<PolicyMirror | null>(mirror({ vowifi_enabled: true }))
  let applied: PolicyMirror | null = null
  const toggles = useCardPolicyToggles(source, {
    applyNetwork: async () => ({ ok: true }),
    applyVoWiFi: async () => ({ ok: true }),
    applyAirplane: async () => ({ ok: true }),
    applyPhoneMode: async (next) => {
      applied = next
      return { ok: true }
    }
  })
  await nextTick()

  await toggles.onPhoneModeChange('cellular')

  assert.equal(applied?.vowifi_enabled, true)
  assert.equal(applied?.phone_mode, 'cellular')
  assert.equal(applied?.network_enabled, false)
  assert.equal(applied?.airplane_enabled, false)
})

test('switching to cellular keeps airplane off when network is already on', async () => {
  const source = ref<PolicyMirror | null>(mirror({ network_enabled: true, vowifi_enabled: true }))
  let applied: PolicyMirror | null = null
  const toggles = useCardPolicyToggles(source, {
    applyNetwork: async () => ({ ok: true }),
    applyVoWiFi: async () => ({ ok: true }),
    applyAirplane: async () => ({ ok: true }),
    applyPhoneMode: async (next) => {
      applied = next
      return { ok: true }
    }
  })
  await nextTick()

  await toggles.onPhoneModeChange('cellular')

  assert.equal(applied?.network_enabled, true)
  assert.equal(applied?.airplane_enabled, false)
})

test('airplane in cellular keeps software phone and closes data', async () => {
  const source = ref<PolicyMirror | null>(mirror({
    network_enabled: true,
    vowifi_enabled: true,
    phone_mode: 'cellular'
  }))
  let applied: PolicyMirror | null = null
  const toggles = useCardPolicyToggles(source, {
    applyNetwork: async () => ({ ok: true }),
    applyVoWiFi: async () => ({ ok: true }),
    applyAirplane: async (_enabled, next) => {
      applied = next
      return { ok: true }
    },
    applyPhoneMode: async () => ({ ok: true })
  })
  await nextTick()

  await toggles.onRadioModeChange('airplane')

  assert.equal(applied?.airplane_enabled, true)
  assert.equal(applied?.network_enabled, false)
  assert.equal(applied?.vowifi_enabled, true)
  assert.equal(toggles.radioMode.value, 'airplane')
})

test('turning airplane off restores network for cellular always', async () => {
  const source = ref<PolicyMirror | null>(mirror({
    airplane_enabled: true,
    vowifi_enabled: true,
    phone_mode: 'cellular',
    data_strategy: 'always'
  }))
  let applied: PolicyMirror | null = null
  const toggles = useCardPolicyToggles(source, {
    applyNetwork: async () => ({ ok: true }),
    applyVoWiFi: async () => ({ ok: true }),
    applyAirplane: async (_enabled, next) => {
      applied = next
      return { ok: true }
    },
    applyPhoneMode: async () => ({ ok: true })
  })
  await nextTick()

  await toggles.onAirplaneToggle(false)

  assert.equal(applied?.airplane_enabled, false)
  assert.equal(applied?.network_enabled, true)
  assert.equal(applied?.vowifi_enabled, true)
})

test('airplane in wifi calling turns software phone off', async () => {
  const source = ref<PolicyMirror | null>(mirror({
    vowifi_enabled: true,
    phone_mode: 'wifi'
  }))
  let applied: PolicyMirror | null = null
  const toggles = useCardPolicyToggles(source, {
    applyNetwork: async () => ({ ok: true }),
    applyVoWiFi: async () => ({ ok: true }),
    applyAirplane: async (_enabled, next) => {
      applied = next
      return { ok: true }
    },
    applyPhoneMode: async () => ({ ok: true })
  })
  await nextTick()

  assert.equal(toggles.wifiCallingLocksRadio.value, true)
  assert.equal(toggles.radioMode.value, 'airplane')

  await toggles.onAirplaneToggle(true)

  assert.equal(applied?.airplane_enabled, true)
  assert.equal(applied?.vowifi_enabled, false)
  assert.equal(applied?.network_enabled, false)
})

test('switching to volte unlocks radio and is not wifi calling', async () => {
  const source = ref<PolicyMirror | null>(mirror({
    vowifi_enabled: true,
    airplane_enabled: true,
    phone_mode: 'wifi'
  }))
  let applied: PolicyMirror | null = null
  const toggles = useCardPolicyToggles(source, {
    applyNetwork: async () => ({ ok: true }),
    applyVoWiFi: async () => ({ ok: true }),
    applyAirplane: async () => ({ ok: true }),
    applyPhoneMode: async (next) => {
      applied = next
      return { ok: true }
    }
  })
  await nextTick()

  await toggles.onPhoneModeChange('volte')

  assert.equal(applied?.phone_mode, 'volte')
  assert.equal(applied?.vowifi_enabled, true)
  assert.equal(applied?.airplane_enabled, false)
  assert.equal(toggles.wifiCallingLocksRadio.value, false)
  assert.equal(toggles.radioMode.value, 'camp')
})

test('volte with software phone does not lock flight', async () => {
  const source = ref<PolicyMirror | null>(mirror({
    vowifi_enabled: true,
    airplane_enabled: false,
    phone_mode: 'volte'
  }))
  const toggles = useCardPolicyToggles(source, {
    applyNetwork: async () => ({ ok: true }),
    applyVoWiFi: async () => ({ ok: true }),
    applyAirplane: async () => ({ ok: true }),
    applyPhoneMode: async () => ({ ok: true })
  })
  await nextTick()

  assert.equal(toggles.wifiCallingLocksRadio.value, false)
  assert.equal(toggles.radioMode.value, 'camp')
  assert.equal(toggles.local.value.airplane_enabled, false)
})

test('card policy puts a start switch under wifi calling instead of only a mode picker', async () => {
  const panel = await readFile(new URL('../src/components/CardPolicyPanel.vue', import.meta.url), 'utf8')
  const inline = await readFile(new URL('../src/components/EsimCardPolicyInline.vue', import.meta.url), 'utf8')
  assert.match(panel, /<strong>通话方式<\/strong>[\s\S]*模组直拨[\s\S]*phone_mode \?\? 'wifi'\) === 'wifi' \? '启动' : '电话服务'[\s\S]*onVoWiFiToggle/)
  assert.doesNotMatch(panel, /只选路径/)
  assert.match(inline, />通话方式</)
  assert.match(inline, /phone_mode \?\? 'wifi'\) === 'wifi' \? '启动' : '电话服务'[\s\S]*onVoWiFiToggle/)
})

test('turning wifi calling off keeps the wifi path and only stops the service', async () => {
  const source = ref<PolicyMirror | null>(mirror({
    vowifi_enabled: true,
    airplane_enabled: true,
    phone_mode: 'wifi'
  }))
  let applied: PolicyMirror | null = null
  const toggles = useCardPolicyToggles(source, {
    applyNetwork: async () => ({ ok: true }),
    applyVoWiFi: async (_enabled, next) => {
      applied = next
      return { ok: true }
    },
    applyAirplane: async () => ({ ok: true }),
    applyPhoneMode: async () => ({ ok: true })
  })
  await nextTick()

  await toggles.onVoWiFiToggle(false)

  assert.equal(applied?.phone_mode, 'wifi')
  assert.equal(applied?.vowifi_enabled, false)
  assert.equal(applied?.airplane_enabled, true)
  assert.equal(toggles.local.value.phone_mode, 'wifi')
  assert.equal(toggles.local.value.vowifi_enabled, false)
})

test('wifi calling locks camp so radio cannot independently leave airplane', async () => {
  const source = ref<PolicyMirror | null>(mirror({
    vowifi_enabled: true,
    airplane_enabled: true,
    phone_mode: 'wifi'
  }))
  let applied: PolicyMirror | null = null
  const toggles = useCardPolicyToggles(source, {
    applyNetwork: async () => ({ ok: true }),
    applyVoWiFi: async () => ({ ok: true }),
    applyAirplane: async (_enabled, next) => {
      applied = next
      return { ok: true }
    },
    applyPhoneMode: async () => ({ ok: true })
  })
  await nextTick()

  await toggles.onRadioModeChange('camp')

  assert.equal(applied, null)
  assert.equal(toggles.local.value.airplane_enabled, true)
  assert.equal(toggles.local.value.vowifi_enabled, true)
})
