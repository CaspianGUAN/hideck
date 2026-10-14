package device

import (
	"context"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/mbn"
)

func mbnWorkerOwner(w *Worker) mbn.Owner {
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	return mbn.Owner{DeviceID: w.ID, IMEI: w.state.Identity.IMEI, ICCID: db.CanonicalICCID(w.state.Identity.ICCID), Firmware: w.state.Runtime.Firmware, Generation: w.generation, IdentityGeneration: w.state.Identity.Generation}
}
func (p *Pool) MBNManaged(id string) (bool, error) {
	if p.mbnService == nil {
		return false, nil
	}
	w := p.GetWorker(id)
	if w == nil {
		return false, mbn.ErrOwnerChanged
	}
	policy, err := p.mbnService.EffectivePolicy(context.Background(), mbnWorkerOwner(w))
	return policy.Mode != "inherit", err
}
func (p *Pool) reconcileMBN(w *Worker) (bool, error) {
	if p.mbnService == nil {
		return false, nil
	}
	return p.mbnService.Reconcile(p.Context(), mbnWorkerOwner(w))
}
