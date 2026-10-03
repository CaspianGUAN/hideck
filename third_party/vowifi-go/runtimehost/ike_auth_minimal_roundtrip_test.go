package runtimehost

import (
	"testing"

	"github.com/iniwex5/vowifi-go/internal/vowifi/policy"
	internalprofile "github.com/iniwex5/vowifi-go/internal/vowifi/profile"
)

// The prepared session crosses runtimehost's public carrier types before the
// SWu config is built; a carrier switch must survive the round trip.
func TestIKEAuthMinimalSurvivesPreparedSessionRoundTrip(t *testing.T) {
	plan := policy.CarrierPlanFromEffectiveConfig(policy.EffectiveCarrierConfig{
		MCC: "520", MNC: "03", APN: "ims", IKEAuthMinimal: true,
	})
	public := preparedSessionFromInternal(internalprofile.PreparedSession{CarrierPlan: plan})
	if !public.ResolvedCarrierConfig().IKEAuthMinimal {
		t.Fatal("ike_auth_minimal lost converting to the public carrier config")
	}
	back := preparedSessionPtrToInternal(&public)
	if !back.CarrierPlan.IKE.IKEAuthMinimal {
		t.Fatal("ike_auth_minimal lost converting back to the internal carrier plan")
	}
}
