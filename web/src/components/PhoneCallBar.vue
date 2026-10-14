<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  CallEnd24Regular,
  Dialpad24Regular,
  Mic24Regular,
  MicOff24Regular,
  Pause24Regular,
  Play24Regular,
  Speaker224Regular
} from '@vicons/fluent'
import { usePhoneStore } from '../stores/phone'
import { usePhoneIdentity } from '../composables/usePhoneIdentity'
import { formatCallDuration, phoneCallStatusLabel, phoneErrorMessage } from '../utils/phone'
import PhoneDialPad from './PhoneDialPad.vue'

const router = useRouter()
const phone = usePhoneStore()
const identities = usePhoneIdentity()
const ending = ref(false)
const keypadOpen = ref(false)
const lastDTMF = ref('')
const call = computed(() => phone.currentCall)
const waitingCall = computed(() => phone.calls.find((item) =>
  item.status === 'waiting' && item.call_id !== call.value?.call_id))
const callEnding = computed(() => call.value ? phone.isCallEnding(call.value.call_id) : false)
const connected = computed(() => call.value?.status === 'connected')
const canControl = computed(() => !!call.value
  && !call.value.read_only
  && !(call.value.direction === 'inbound' && call.value.status === 'ringing'))

watch(connected, (isConnected) => {
  if (!isConnected) {
    keypadOpen.value = false
    lastDTMF.value = ''
  }
})
watch(() => [call.value?.peer, call.value?.device_id] as const, ([peer, deviceId]) => {
  if (peer) void identities.resolve(peer, deviceId)
}, { immediate: true })

async function hangup() {
  if (!call.value || ending.value) return
  ending.value = true
  try {
    await phone.hangup(call.value)
  } catch (error) {
    phone.error = phoneErrorMessage(error, '挂断失败')
    ElMessage.error(phone.error)
  } finally {
    ending.value = false
  }
}

async function toggleHold() {
  try {
    await phone.toggleHold()
  } catch (error) {
    phone.error = phoneErrorMessage(error, '保持失败')
    ElMessage.error(phone.error)
  }
}

function toggleKeypad() {
  if (!connected.value || callEnding.value) return
  keypadOpen.value = !keypadOpen.value
}

async function sendDigit(digit: string) {
  try {
    await phone.sendDTMF(digit)
    lastDTMF.value = digit
  } catch (error) {
    ElMessage.error(phoneErrorMessage(error, 'DTMF 发送失败'))
  }
}
</script>

<template>
  <aside v-if="call" class="call-bar-wrap" aria-live="polite" aria-label="当前电话">
    <div class="call-bar">
      <button type="button" class="call-summary" @click="router.push('/phone')">
        <span class="call-pulse" aria-hidden="true" />
        <span class="call-copy">
          <strong>{{ identities.titleFor(call.peer, call.device_id) }}</strong>
          <small>{{ identities.subtitleFor(call.peer, call.device_id) ? identities.subtitleFor(call.peer, call.device_id) + ' · ' : '' }}{{ phone.mediaMode === 'listen-only' ? '仅听 · ' : '' }}{{ phoneCallStatusLabel(call, callEnding) }} · {{ formatCallDuration(call, phone.now) }}{{ waitingCall ? ` · 第二路 ${identities.titleFor(waitingCall.peer, waitingCall.device_id)}` : '' }}</small>
        </span>
        <span v-if="call.read_only" class="read-only-tag">只读</span>
      </button>
      <div class="call-actions">
        <button
          v-if="phone.mediaReady && !call.read_only"
          type="button"
          class="call-action"
          :disabled="phone.mediaMode !== 'two-way'"
          :aria-label="phone.mediaMode === 'listen-only' ? '仅听模式，对方听不到你' : phone.muted ? '取消静音' : '静音'"
          :aria-pressed="phone.muted"
          @click="phone.toggleMute"
        >
          <el-icon>
            <Speaker224Regular v-if="phone.mediaMode === 'listen-only'" />
            <MicOff24Regular v-else-if="phone.muted" />
            <Mic24Regular v-else />
          </el-icon>
        </button>
        <button
          v-if="canControl"
          type="button"
          class="call-action"
          :disabled="!connected || callEnding"
          :aria-label="call.held ? '恢复通话' : '保持通话'"
          :aria-pressed="!!call.held"
          @click="toggleHold"
        >
          <el-icon>
            <Play24Regular v-if="call.held" />
            <Pause24Regular v-else />
          </el-icon>
        </button>
        <button
          type="button"
          class="call-action"
          :disabled="!phone.canSendDTMF"
          :aria-label="connected ? (keypadOpen ? '关闭拨号键盘' : '打开拨号键盘') : '接通后可发送拨号音'"
          :aria-pressed="keypadOpen"
          @click="toggleKeypad"
        >
          <el-icon><Dialpad24Regular /></el-icon>
        </button>
        <button
          v-if="canControl"
          type="button"
          class="call-action is-danger"
          :disabled="ending || callEnding"
          :aria-label="ending || callEnding ? '正在挂断电话' : '挂断电话'"
          @click="hangup"
        >
          <el-icon><CallEnd24Regular /></el-icon>
        </button>
      </div>
    </div>
    <div v-if="keypadOpen && connected" class="call-bar-keypad">
      <p aria-live="polite">发送 DTMF{{ lastDTMF ? `：${lastDTMF}` : '' }}</p>
      <PhoneDialPad :disabled="!phone.canSendDTMF" @digit="sendDigit" />
    </div>
  </aside>
</template>

<style scoped>
.call-bar-wrap {
  margin: 12px 18px 0;
}

.call-bar {
  min-height: 62px;
  padding: 8px 10px 8px 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  border: 1px solid color-mix(in srgb, var(--ui-success) 36%, var(--ui-border));
  border-radius: var(--ui-radius-lg);
  background: color-mix(in srgb, var(--ui-success) 8%, var(--ui-surface));
  box-shadow: var(--ui-shadow-sm);
}

.call-bar-keypad {
  margin-top: 8px;
  padding: 14px 16px 16px;
  border: 1px solid var(--ui-border);
  border-radius: var(--ui-radius-lg);
  background: var(--ui-surface);
  box-shadow: var(--ui-shadow-sm);
}

.call-bar-keypad > p {
  margin: 0 0 12px;
  color: var(--ui-text-muted);
  font-size: 11px;
  text-align: center;
}

.call-summary {
  min-width: 0;
  flex: 1;
  display: flex;
  align-items: center;
  gap: 11px;
  border: 0;
  background: transparent;
  color: var(--ui-text);
  text-align: left;
  cursor: pointer;
}

.call-pulse {
  width: 10px;
  height: 10px;
  flex: 0 0 10px;
  border-radius: 50%;
  background: var(--ui-success);
  box-shadow: 0 0 0 5px color-mix(in srgb, var(--ui-success) 14%, transparent);
}

.call-copy { min-width: 0; display: grid; }
.call-copy strong { overflow: hidden; text-overflow: ellipsis; font-family: "v-mono", monospace; font-size: 13px; white-space: nowrap; }
.call-copy small { color: var(--ui-text-muted); font-size: 11px; }
.read-only-tag { padding: 2px 7px; border-radius: var(--ui-radius-sm); background: var(--ui-surface-muted); color: var(--ui-text-muted); font-size: 10px; }
.call-actions { display: flex; gap: 8px; }
.call-action { width: 44px; height: 44px; display: grid; place-items: center; border: 1px solid var(--ui-border); border-radius: 50%; background: var(--ui-surface); color: var(--ui-text); cursor: pointer; }
.call-action.is-danger { border-color: color-mix(in srgb, var(--ui-danger) 40%, var(--ui-border)); background: var(--ui-danger); color: #fff; }
.call-action:disabled { cursor: not-allowed; opacity: .5; }
.call-summary:focus-visible, .call-action:focus-visible { outline: 2px solid var(--ui-primary); outline-offset: 2px; }

@media (max-width: 820px) {
  .call-bar-wrap { margin: 8px 10px 0; }
  .call-bar { min-height: 58px; }
  .call-copy small { max-width: 148px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .read-only-tag { display: none; }
}
</style>
