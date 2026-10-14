package runtimecore

import "testing"

func TestFastReauthStoreNeverReplaysIdentityOnReconnect(t *testing.T) {
	store := &FastReauthStore{}
	var first SessionConfig
	store.Apply(&first)
	first.OnFastReauthUpdate("reauth@nai", []byte{1}, []byte{2}, []byte{3})

	var reconnect SessionConfig
	store.Apply(&reconnect)
	if reconnect.FastReauthID != "" || reconnect.FastReauthMK != nil {
		t.Fatalf("reconnect replayed fast reauth identity %q", reconnect.FastReauthID)
	}
}
