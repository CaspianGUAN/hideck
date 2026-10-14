import assert from 'node:assert/strict'
import test from 'node:test'
import {
  PhoneMediaController,
  isTrustedHTTPSContext,
  supportsPCMU,
  type PhoneMediaDependencies,
  type PhoneMediaState
} from '../src/services/phone-media'
import { phoneService } from '../src/services/phone'
import { api } from '../src/stores/auth'

const PCMU_OFFER = 'v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 0\r\na=rtpmap:0 PCMU/8000\r\n'
const RECVONLY_PCMU_OFFER = `${PCMU_OFFER}a=recvonly\r\n`

type FakeTrack = MediaStreamTrack & { stopped: boolean }

function mediaFixture(secure = true, connectAutomatically = true, releaseError: Error | null = null) {
  const states: PhoneMediaState[] = []
  const track = {
    enabled: true,
    stopped: false,
    stop() { this.stopped = true }
  } as FakeTrack
  const microphone = {
    active: true,
    getAudioTracks: () => [track],
    getTracks: () => [track]
  } as unknown as MediaStream
  const peers: FakePeer[] = []
  const timers = new Map<number, () => void>()
  let nextTimer = 0
  let requestedConstraints: MediaStreamConstraints | null = null
  const requestedOffers: string[] = []
  const releasedMedia: Array<{ mediaId: string; lease: string }> = []
  const microphoneStoppedAtMediaCreation: boolean[] = []
  const dependencies: PhoneMediaDependencies = {
    secureContext: () => secure,
    getUserMedia: async (constraints) => {
      requestedConstraints = constraints
      return microphone
    },
    createPeer: () => {
      const peer = new FakePeer()
      peer.connectAutomatically = connectAutomatically
      peers.push(peer)
      return peer as unknown as RTCPeerConnection
    },
    createAudio: () => ({ autoplay: false, srcObject: null, play: async () => {} }) as unknown as HTMLAudioElement,
    createMedia: async (sdp) => {
      requestedOffers.push(sdp)
      microphoneStoppedAtMediaCreation.push(track.stopped)
      const suffix = requestedOffers.length
      return { media_id: `media-${suffix}`, lease: `lease-${suffix}`, sdp: PCMU_OFFER }
    },
    releaseMedia: async (mediaId, lease) => {
      releasedMedia.push({ mediaId, lease })
      if (releaseError) throw releaseError
    },
    setTimer: (handler) => { const id = ++nextTimer; timers.set(id, handler); return id },
    clearTimer: (id) => { timers.delete(id) }
  }
  const controller = new PhoneMediaController({
    onState: (state) => states.push(state),
    onError: (message) => assert.fail(message)
  }, dependencies)
  return {
    controller,
    states,
    track,
    peers,
    timers,
    constraints: () => requestedConstraints,
    offers: () => requestedOffers,
    releasedMedia: () => releasedMedia,
    microphoneStoppedAtMediaCreation: () => microphoneStoppedAtMediaCreation
  }
}

test('refuses microphone access outside a trusted secure context', async () => {
  const fixture = mediaFixture(false)
  await assert.rejects(fixture.controller.prepare(), /HTTPS 安全上下文/)
  assert.equal(fixture.constraints(), null)
})

test('creates a receive-only listening session without microphone access', async () => {
  const fixture = mediaFixture(false)
  assert.deepEqual(
    await fixture.controller.prepare({ microphone: false }),
    { mediaId: 'media-1', lease: 'lease-1' }
  )
  assert.equal(fixture.constraints(), null)
  assert.equal(fixture.peers[0].transceiverDirection, 'recvonly')
  assert.deepEqual(fixture.offers(), [RECVONLY_PCMU_OFFER])
  fixture.controller.close()
})

test('switching from two-way to receive-only stops microphone capture before replacement', async () => {
  const fixture = mediaFixture()
  await fixture.controller.prepare()
  assert.equal(fixture.track.stopped, false)
  await fixture.controller.prepare({ microphone: false })
  assert.equal(fixture.track.stopped, true)
  assert.equal(fixture.peers[0].closed, true)
  assert.equal(fixture.peers[1].transceiverDirection, 'recvonly')
  assert.deepEqual(fixture.offers(), [PCMU_OFFER, RECVONLY_PCMU_OFFER])
  assert.deepEqual(fixture.microphoneStoppedAtMediaCreation(), [false, true])
  fixture.controller.close()
})

test('prepares PCMU media, controls mute, and releases browser resources', async () => {
  const fixture = mediaFixture()
  assert.deepEqual(await fixture.controller.prepare(), { mediaId: 'media-1', lease: 'lease-1' })
  assert.deepEqual(fixture.states, ['requesting', 'connecting', 'connected'])
  assert.equal((fixture.constraints()?.audio as MediaTrackConstraints).channelCount, 1)
  assert.deepEqual(fixture.offers(), [PCMU_OFFER])
  assert.equal(fixture.peers[0].remoteDescription?.sdp, PCMU_OFFER)
  fixture.controller.setMuted(true)
  assert.equal(fixture.track.enabled, false)
  fixture.controller.close()
  assert.equal(fixture.track.stopped, true)
  assert.equal(fixture.peers[0].closed, true)
  assert.equal(fixture.states.at(-1), 'idle')
})

test('recognizes only an explicit PCMU 8000 SDP mapping', () => {
  assert.equal(supportsPCMU(PCMU_OFFER), true)
  assert.equal(supportsPCMU('a=rtpmap:8 PCMA/8000\r\n'), false)
})

test('does not treat the localhost HTTP exception as an HTTPS phone context', () => {
  assert.equal(isTrustedHTTPSContext('http:', true, true), false)
  assert.equal(isTrustedHTTPSContext('https:', false, true), false)
  assert.equal(isTrustedHTTPSContext('https:', true, true), true)
})

class FakePeer extends EventTarget {
  iceGatheringState: RTCIceGatheringState = 'complete'
  connectionState: RTCPeerConnectionState = 'new'
  localDescription: RTCSessionDescription | null = null
  remoteDescription: RTCSessionDescription | null = null
  closed = false
  connectAutomatically = true
  transceiverDirection = ''
  private readonly transceiver = { sender: {}, setCodecPreferences: () => {} } as unknown as RTCRtpTransceiver

  addTrack() { return this.transceiver.sender }
  addTransceiver(_track: string, init?: RTCRtpTransceiverInit) {
    this.transceiverDirection = init?.direction || ''
    return this.transceiver
  }
  getTransceivers() { return [this.transceiver] }
  async createOffer() {
    const sdp = this.transceiverDirection === 'recvonly' ? RECVONLY_PCMU_OFFER : PCMU_OFFER
    return { type: 'offer', sdp } as RTCSessionDescriptionInit
  }
  async setLocalDescription(description: RTCLocalSessionDescriptionInit) {
    this.localDescription = description as RTCSessionDescription
  }
  async setRemoteDescription(description: RTCSessionDescriptionInit) {
    this.remoteDescription = description as RTCSessionDescription
    if (this.connectAutomatically) this.setConnectionState('connected')
  }
  setConnectionState(state: RTCPeerConnectionState) {
    this.connectionState = state
    this.dispatchEvent(new Event('connectionstatechange'))
  }
  close() { this.closed = true; this.setConnectionState('closed') }
}

test('does not return dialable media before the peer actually connects', async () => {
  const fixture = mediaFixture(true, false)
  let ready = false
  const preparing = fixture.controller.prepare().then((result) => { ready = true; return result })
  await new Promise<void>((resolve) => setImmediate(resolve))
  assert.equal(ready, false)
  assert.equal(fixture.peers[0].remoteDescription?.sdp, PCMU_OFFER)
  fixture.peers[0].setConnectionState('connected')
  await preparing
  assert.equal(ready, true)
  assert.equal(fixture.timers.size, 0)
  assert.deepEqual(fixture.releasedMedia(), [])
  fixture.controller.close()
})

test('failed ICE rejects preparation and releases the microphone', async () => {
  const fixture = mediaFixture(true, false)
  const preparing = fixture.controller.prepare()
  const rejected = assert.rejects(preparing, /UDP 媒体通道/)
  await new Promise<void>((resolve) => setImmediate(resolve))
  fixture.peers[0].setConnectionState('failed')
  await rejected
  assert.equal(fixture.track.stopped, true)
  assert.equal(fixture.peers[0].closed, true)
  assert.equal(fixture.timers.size, 0)
  assert.deepEqual(fixture.releasedMedia(), [{ mediaId: 'media-1', lease: 'lease-1' }])
})

test('an unreachable media channel times out without returning dialable media', async () => {
  const fixture = mediaFixture(true, false)
  const preparing = fixture.controller.prepare()
  const rejected = assert.rejects(preparing, /听筒连接超时/)
  await new Promise<void>((resolve) => setImmediate(resolve))
  assert.equal(fixture.timers.size, 1)
  for (const expire of fixture.timers.values()) expire()
  await rejected
  assert.equal(fixture.track.stopped, true)
  assert.equal(fixture.timers.size, 0)
  assert.deepEqual(fixture.releasedMedia(), [{ mediaId: 'media-1', lease: 'lease-1' }])
})

test('media preparation exposes both the connection and server cleanup failures', async () => {
  const fixture = mediaFixture(true, false, new Error('release denied'))
  const preparing = fixture.controller.prepare()
  const rejected = assert.rejects(preparing, /媒体连接失败.*服务端媒体清理失败：release denied/)
  await new Promise<void>((resolve) => setImmediate(resolve))
  fixture.peers[0].setConnectionState('failed')
  await rejected
  assert.deepEqual(fixture.releasedMedia(), [{ mediaId: 'media-1', lease: 'lease-1' }])
})

test('server media cleanup uses a bounded request', async () => {
  const originalDelete = api.delete
  let observed: { url?: string; timeout?: number; lease?: string } = {}
  const replacement = async (
    url: string,
    config?: { timeout?: number; headers?: Record<string, string> }
  ) => {
    observed = { url, timeout: config?.timeout, lease: config?.headers?.['X-Phone-Lease'] }
    return { data: undefined }
  }
  api.delete = replacement as unknown as typeof api.delete
  try {
    await phoneService.releaseMedia('media with space', 'lease-1')
  } finally {
    api.delete = originalDelete
  }
  assert.deepEqual(observed, {
    url: '/phone/media/media%20with%20space',
    timeout: 5_000,
    lease: 'lease-1'
  })
})
