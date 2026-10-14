package volte

import "context"

type fakeBoundDTMF struct{ modem *FakeModem }

func (f *FakeModem) BindVoiceDTMF(string) (DTMFTransport, error) {
	return fakeBoundDTMF{modem: f}, nil
}

func (f fakeBoundDTMF) VOICEStartContinuousDTMF(ctx context.Context, id, digit uint8) (uint8, error) {
	return id, f.modem.VOICEStartContinuousDTMF(ctx, "", id, digit)
}

func (f fakeBoundDTMF) VOICEStopContinuousDTMF(ctx context.Context, id uint8) (uint8, error) {
	return id, f.modem.VOICEStopContinuousDTMF(ctx, "", id)
}

func (f fakeBoundDTMF) VOICEBurstDTMF(ctx context.Context, id uint8, digit string) (uint8, error) {
	return id, f.modem.VOICEBurstDTMF(ctx, "", id, digit)
}
