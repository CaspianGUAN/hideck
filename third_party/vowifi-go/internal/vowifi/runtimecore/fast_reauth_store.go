package runtimecore

import (
	"strings"
	"sync"
)

// FastReauthStore records EAP-AKA fast reauthentication material issued by
// the AAA. The identity is deliberately not replayed on reconnect: the engine's
// AKA-Reauthentication handler reads AT_COUNTER/AT_NONCE_S outside AT_ENCR_DATA
// (RFC 4187 9.7 nests them), so every fast reauth attempt failed and cost a
// rejected IKE_AUTH before the fallback. Full EAP-AKA is used instead.
// ponytail: re-enable identity replay once the handler decrypts AT_ENCR_DATA.
type FastReauthStore struct {
	mu    sync.Mutex
	id    string
	mk    []byte
	kAut  []byte
	kEncr []byte
}

func (store *FastReauthStore) Capture() func(string, []byte, []byte, []byte) {
	return func(id string, mk, kAut, kEncr []byte) {
		if store == nil {
			return
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		store.id = strings.TrimSpace(id)
		store.mk = append([]byte(nil), mk...)
		store.kAut = append([]byte(nil), kAut...)
		store.kEncr = append([]byte(nil), kEncr...)
	}
}

func (store *FastReauthStore) Apply(cfg *SessionConfig) {
	if store == nil || cfg == nil {
		return
	}
	capture := store.Capture()
	if cfg.OnFastReauthUpdate == nil {
		cfg.OnFastReauthUpdate = capture
		return
	}
	previous := cfg.OnFastReauthUpdate
	cfg.OnFastReauthUpdate = func(nextID string, nextMK, nextKAut, nextKEncr []byte) {
		capture(nextID, nextMK, nextKAut, nextKEncr)
		previous(nextID, nextMK, nextKAut, nextKEncr)
	}
}
